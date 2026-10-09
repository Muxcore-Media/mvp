package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Real-provider tests (ADR-0033 S9b): the published userdata-local v0.1.6
// daemon (go.mod tool directive) runs as a subprocess with a fake core
// registration service and a fake auth-local, and the BFF talks to it through
// the production client. This proves provider persistence, admission and the
// packaged userdata-health probe rather than the BFF's local fallback.

var (
	userdataToolsOnce sync.Once
	userdataToolsDir  string
	userdataToolsErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if userdataToolsDir != "" {
		_ = os.RemoveAll(userdataToolsDir)
	}
	os.Exit(code)
}

// userdataTools builds the provider daemon and its health probe once per test
// binary from the versions pinned in go.mod.
func userdataTools(t *testing.T) (daemon, probe string) {
	t.Helper()
	userdataToolsOnce.Do(func() {
		goBin, err := exec.LookPath("go")
		if err != nil {
			userdataToolsErr = fmt.Errorf("go toolchain not on PATH: %w", err)
			return
		}
		dir, err := os.MkdirTemp("", "mvp-userdata-tools-")
		if err != nil {
			userdataToolsErr = err
			return
		}
		userdataToolsDir = dir
		for out, pkg := range map[string]string{
			"userdata-local":  "github.com/Muxcore-Media/userdata-local/cmd/module",
			"userdata-health": "github.com/Muxcore-Media/userdata-local/cmd/userdata-health",
		} {
			cmd := exec.Command(goBin, "build", "-o", filepath.Join(dir, out), pkg)
			if b, err := cmd.CombinedOutput(); err != nil {
				userdataToolsErr = fmt.Errorf("go build %s: %v\n%s", pkg, err, b)
				return
			}
		}
	})
	if userdataToolsErr != nil {
		t.Fatal(userdataToolsErr)
	}
	return filepath.Join(userdataToolsDir, "userdata-local"), filepath.Join(userdataToolsDir, "userdata-health")
}

// providerCore accepts every registration (the SDK registers before Init).
type providerCore struct {
	modulev1.UnimplementedModuleRegistrationServer
}

func (providerCore) Register(context.Context, *modulev1.RegisterRequest) (*modulev1.RegisterResponse, error) {
	return &modulev1.RegisterResponse{Accepted: true}, nil
}

func (providerCore) Unregister(context.Context, *modulev1.UnregisterRequest) (*modulev1.UnregisterResponse, error) {
	return &modulev1.UnregisterResponse{}, nil
}

type providerAuthUser struct {
	id, tenant string
	roles      []string
}

// providerAuth is auth-local's session validation and user listing.
type providerAuth struct {
	authv1.UnimplementedAuthServiceServer
	tokens map[string]providerAuthUser
}

func (a providerAuth) Validate(_ context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
	u, ok := a.tokens[req.GetToken()]
	if id, dynamic := strings.CutPrefix(req.GetToken(), "tok-user-"); !ok && dynamic && id != "" {
		u, ok = providerAuthUser{id: id, roles: []string{"user"}}, true
	}
	if !ok {
		return &authv1.ValidateResponse{Valid: false, Error: "unknown token"}, nil
	}
	return &authv1.ValidateResponse{Valid: true, UserId: u.id, Username: u.id, Roles: u.roles, TenantId: u.tenant}, nil
}

func (a providerAuth) ListUsers(context.Context, *authv1.ListUsersRequest) (*authv1.ListUsersResponse, error) {
	resp := &authv1.ListUsersResponse{}
	for _, u := range a.tokens {
		resp.Users = append(resp.Users, &authv1.UserInfo{Id: u.id, Username: u.id, Roles: u.roles, TenantId: u.tenant})
	}
	return resp, nil
}

var providerAuthTokens = map[string]providerAuthUser{
	"tok-kid":   {id: "kid", roles: []string{"user"}},
	"tok-alice": {id: "alice", roles: []string{"user"}},
	"tok-admin": {id: "admin", roles: []string{"admin"}},
	"tok-bob":   {id: "bob", tenant: "tB", roles: []string{"user"}},
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func serveGRPC(t *testing.T, creds credentials.TransportCredentials, register func(*grpc.Server)) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var opts []grpc.ServerOption
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
	}
	srv := grpc.NewServer(opts...)
	register(srv)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)
	return l.Addr().String()
}

