package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure/erasuretest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const fixtureBearer = "fixture-admin-session-SECRET"

// Unlike erasuretest.Provider, this service enforces both production admission
// boundaries. Every RPC, including an accidental write, is recorded.
type acquisitionFixture struct {
	authv1.UnimplementedAuthServiceServer
	discoveryv1.UnimplementedDiscoveryServiceServer
	cfg          providerConfig
	expected     Expected
	stone        *authv1.UserErasure
	response     *authv1.GetUserErasureStatusResponse
	discovery    *discoveryv1.FindByCapabilityResponse
	list         func(int, *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error)
	statusErr    error
	discoveryErr error
	adminBearer  string
	delays       map[string]time.Duration
	mu           sync.Mutex
	calls        []fixtureCall
	listCount    int
}

type fixtureCall struct {
	method   string
	md       metadata.MD
	cn       string
	deadline time.Time
	request  any
}

func (f *acquisitionFixture) intercept(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	p, ok := peer.FromContext(ctx)
	cn := ""
	if ok {
		if ti, valid := p.AuthInfo.(credentials.TLSInfo); valid && len(ti.State.VerifiedChains) > 0 {
			cn = ti.State.VerifiedChains[0][0].Subject.CommonName
		}
	}
	deadline, _ := ctx.Deadline()
	f.mu.Lock()
	f.calls = append(f.calls, fixtureCall{info.FullMethod, md.Copy(), cn, deadline, req})
	f.mu.Unlock()
	if cn == "" {
		return nil, status.Error(codes.Unauthenticated, "fixture requires verified CN SECRET")
	}
	switch info.FullMethod {
	case discoveryv1.DiscoveryService_FindByCapability_FullMethodName:
		if len(md.Get("x-auth-token")) != 0 {
			return nil, status.Error(codes.PermissionDenied, "discovery bearer leak SECRET")
		}
	case authv1.AuthService_ListUserErasures_FullMethodName:
		if cn != "userdata-local" || len(md.Get("x-auth-token")) != 0 {
			return nil, status.Error(codes.PermissionDenied, "ledger admission SECRET")
		}
	case authv1.AuthService_GetUserErasureStatus_FullMethodName:
		vals := md.Get("x-auth-token")
		if len(vals) != 1 || vals[0] != f.adminBearer {
			return nil, status.Error(codes.Unauthenticated, "admin session SECRET")
		}
	default:
		return nil, status.Error(codes.Unimplemented, "unexpected method SECRET")
	}
	if delay := f.delays[info.FullMethod]; delay != 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	return handler(ctx, req)
}

func (f *acquisitionFixture) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.Capability != "identity" {
		return nil, status.Error(codes.InvalidArgument, "wrong capability")
	}
	return f.discovery, f.discoveryErr
}
func (f *acquisitionFixture) ListUserErasures(_ context.Context, req *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error) {
	f.mu.Lock()
	f.listCount++
	count := f.listCount
	f.mu.Unlock()
	if req.PageSize != ledgerPageSize {
		return nil, status.Error(codes.InvalidArgument, "wrong page size")
	}
	if f.list != nil {
		return f.list(count, req)
	}
	return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{f.stone}}, nil
}
func (f *acquisitionFixture) GetUserErasureStatus(_ context.Context, req *authv1.GetUserErasureStatusRequest) (*authv1.GetUserErasureStatusResponse, error) {
	if req.ErasureId != f.expected.ErasureID || req.PendingOnly || req.PageToken != "" || req.PageSize != 0 {
		return nil, status.Error(codes.InvalidArgument, "status must target exact E")
	}
	return f.response, f.statusErr
}

