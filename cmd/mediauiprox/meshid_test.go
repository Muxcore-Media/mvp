package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/meshid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestEnsureMeshIdentity_PassesModuleIDAndInsecure(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		wantID       string
		wantInsecure bool
	}{
		{"default id", map[string]string{}, "media-ui", false},
		{"explicit id", map[string]string{"MUXCORE_MODULE_ID": " media-ui-2 "}, "media-ui-2", false},
		{"dev insecure", map[string]string{"MUXCORE_INSECURE_DISABLE_TLS": "true"}, "media-ui", true},
		{"dev insecure 1", map[string]string{"MUXCORE_INSECURE_DISABLE_TLS": "1"}, "media-ui", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got meshid.Config
			fake := func(_ context.Context, cfg meshid.Config) (meshid.Paths, error) {
				got = cfg
				return meshid.Paths{Cert: "/id/module.crt"}, nil
			}
			p, err := ensureMeshIdentity(context.Background(), fake, envMap(tc.env))
			if err != nil {
				t.Fatalf("ensureMeshIdentity: %v", err)
			}
			if p.Cert != "/id/module.crt" {
				t.Errorf("paths = %+v", p)
			}
			if got.ModuleID != tc.wantID || got.Insecure != tc.wantInsecure {
				t.Errorf("config ModuleID=%q Insecure=%v, want %q %v", got.ModuleID, got.Insecure, tc.wantID, tc.wantInsecure)
			}
			if got.Getenv == nil {
				t.Error("Getenv not passed through")
			}
		})
	}
}

func TestEnsureMeshIdentity_ErrorNamesModule(t *testing.T) {
	fake := func(context.Context, meshid.Config) (meshid.Paths, error) {
		return meshid.Paths{}, meshid.ErrNoIdentity
	}
	_, err := ensureMeshIdentity(context.Background(), fake, envMap(nil))
	if !errors.Is(err, meshid.ErrNoIdentity) || !strings.Contains(err.Error(), `"media-ui"`) {
		t.Fatalf("err = %v, want ErrNoIdentity naming media-ui", err)
	}
}

// Household end to end with a fake core: the real meshid.Ensure enrolls over
// BootstrapRegister, and the exported MUXCORE_TLS_* make meshGRPCDialOptions
// present the enrolled certificate to a peer module that requires it.
func TestEnsureMeshIdentity_EnrollsAndDialsWithTLS(t *testing.T) {
	ca := newTestCA(t)
	const token = "mct_2_media-ui_fixture"
	coreAddr := startFakeCore(t, ca, token)

	dir := t.TempDir()
	exportDir := filepath.Join(dir, "mesh-ca")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(exportDir, "ca.crt"), ca.certPEM)
	idDir := filepath.Join(dir, "mesh-id")
	for k, v := range map[string]string{
		"MUXCORE_PROFILE":              "household",
		"MUXCORE_INSECURE_DISABLE_TLS": "",
		"MUXCORE_MODULE_ID":            "media-ui",
		"MUXCORE_GRPC_ADDR":            coreAddr,
		"MUXCORE_BOOTSTRAP_TOKEN":      token,
		"MUXCORE_TLS_CA":               filepath.Join(exportDir, "ca.crt"),
		"MUXCORE_TLS_DIR":              idDir,
		"MUXCORE_TLS_CERT":             "",
		"MUXCORE_TLS_KEY":              "",
		"MUXCORE_TLS_SERVER_NAME":      "",
		"MUXCORE_ENROLL_DNS_NAMES":     "media-ui",
	} {
		t.Setenv(k, v)
	}

	p, err := ensureMeshIdentity(context.Background(), meshid.Ensure, os.Getenv)
	if err != nil {
		t.Fatalf("ensureMeshIdentity: %v", err)
	}
	if !p.Enrolled || p.Cert != filepath.Join(idDir, "module.crt") {
		t.Fatalf("paths = %+v, want enrolled identity in %s", p, idDir)
	}
	if got := os.Getenv("MUXCORE_TLS_CERT"); got != p.Cert {
		t.Fatalf("MUXCORE_TLS_CERT = %q, want %q", got, p.Cert)
	}
	if st, err := os.Stat(p.Key); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key %s: mode %v err %v, want 0600", p.Key, st.Mode().Perm(), err)
	}

	peerAddr, seenCN := startTLSPeer(t, ca)
	conn, err := dialMeshGRPC(peerAddr)
	if err != nil {
		t.Fatalf("dialMeshGRPC: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health check over mesh TLS: %v", err)
	}
	if cn := <-seenCN; cn != "media-ui" {
		t.Fatalf("peer saw client CN %q, want media-ui", cn)
	}

	// A restart reuses the stored identity without contacting core again.
	t.Setenv("MUXCORE_TLS_CERT", "")
	t.Setenv("MUXCORE_TLS_KEY", "")
	t.Setenv("MUXCORE_GRPC_ADDR", "127.0.0.1:1")
	p2, err := ensureMeshIdentity(context.Background(), meshid.Ensure, os.Getenv)
	if err != nil || p2.Enrolled || p2.Cert != p.Cert {
		t.Fatalf("second start: paths %+v err %v, want stored identity", p2, err)
	}
}