type realProvider struct {
	origin   string // as the BFF dials it
	httpAddr string // USERDATA_LOCAL_HTTP_ADDR
	pki      *testPKI
	id       testIdentity // userdata-local's own identity (household)
	cmd      *exec.Cmd
	done     chan struct{}
	logFile  string
}

// startRealProvider runs the published daemon. household: core-CA mTLS with
// the provider's enrolled identity; otherwise explicit insecure dev.
func startRealProvider(t *testing.T, household bool) *realProvider {
	t.Helper()
	daemon, _ := userdataTools(t)
	dir := t.TempDir()
	p := &realProvider{httpAddr: freeLoopbackAddr(t), logFile: filepath.Join(dir, "provider.log")}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"USERDATA_LOCAL_HTTP_ADDR=" + p.httpAddr,
		"USERDATA_LOCAL_GRPC_ADDR=" + freeLoopbackAddr(t),
		"USERDATA_LOCAL_DB_PATH=" + filepath.Join(dir, "userdata.db"),
		"MUXCORE_DATA_DIR=" + filepath.Join(dir, "data"),
	}
	register := func(s *grpc.Server) { modulev1.RegisterModuleRegistrationServer(s, providerCore{}) }
	registerAuth := func(s *grpc.Server) { authv1.RegisterAuthServiceServer(s, providerAuth{tokens: providerAuthTokens}) }
	if household {
		p.pki = newTestPKI(t, "muxcore-ca")
		p.id = p.pki.enrolled(t, "userdata-local")
		serverCreds := func(id testIdentity) credentials.TransportCredentials {
			return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{id.pair},
				ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: p.pki.pool})
		}
		coreAddr := serveGRPC(t, serverCreds(p.pki.enrolled(t, "muxcore")), register)
		authAddr := serveGRPC(t, serverCreds(p.pki.enrolled(t, "auth-local")), registerAuth)
		env = append(env, "MUXCORE_PROFILE=household", "MUXCORE_GRPC_ADDR="+coreAddr, "AUTH_LOCAL_GRPC_ADDR="+authAddr,
			"MUXCORE_TLS_CERT="+p.id.certFile, "MUXCORE_TLS_KEY="+p.id.keyFile, "MUXCORE_TLS_CA="+p.id.caFile)
		p.origin = "https://" + p.httpAddr
	} else {
		coreAddr := serveGRPC(t, nil, register)
		authAddr := serveGRPC(t, nil, registerAuth)
		env = append(env, "MUXCORE_PROFILE=dev", "MUXCORE_INSECURE_DISABLE_TLS=true",
			"MUXCORE_GRPC_ADDR="+coreAddr, "AUTH_LOCAL_GRPC_ADDR="+authAddr)
		p.origin = "http://" + p.httpAddr
	}
	logf, err := os.Create(p.logFile)
	if err != nil {
		t.Fatal(err)
	}
	p.cmd = exec.Command(daemon)
	p.cmd.Env = env
	p.cmd.Dir = dir
	p.cmd.Stdout, p.cmd.Stderr = logf, logf
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p.done = make(chan struct{})
	go func() { _ = p.cmd.Wait(); _ = logf.Close(); close(p.done) }()
	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			b, _ := os.ReadFile(p.logFile)
			t.Logf("userdata-local log:\n%s", b)
		}
	})
	p.waitReady(t)
	return p
}