func serveAcquisitionTLS(t *testing.T, clientCA, issuer *erasuretest.PKI, cn, san string, f *acquisitionFixture, core bool) string {
	t.Helper()
	cert, key := issuer.Issue(t, cn, san)
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(clientCA.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert})), grpc.UnaryInterceptor(f.intercept),
		grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			method, _ := grpc.MethodFromServerStream(stream)
			f.mu.Lock()
			f.calls = append(f.calls, fixtureCall{method: method})
			f.mu.Unlock()
			return status.Error(codes.Unimplemented, "unexpected service SECRET")
		}))
	if core {
		discoveryv1.RegisterDiscoveryServiceServer(server, f)
	} else {
		authv1.RegisterAuthServiceServer(server, f)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	return listener.Addr().String()
}

func newAcquisitionFixture(t *testing.T, tlsCase string) *acquisitionFixture {
	t.Helper()
	for _, name := range []string{"MUXCORE_INSECURE_DISABLE_TLS", "MUXCORE_DEV_TLS_SKIP", "MUXCORE_GRPC_INSECURE", "MUXCORE_TLS_SERVER_NAME", "MUXCORE_MESH_DIAL_LOCAL", "MUXCORE_TLS_DIR", "MUXCORE_TLS_CERT", "MUXCORE_TLS_KEY", "MUXCORE_TLS_CA", "MUXCORE_MODULE_ID", "MUXCORE_GRPC_ADDR"} {
		t.Setenv(name, "")
	}
	pki := erasuretest.NewPKI(t)
	coreIssuer, authIssuer := pki, pki
	coreCN, authCN, callerCN := "muxcored", "auth-local", "userdata-local"
	switch tlsCase {
	case "core CN":
		coreCN = "wrong-core"
	case "provider CN":
		authCN = "wrong-provider"
	case "core CA":
		coreIssuer = erasuretest.NewPKI(t)
	case "provider CA":
		authIssuer = erasuretest.NewPKI(t)
	case "caller CN", "unallowlisted caller":
		callerCN = "other-consumer"
	}
	e, stone, response := validSnapshots()
	f := &acquisitionFixture{expected: e, stone: stone, response: response, delays: map[string]time.Duration{}, adminBearer: fixtureBearer}
	f.cfg = providerConfig{callerID: "userdata-local", caFile: pki.CAFile}
	f.cfg.certFile, f.cfg.keyFile = pki.Issue(t, callerCN)
	if tlsCase == "unallowlisted caller" {
		f.cfg.callerID = callerCN
	}
	addr := serveAcquisitionTLS(t, pki, authIssuer, authCN, "auth-local", f, false)
	f.discovery = &discoveryv1.FindByCapabilityResponse{Modules: []*discoveryv1.ModuleInfoProto{{Id: "auth-local", HttpAddr: addr}}}
	f.cfg.coreAddr = serveAcquisitionTLS(t, pki, coreIssuer, coreCN, "muxcored", f, true)
	t.Cleanup(func() {
		for _, c := range f.recorded() {
			switch c.method {
			case discoveryv1.DiscoveryService_FindByCapability_FullMethodName, authv1.AuthService_ListUserErasures_FullMethodName, authv1.AuthService_GetUserErasureStatus_FullMethodName:
			default:
				t.Errorf("unexpected RPC (writes are forbidden): %s", c.method)
			}
		}
	})
	return f
}
func (f *acquisitionFixture) recorded() []fixtureCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fixtureCall(nil), f.calls...)
}
func (f *acquisitionFixture) env(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{"MUXCORE_MODULE_ID": f.cfg.callerID, "MUXCORE_GRPC_ADDR": f.cfg.coreAddr,
		"MUXCORE_TLS_CERT": f.cfg.certFile, "MUXCORE_TLS_KEY": f.cfg.keyFile, "MUXCORE_TLS_CA": f.cfg.caFile} {
		t.Setenv(key, value)
	}
}
func (f *acquisitionFixture) check(t *testing.T, token string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return acquireProvider(ctx, f.cfg, f.expected, token)
}
func bystander(n int) *authv1.UserErasure {
	return &authv1.UserErasure{ErasureId: fmt.Sprintf("bystander-%d", n), UserId: "other-user", TenantId: "other-tenant", DeletedAt: deletedAt}
}