func TestEnsureMeshIdentity_HouseholdRefusesInsecure(t *testing.T) {
	t.Setenv("MUXCORE_PROFILE", "household")
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	_, err := ensureMeshIdentity(context.Background(), meshid.Ensure, os.Getenv)
	if !errors.Is(err, meshid.ErrInsecureInHousehold) {
		t.Fatalf("err = %v, want ErrInsecureInHousehold", err)
	}
}

// ---- fakes ----

type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	pool    *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test core CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs pub for cn with loopback SANs (client + server auth).
func (ca *testCA) issue(t *testing.T, cn string, pub any) []byte {
	t.Helper()
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", cn},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func (ca *testCA) serverCert(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(ca.issue(t, cn, &key.PublicKey),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

type fakeCore struct {
	modulev1.UnimplementedModuleRegistrationServer
	t     *testing.T
	ca    *testCA
	token string
}

func (f *fakeCore) BootstrapRegister(_ context.Context, req *modulev1.BootstrapRegisterRequest) (*modulev1.BootstrapRegisterResponse, error) {
	if req.GetToken() != f.token {
		return &modulev1.BootstrapRegisterResponse{Error: "bad token"}, nil
	}
	block, _ := pem.Decode([]byte(req.GetCsrPem()))
	if block == nil {
		return &modulev1.BootstrapRegisterResponse{Error: "no CSR"}, nil
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return &modulev1.BootstrapRegisterResponse{Error: "bad CSR"}, nil
	}
	return &modulev1.BootstrapRegisterResponse{
		Accepted:   true,
		SignedCert: string(f.ca.issue(f.t, req.GetModuleId(), csr.PublicKey)),
	}, nil
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return lis
}

func startFakeCore(t *testing.T, ca *testCA, token string) string {
	t.Helper()
	lis := listenLoopback(t)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{ca.serverCert(t, "muxcored")},
		MinVersion:   tls.VersionTLS12,
	})))
	modulev1.RegisterModuleRegistrationServer(srv, &fakeCore{t: t, ca: ca, token: token})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// startTLSPeer is a module gRPC server that requires a client certificate
// from the core CA and reports the client CN it saw.
func startTLSPeer(t *testing.T, ca *testCA) (string, <-chan string) {
	t.Helper()
	seen := make(chan string, 1)
	lis := listenLoopback(t)
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{ca.serverCert(t, "media-movies")},
			ClientCAs:    ca.pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			if p, ok := peer.FromContext(ctx); ok {
				if ti, ok := p.AuthInfo.(credentials.TLSInfo); ok && len(ti.State.PeerCertificates) > 0 {
					select {
					case seen <- ti.State.PeerCertificates[0].Subject.CommonName:
					default:
					}
				}
			}
			return h(ctx, req)
		}),
	)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	_, port, _ := net.SplitHostPort(lis.Addr().String())
	return net.JoinHostPort("localhost", port), seen
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