func (p *realProvider) stop() {
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// waitReady polls GET /health with the provider's own identity (as the
// packaged probe does).
func (p *realProvider) waitReady(t *testing.T) {
	t.Helper()
	var client userdataProviderClient
	if p.pki != nil {
		client = householdProvider(t, p.origin, "userdata-local", p.id)
	} else {
		client = devUserdataProvider(t, p.origin, time.Second)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		resp, err := client.Do(t.Context(), httpclient.GetHealth, nil, nil)
		if err == nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code == http.StatusOK {
				return
			}
		}
		select {
		case <-p.done:
			b, _ := os.ReadFile(p.logFile)
			t.Fatalf("userdata-local exited before becoming ready:\n%s", b)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("userdata-local not ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// runProbe runs the packaged userdata-health probe with exactly env.
func runProbe(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	_, probe := userdataTools(t)
	cmd := exec.Command(probe, args...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// meshIDDir lays an identity out the way compose mounts it: <dir>/mesh-id
// holds module.crt/module.key (MUXCORE_TLS_DIR) and <dir>/mesh-ca/ca.crt is the
// read-only core CA (MUXCORE_TLS_CA).
func meshIDDir(t *testing.T, id testIdentity) (tlsDir, caFile string) {
	t.Helper()
	root := t.TempDir()
	tlsDir, caDir := filepath.Join(root, "mesh-id"), filepath.Join(root, "mesh-ca")
	for _, d := range []string{tlsDir, caDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for src, dst := range map[string]string{id.certFile: filepath.Join(tlsDir, "module.crt"), id.keyFile: filepath.Join(tlsDir, "module.key"), id.caFile: filepath.Join(caDir, "ca.crt")} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tlsDir, filepath.Join(caDir, "ca.crt")
}

func putUserdataViaBFF(t *testing.T, s *server, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: session})
	w := httptest.NewRecorder()
	s.handleUserdataPut(w, req)
	return w
}

func getUserdataViaBFF(t *testing.T, s *server, session string) store.Blob {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: session})
	w := httptest.NewRecorder()
	s.handleUserdataGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/userdata: %d %s", w.Code, w.Body.String())
	}
	var blob store.Blob
	if err := json.Unmarshal(w.Body.Bytes(), &blob); err != nil {
		t.Fatal(err)
	}
	return blob
}

// blobRoundTrip writes through one BFF and reads back through a second BFF
// whose local store is empty, so only provider persistence can satisfy it.
func blobRoundTrip(t *testing.T, client userdataProviderClient) {
	t.Helper()
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("alice", "alice", "", []string{"user"}, "tok-alice")
	if err != nil {
		t.Fatal(err)
	}
	writer := &server{userdata: newServerUserdata(t.TempDir(), client), sessions: sessions}
	if w := putUserdataViaBFF(t, writer, tok, `{"progress":{"m1":{"id":"m1","kind":"movie","title":"M","href":"/m/1","positionSec":42,"durationSec":100,"updatedAt":"2026-10-09T00:00:00Z"}}}`); w.Code != http.StatusOK {
		t.Fatalf("PUT via BFF: %d %s", w.Code, w.Body.String())
	}
	if writer.userdata.degraded.Load() {
		t.Fatal("provider PUT failed; the write landed only in the local fallback")
	}
	readerDir := t.TempDir()
	reader := &server{userdata: newServerUserdata(readerDir, client), sessions: sessions}
	blob := getUserdataViaBFF(t, reader, tok)
	if reader.userdata.degraded.Load() {
		t.Fatal("provider GET failed")
	}
	var entry struct {
		PositionSec float64 `json:"positionSec"`
	}
	if raw, ok := blob.Progress["m1"]; !ok || json.Unmarshal(raw, &entry) != nil || entry.PositionSec != 42 {
		t.Fatalf("provider did not persist the blob: %+v", blob)
	}
	if local := reader.userdata.store.Get(store.Scope{UserID: "alice"}); len(local.Progress) != 0 {
		t.Fatalf("reader's local store unexpectedly populated: %+v", local)
	}
}

func policyPut(t *testing.T, client userdataProviderClient, bearer, target, policy string, expected int64) (*http.Response, error) {
	t.Helper()
	body := fmt.Sprintf(`{"expected_revision":%d,"policy":%s}`, expected, policy)
	h := http.Header{"Authorization": {"Bearer " + bearer}, muxcoreUserIDHeader: {target}, "Content-Type": {"application/json"}}
	return client.Do(t.Context(), httpclient.PutPolicy, h, bytes.NewReader([]byte(body)))
}

func TestRealUserdataProviderHousehold(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the userdata-local daemon")
	}
	p := startRealProvider(t, true)
	media := householdProvider(t, p.origin, "media-ui", p.pki.enrolled(t, "media-ui"))
	admin := householdProvider(t, p.origin, "admin-ui", p.pki.enrolled(t, "admin-ui"))

	t.Run("blob persistence round trip through the BFF", func(t *testing.T) {
		blobRoundTrip(t, media)
	})

	t.Run("restrictive admin write reaches the BFF within the cache bound", func(t *testing.T) {
		clock := newFakeClock()
		g := newParentalGateWith(media, clock.Now)
		kid := parentalPrincipal{userID: "kid", bearer: "tok-kid"}
		if pol, perr := g.policy(t.Context(), kid); perr != nil || pol.configured {
			t.Fatalf("fresh account: %+v %v, want the validated unconfigured state", pol, perr)
		}
		resp, err := policyPut(t, admin, "tok-admin", "kid", restrictedPolicyJSON, 0)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("admin-ui PUT: %v %v", err, resp)
		}
		_ = resp.Body.Close()
		if pol, perr := g.policy(t.Context(), kid); perr != nil || pol.configured {
			t.Fatalf("within TTL the cached document is used: %+v %v", pol, perr)
		}
		clock.Advance(parentalPolicyTTL)
		pol, perr := g.policy(t.Context(), kid)
		if perr != nil || !pol.configured || pol.unrestricted() || pol.revision != 1 {
			t.Fatalf("after TTL: %+v %v", pol, perr)
		}
	})

	t.Run("media-ui cannot write policy even with an admin bearer", func(t *testing.T) {
		_, err := policyPut(t, media, "tok-admin", "alice", unrestrictedPolicyJSON, 0)
		var ue *httpclient.UnavailableError
		if !errors.As(err, &ue) || ue.Reason != httpclient.ReasonModuleForbidden {
			t.Fatalf("media-ui policy PUT: %v, want module_forbidden", err)
		}
	})

	t.Run("admin-ui with a member bearer gets the application denial", func(t *testing.T) {
		resp, err := policyPut(t, admin, "tok-alice", "kid", unrestrictedPolicyJSON, 1)
		if err != nil {
			t.Fatalf("application denial reported as unavailability: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "policy.forbidden") {
			t.Fatalf("member write: %d %s", resp.StatusCode, b)
		}
	})

	t.Run("module admission denial is 503 and blob fallback is not success", func(t *testing.T) {
		monitor := householdProvider(t, p.origin, "health-monitor", p.pki.enrolled(t, "health-monitor"))
		g := newParentalGateWith(monitor, newFakeClock().Now)
		if _, perr := g.policy(t.Context(), parentalPrincipal{userID: "kid", bearer: "tok-kid"}); perr != errParentalUnavail {
			t.Fatalf("admission denial: %v, want parental.policy_unavailable", perr)
		}
		u := newServerUserdata(t.TempDir(), monitor)
		if blob := loadBlob(t, u, store.Scope{UserID: "alice"}, "tok-alice"); len(blob.Progress) != 0 || !u.degraded.Load() {
			t.Fatalf("denied blob read: %+v degraded=%v", blob, u.degraded.Load())
		}
	})

	t.Run("untrusted BFF certificate fails the handshake", func(t *testing.T) {
		rogue := householdProvider(t, p.origin, "media-ui", newTestPKI(t, "rogue").enrolled(t, "media-ui"))
		_, err := rogue.Do(t.Context(), httpclient.GetHealth, nil, nil)
		var ue *httpclient.UnavailableError
		if !errors.As(err, &ue) || ue.Reason != httpclient.ReasonTransport {
			t.Fatalf("rogue client: %v", err)
		}
	})

	t.Run("plaintext client to the secure port is refused", func(t *testing.T) {
		plain := devUserdataProvider(t, "http://"+p.httpAddr, 3*time.Second)
		resp, err := plain.Do(t.Context(), httpclient.GetPolicy, http.Header{"Authorization": {"Bearer tok-kid"}, muxcoreUserIDHeader: {"kid"}}, nil)
		if err == nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code == http.StatusOK {
				t.Fatal("secure listener answered a plaintext policy read")
			}
		}
	})

	t.Run("packaged userdata-health probe", func(t *testing.T) {
		port := p.httpAddr[strings.LastIndex(p.httpAddr, ":"):]
		tlsDir, caFile := meshIDDir(t, p.id)
		// Compose shape: USERDATA_LOCAL_HTTP_ADDR=":<port>", the identity volume at
		// MUXCORE_TLS_DIR and the read-only core CA at MUXCORE_TLS_CA.
		composeEnv := []string{"MUXCORE_PROFILE=household", "USERDATA_LOCAL_HTTP_ADDR=" + port, "MUXCORE_TLS_DIR=" + tlsDir, "MUXCORE_TLS_CA=" + caFile}
		if out, err := runProbe(t, composeEnv); err != nil {
			t.Fatalf("probe with the provider identity: %v\n%s", err, out)
		}
		// Host shape: explicit files and an explicit loopback origin.
		if out, err := runProbe(t, []string{"MUXCORE_PROFILE=household", "MUXCORE_TLS_CERT=" + p.id.certFile, "MUXCORE_TLS_KEY=" + p.id.keyFile, "MUXCORE_TLS_CA=" + p.id.caFile}, "--origin", p.origin); err != nil {
			t.Fatalf("probe with explicit files: %v\n%s", err, out)
		}
		otherDir, otherCA := meshIDDir(t, p.pki.enrolled(t, "media-ui"))
		for name, env := range map[string][]string{
			"another module's identity": {"MUXCORE_PROFILE=household", "USERDATA_LOCAL_HTTP_ADDR=" + port, "MUXCORE_TLS_DIR=" + otherDir, "MUXCORE_TLS_CA=" + otherCA},
			"no identity":               {"MUXCORE_PROFILE=household", "USERDATA_LOCAL_HTTP_ADDR=" + port, "MUXCORE_TLS_DIR=" + t.TempDir()},
			"insecure flag":             {"MUXCORE_PROFILE=household", "MUXCORE_INSECURE_DISABLE_TLS=true", "USERDATA_LOCAL_HTTP_ADDR=" + port},
		} {
			if out, err := runProbe(t, env); err == nil {
				t.Errorf("probe succeeded with %s: %s", name, out)
			}
		}
	})

	t.Run("provider outage after cache expiry fails closed", func(t *testing.T) {
		clock := newFakeClock()
		g := newParentalGateWith(media, clock.Now)
		alice := parentalPrincipal{userID: "alice", bearer: "tok-alice"}
		if pol, perr := g.policy(t.Context(), alice); perr != nil || pol.configured {
			t.Fatalf("prime: %+v %v", pol, perr)
		}
		p.stop()
		clock.Advance(parentalPolicyTTL)
		if _, perr := g.policy(t.Context(), alice); perr != errParentalUnavail {
			t.Fatalf("outage: %v, want parental.policy_unavailable", perr)
		}
	})
}