func TestAcquireProviderAdmissionAndMetadata(t *testing.T) {
	f := newAcquisitionFixture(t, "")
	identityFiles := func() map[string]string {
		t.Helper()
		files := make(map[string]string)
		entries, err := os.ReadDir(filepath.Dir(f.cfg.certFile))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, readErr := os.ReadFile(filepath.Join(filepath.Dir(f.cfg.certFile), entry.Name()))
			if readErr != nil {
				t.Fatal(readErr)
			}
			files[entry.Name()] = string(data)
		}
		return files
	}
	before := identityFiles()
	t.Setenv("MUXCORE_BOOTSTRAP_TOKEN", "SECRET-unused-enrollment-token")
	// Poisoned inherited metadata must be replaced, not appended to.
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-auth-token", "SECRET-first", "x-auth-token", "SECRET-second", "authorization", "Bearer SECRET", "x-caller-id", "spoof"))
	ctx, cancel := context.WithTimeout(ctx, providerRPCBudget)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if err := acquireProvider(ctx, f.cfg, f.expected, fixtureBearer); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(before, identityFiles()) {
		t.Fatal("identity files changed")
	}
	calls := f.recorded()
	if len(calls) != 3 {
		t.Fatalf("calls=%d", len(calls))
	}
	for i, c := range calls {
		if c.cn != "userdata-local" || len(c.md.Get("authorization")) != 0 {
			t.Fatalf("bad identity/metadata at call %d", i)
		}
		if c.deadline.IsZero() || c.deadline.Sub(deadline).Abs() > 100*time.Millisecond {
			t.Fatalf("deadline changed at call %d", i)
		}
		tokens := c.md.Get("x-auth-token")
		if i < 2 && len(tokens) != 0 {
			t.Fatalf("bearer leaked to call %d", i)
		}
		if i == 2 && (len(tokens) != 1 || tokens[0] != fixtureBearer) {
			t.Fatal("status lacks exact bearer")
		}
		if i > 0 && (len(c.md.Get("x-caller-id")) != 1 || c.md.Get("x-caller-id")[0] != "userdata-local") {
			t.Fatal("caller hint was not replaced")
		}
	}
}

func TestAcquireProviderIdentityFailures(t *testing.T) {
	for _, name := range []string{"core CN", "provider CN", "core CA", "provider CA", "caller CN", "unallowlisted caller"} {
		t.Run(name, func(t *testing.T) {
			f := newAcquisitionFixture(t, name)
			if err := f.check(t, fixtureBearer); err == nil {
				t.Fatal("accepted invalid transport identity")
			}
			for _, c := range f.recorded() {
				if c.method == authv1.AuthService_GetUserErasureStatus_FullMethodName {
					t.Fatal("status called after identity failure")
				}
			}
		})
	}
}

func TestAcquireProviderDiscoveryAndStatusFailures(t *testing.T) {
	cases := map[string]func(*acquisitionFixture){
		"missing provider": func(f *acquisitionFixture) { f.discovery.Modules = nil },
		"missing address":  func(f *acquisitionFixture) { f.discovery.Modules[0].HttpAddr = "" },
		"ambiguous": func(f *acquisitionFixture) {
			f.discovery.Modules = append(f.discovery.Modules, &discoveryv1.ModuleInfoProto{Id: "other-auth", HttpAddr: "127.0.0.1:1"})
		},
		"discovery unimplemented": func(f *acquisitionFixture) { f.discoveryErr = status.Error(codes.Unimplemented, "SECRET") },
		"unreachable provider":    func(f *acquisitionFixture) { f.discovery.Modules[0].HttpAddr = "127.0.0.1:1" },
		"status expired":          func(f *acquisitionFixture) { f.statusErr = status.Error(codes.Unauthenticated, "SECRET") },
		"status nonadmin":         func(f *acquisitionFixture) { f.statusErr = status.Error(codes.PermissionDenied, "SECRET") },
		"status wrong tenant":     func(f *acquisitionFixture) { f.statusErr = status.Error(codes.NotFound, "SECRET") },
		"status pending":          func(f *acquisitionFixture) { f.response.Erasures[0].Complete = false },
		"status required set":     func(f *acquisitionFixture) { f.response.Erasures[0].Modules[0].Required = false },
		"status wrong target":     func(f *acquisitionFixture) { f.response.Erasures[0].ErasureId = "other" },
		"status missing":          func(f *acquisitionFixture) { f.response.Erasures = nil },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAcquisitionFixture(t, "")
			edit(f)
			if err := f.check(t, fixtureBearer); err == nil {
				t.Fatal("accepted invalid provider observation")
			}
		})
	}
	for _, token := range []string{"", "not-admin", "expired-session"} {
		t.Run("bearer/"+token, func(t *testing.T) {
			f := newAcquisitionFixture(t, "")
			if err := f.check(t, token); err == nil {
				t.Fatal("accepted missing/invalid admin session")
			}
		})
	}
}