// Explicit insecure dev keeps working exactly as before: plaintext HTTP with
// user-bearer authorization and the same canonical routes; the household
// client never downgrades to it.
func TestRealUserdataProviderDev(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the userdata-local daemon")
	}
	p := startRealProvider(t, false)
	media := devUserdataProvider(t, p.origin, 3*time.Second)
	blobRoundTrip(t, media)
	g := newParentalGateWith(media, newFakeClock().Now)
	if pol, perr := g.policy(t.Context(), parentalPrincipal{userID: "kid", bearer: "tok-kid"}); perr != nil || pol.configured {
		t.Fatalf("dev policy read: %+v %v, want the validated unconfigured state", pol, perr)
	}
	pki := newTestPKI(t, "muxcore-ca")
	secure := householdProvider(t, "https://"+p.httpAddr, "media-ui", pki.enrolled(t, "media-ui"))
	if _, perr := newParentalGateWith(secure, newFakeClock().Now).policy(t.Context(), parentalPrincipal{userID: "kid", bearer: "tok-kid"}); perr != errParentalUnavail {
		t.Fatalf("household client against a plaintext provider: %v", perr)
	}
	port := p.httpAddr[strings.LastIndex(p.httpAddr, ":"):]
	if out, err := runProbe(t, []string{"MUXCORE_PROFILE=dev", "MUXCORE_INSECURE_DISABLE_TLS=true", "USERDATA_LOCAL_HTTP_ADDR=" + port}); err != nil {
		t.Fatalf("dev probe: %v\n%s", err, out)
	}
}