func TestAcquireProviderPagination(t *testing.T) {
	for _, targetPage := range []int{1, 3, 100} {
		t.Run(fmt.Sprint(targetPage), func(t *testing.T) {
			f := newAcquisitionFixture(t, "")
			f.list = func(n int, req *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error) {
				want := ""
				if n > 1 {
					want = fmt.Sprint(n - 1)
				}
				if req.PageToken != want {
					return nil, errors.New("wrong continuation")
				}
				rows := []*authv1.UserErasure{bystander(n)}
				if n == targetPage {
					rows = append(rows, f.stone, bystander(100+n))
				}
				// A valid continuation after the matching page is deliberately unvisited.
				return &authv1.ListUserErasuresResponse{Erasures: rows, NextPageToken: fmt.Sprint(n)}, nil
			}
			if err := f.check(t, fixtureBearer); err != nil {
				t.Fatal(err)
			}
			if f.listCount != targetPage {
				t.Fatalf("calls=%d want=%d", f.listCount, targetPage)
			}
		})
	}
}

func TestAcquireProviderRejectsPages(t *testing.T) {
	type editPage func(*acquisitionFixture, int) *authv1.ListUserErasuresResponse
	cases := map[string]editPage{
		"duplicate after target": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{f.stone, proto.Clone(f.stone).(*authv1.UserErasure)}}
		},
		"conflict after target": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			s := proto.Clone(f.stone).(*authv1.UserErasure)
			s.UserId = "wrong"
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{f.stone, s}}
		},
		"nil after target": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{f.stone, nil}}
		},
		"malformed after target": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			s := bystander(1)
			s.DeletedAt = "bad"
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{f.stone, s}}
		},
		"wrong user": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			s := proto.Clone(f.stone).(*authv1.UserErasure)
			s.UserId = "wrong"
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{s}}
		},
		"wrong tenant": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			s := proto.Clone(f.stone).(*authv1.UserErasure)
			s.TenantId = "wrong"
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{s}}
		},
		"oversized next after target": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{f.stone}, NextPageToken: strings.Repeat("x", maxLedgerToken+1)}
		},
		"cyclic next on found page": func(f *acquisitionFixture, n int) *authv1.ListUserErasuresResponse {
			s := bystander(n)
			if n == 2 {
				s = f.stone
			}
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{s}, NextPageToken: "next"}
		},
		"multitoken cycle": func(_ *acquisitionFixture, n int) *authv1.ListUserErasuresResponse {
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{bystander(n)}, NextPageToken: fmt.Sprint(n % 2)}
		},
		"empty continuation": func(_ *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			return &authv1.ListUserErasuresResponse{NextPageToken: "next"}
		},
		"absent": func(_ *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			return &authv1.ListUserErasuresResponse{}
		},
		"101 rows": func(f *acquisitionFixture, _ int) *authv1.ListUserErasuresResponse {
			rows := []*authv1.UserErasure{f.stone}
			for n := range 100 {
				rows = append(rows, bystander(n))
			}
			return &authv1.ListUserErasuresResponse{Erasures: rows}
		},
		"page cap": func(_ *acquisitionFixture, n int) *authv1.ListUserErasuresResponse {
			return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{bystander(n)}, NextPageToken: fmt.Sprint(n)}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAcquisitionFixture(t, "")
			f.list = func(n int, _ *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error) {
				return edit(f, n), nil
			}
			if err := f.check(t, fixtureBearer); err == nil {
				t.Fatal("accepted invalid ledger page")
			}
			if f.listCount > maxLedgerPages {
				t.Fatal("exceeded page cap")
			}
			if name == "page cap" && f.listCount != maxLedgerPages {
				t.Fatal("page-cap fixture failed early")
			}
			for _, c := range f.recorded() {
				if c.method == authv1.AuthService_GetUserErasureStatus_FullMethodName {
					t.Fatal("status called before entire page validated")
				}
			}
		})
	}
}

// A nil response cannot be represented on the protobuf wire. Exercise that
// defensive branch directly; decode-limit tests below use real gRPC instead.
type nilLedgerClient struct{ authv1.AuthServiceClient }

func (nilLedgerClient) ListUserErasures(context.Context, *authv1.ListUserErasuresRequest, ...grpc.CallOption) (*authv1.ListUserErasuresResponse, error) {
	return nil, nil
}
func TestAcquireTombstoneNilResponse(t *testing.T) {
	e, _, _ := validSnapshots()
	if _, err := acquireTombstone(context.Background(), nilLedgerClient{}, e); err == nil {
		t.Fatal("accepted nil response")
	}
}

func TestAcquireProviderDecodeLimits(t *testing.T) {
	for _, phase := range []string{"discovery", "list", "status"} {
		t.Run(phase, func(t *testing.T) {
			f := newAcquisitionFixture(t, "")
			huge := strings.Repeat("x", maxSnapshotBytes+1)
			want := checkFailure("")
			switch phase {
			case "discovery":
				f.discovery.Modules[0].Description = huge
				want = "provider_discovery"
			case "list":
				f.list = func(_ int, _ *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error) {
					s := proto.Clone(f.stone).(*authv1.UserErasure)
					s.UserId = huge
					return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{s}}, nil
				}
				want = "provider_ledger"
			case "status":
				f.response.Erasures[0].Modules[0].DetailCode = huge
				want = "provider_status"
			}
			if err := f.check(t, fixtureBearer); err != want {
				t.Fatalf("error=%v want wire error %v", err, want)
			}
		})
	}
}

func TestAcquireProviderSharedDeadline(t *testing.T) {
	f := newAcquisitionFixture(t, "")
	// Individually these phases fit the parent budget, cumulatively they do not.
	f.delays[discoveryv1.DiscoveryService_FindByCapability_FullMethodName] = 70 * time.Millisecond
	f.delays[authv1.AuthService_ListUserErasures_FullMethodName] = 70 * time.Millisecond
	f.delays[authv1.AuthService_GetUserErasureStatus_FullMethodName] = 70 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := acquireProvider(ctx, f.cfg, f.expected, fixtureBearer); err == nil {
		t.Fatal("deadline was reset between phases")
	}
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("failed to obey shared deadline: %v", elapsed)
	}
	calls := f.recorded()
	if len(calls) < 2 {
		t.Fatal("fixture did not span phases")
	}
	deadline, _ := ctx.Deadline()
	for _, c := range calls {
		if c.deadline.Sub(deadline).Abs() > 50*time.Millisecond {
			t.Fatal("RPC deadline changed")
		}
	}
}

func copyIdentity(t *testing.T, cfg providerConfig) string {
	t.Helper()
	dir := t.TempDir()
	for src, name := range map[string]string{cfg.certFile: "module.crt", cfg.keyFile: "module.key", cfg.caFile: "ca.crt"} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func TestProviderConfigIdentityPrecedence(t *testing.T) {
	for _, name := range []string{"explicit", "directory", "directory pair explicit CA", "explicit pair directory CA", "partial cert", "partial key", "bad explicit cert", "bad explicit key", "bad explicit CA", "missing identity", "missing core", "missing caller"} {
		t.Run(name, func(t *testing.T) {
			f := newAcquisitionFixture(t, "")
			f.env(t)
			dir := copyIdentity(t, f.cfg)
			t.Setenv("MUXCORE_TLS_DIR", dir)
			wantOK := true
			switch name {
			case "directory":
				t.Setenv("MUXCORE_TLS_CERT", "")
				t.Setenv("MUXCORE_TLS_KEY", "")
				t.Setenv("MUXCORE_TLS_CA", "")
			case "directory pair explicit CA":
				t.Setenv("MUXCORE_TLS_CERT", "")
				t.Setenv("MUXCORE_TLS_KEY", "")
				if err := os.Remove(filepath.Join(dir, "ca.crt")); err != nil {
					t.Fatal(err)
				}
			case "explicit pair directory CA":
				t.Setenv("MUXCORE_TLS_CA", "")
				if err := os.Remove(filepath.Join(dir, "module.crt")); err != nil {
					t.Fatal(err)
				}
			case "partial cert":
				t.Setenv("MUXCORE_TLS_KEY", "")
				wantOK = false
			case "partial key":
				t.Setenv("MUXCORE_TLS_CERT", "")
				wantOK = false
			case "bad explicit cert":
				t.Setenv("MUXCORE_TLS_CERT", filepath.Join(dir, "missing"))
				wantOK = false
			case "bad explicit key":
				t.Setenv("MUXCORE_TLS_KEY", filepath.Join(dir, "missing"))
				wantOK = false
			case "bad explicit CA":
				t.Setenv("MUXCORE_TLS_CA", filepath.Join(dir, "missing"))
				wantOK = false
			case "missing identity":
				t.Setenv("MUXCORE_TLS_DIR", "")
				t.Setenv("MUXCORE_TLS_CERT", "")
				t.Setenv("MUXCORE_TLS_KEY", "")
				wantOK = false
			case "missing core":
				t.Setenv("MUXCORE_GRPC_ADDR", "")
				wantOK = false
			case "missing caller":
				t.Setenv("MUXCORE_MODULE_ID", "")
				wantOK = false
			}
			cfg, err := providerConfigFromEnv()
			if err == nil {
				f.cfg = cfg
				err = f.check(t, fixtureBearer)
			}
			if (err == nil) != wantOK {
				t.Fatalf("error=%v wantOK=%v", err, wantOK)
			}
		})
	}
}
func TestProviderConfigRejectsInsecure(t *testing.T) {
	for _, name := range []string{"MUXCORE_INSECURE_DISABLE_TLS", "MUXCORE_DEV_TLS_SKIP", "MUXCORE_GRPC_INSECURE"} {
		for _, value := range []string{"true", "1", " TrUe "} {
			t.Run(name+value, func(t *testing.T) {
				f := newAcquisitionFixture(t, "")
				f.env(t)
				t.Setenv("MUXCORE_PROFILE", "dev")
				t.Setenv(name, value)
				if _, err := providerConfigFromEnv(); err == nil {
					t.Fatal("accepted plaintext configuration")
				}
				if len(f.recorded()) != 0 {
					t.Fatal("connected with insecure configuration")
				}
			})
		}
	}
}

func bearerPath(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SECRET-session")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadAdminBearer(t *testing.T) {
	cases := map[string]struct {
		input string
		valid bool
	}{
		"raw": {fixtureBearer, true}, "terminal newline": {fixtureBearer + "\n", true}, "exact bound": {strings.Repeat("a", maxBearerBytes), true},
		"empty": {"", false}, "whitespace": {" \t\n", false}, "too large": {strings.Repeat("a", maxBearerBytes+1), false},
		"opaque prefixed session": {"mct_SECRET", true}, "Bearer prefix": {"Bearer " + fixtureBearer, false}, "two tokens": {"one\ntwo", false},
		"control": {"one\x00two", false}, "non ASCII": {"session-ÿ", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			token, err := readAdminBearer(bearerPath(t, tc.input), openAdminBearer)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && token != strings.TrimSpace(tc.input) {
				t.Fatal("token changed")
			}
		})
	}
	for _, name := range []string{"missing", "directory", "symlink"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "SECRET-path")
			switch name {
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(bearerPath(t, fixtureBearer), path); err != nil {
					t.Fatal(err)
				}
			}
			opened := false
			_, err := readAdminBearer(path, func(string) (bearerInput, error) { opened = true; return nil, errors.New("SECRET") })
			if err == nil || opened {
				t.Fatal("nonregular/missing path was opened")
			}
		})
	}
	t.Run("open failure", func(t *testing.T) {
		_, err := readAdminBearer(bearerPath(t, fixtureBearer), func(string) (bearerInput, error) { return nil, errors.New("SECRET") })
		if err == nil {
			t.Fatal("accepted open error")
		}
	})
}

type hookedBearer struct {
	*os.File
	stat func() (os.FileInfo, error)
	read func([]byte) (int, error)
}

func (f hookedBearer) Stat() (os.FileInfo, error) {
	if f.stat != nil {
		return f.stat()
	}
	return f.File.Stat()
}
func (f hookedBearer) Read(p []byte) (int, error) {
	if f.read != nil {
		return f.read(p)
	}
	return f.File.Read(p)
}

func TestReadAdminBearerRechecksOpenedFile(t *testing.T) {
	for _, name := range []string{"replaced regular", "replaced directory", "replaced pipe", "stat error", "read error", "growth after stat"} {
		t.Run(name, func(t *testing.T) {
			path := bearerPath(t, fixtureBearer)
			opener := func(string) (bearerInput, error) {
				switch name {
				case "replaced regular":
					return os.Open(bearerPath(t, fixtureBearer))
				case "replaced directory":
					return os.Open(t.TempDir())
				case "replaced pipe":
					reader, writer, err := os.Pipe()
					if err != nil {
						return nil, err
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					return reader, nil
				}
				f, err := os.Open(path)
				if err != nil {
					return nil, err
				}
				h := hookedBearer{File: f}
				switch name {
				case "stat error":
					h.stat = func() (os.FileInfo, error) { return nil, errors.New("SECRET") }
				case "read error":
					h.read = func([]byte) (int, error) { return 0, errors.New("SECRET") }
				case "growth after stat":
					h.stat = func() (os.FileInfo, error) {
						info, statErr := f.Stat()
						if statErr != nil {
							return nil, statErr
						}
						// Preserve inode; return the pre-growth stat. Only the read's max+1
						// guard can reject the file once it grows beyond the checked size.
						if writeErr := os.WriteFile(path, []byte(strings.Repeat("a", maxBearerBytes+1)), 0o600); writeErr != nil {
							t.Fatal(writeErr)
						}
						return info, nil
					}
				}
				return h, nil
			}
			if _, err := readAdminBearer(path, opener); err == nil {
				t.Fatal("accepted replaced/unreadable/growing file")
			}
		})
	}
}

func TestAcquireProviderValidBoundaries(t *testing.T) {
	f := newAcquisitionFixture(t, "")
	f.list = func(_ int, _ *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error) {
		rows := []*authv1.UserErasure{f.stone}
		for n := range ledgerPageSize - 1 {
			rows = append(rows, bystander(n))
		}
		return &authv1.ListUserErasuresResponse{Erasures: rows, NextPageToken: strings.Repeat("a", maxLedgerToken)}, nil
	}
	f.response.Erasures[0].Modules[0].DetailCode = strings.Repeat("a", maxSnapshotBytes-1024)
	if err := f.check(t, fixtureBearer); err != nil {
		t.Fatal(err)
	}
}
