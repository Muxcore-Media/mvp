package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/store"
)

// Test fixtures for the checked userdata-local transport (ADR-0033): a core-like
// CA issuing module certificates the way enrollment does (CN = module ID,
// client+server EKU, service-name SAN), and TLS servers on real sockets.

// devUserdataProvider is the production client in explicit insecure dev
// (plaintext http:// origin, no module identity).
func devUserdataProvider(t *testing.T, origin string, timeout time.Duration) userdataProviderClient {
	t.Helper()
	c, err := httpclient.New(httpclient.Config{Origin: origin, ModuleID: "media-ui", Profile: "dev", Insecure: true, Timeout: timeout})
	if err != nil {
		t.Fatalf("dev provider client %q: %v", origin, err)
	}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

type testPKI struct {
	dir    string
	key    *ecdsa.PrivateKey
	cert   *x509.Certificate
	caFile string
	pool   *x509.CertPool
	serial int64
	mu     sync.Mutex
}

func newTestPKI(t *testing.T, name string) *testPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.crt")
	writePEM(t, caFile, "CERTIFICATE", der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testPKI{dir: dir, key: key, cert: cert, caFile: caFile, pool: pool, serial: 1}
}

type leafOpts struct {
	cn       string
	dns      []string
	ips      []net.IP
	eku      []x509.ExtKeyUsage // default: client + server (core enrollment)
	notAfter time.Time          // default: +1h
}

type testIdentity struct {
	certFile, keyFile, caFile string
	pair                      tls.Certificate
}

// enrolled mirrors core enrollment: CN and a DNS SAN equal to the module ID
// plus the shared loopback SANs every enrolled certificate carries.
func (p *testPKI) enrolled(t *testing.T, moduleID string) testIdentity {
	return p.issue(t, leafOpts{cn: moduleID, dns: []string{moduleID, "localhost"}, ips: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}})
}

func (p *testPKI) issue(t *testing.T, o leafOpts) testIdentity {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.serial++
	serial := p.serial
	p.mu.Unlock()
	eku := o.eku
	if eku == nil {
		eku = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
	}
	notAfter := o.notAfter
	if notAfter.IsZero() {
		notAfter = time.Now().Add(time.Hour)
	}
	notBefore := time.Now().Add(-time.Hour)
	if notAfter.Before(notBefore) {
		notBefore = notAfter.Add(-time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: o.cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  eku,
		DNSNames:     o.dns,
		IPAddresses:  o.ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.cert, &key.PublicKey, p.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(p.dir, "id-"+big.NewInt(serial).String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := testIdentity{certFile: filepath.Join(dir, "module.crt"), keyFile: filepath.Join(dir, "module.key"), caFile: p.caFile}
	writePEM(t, id.certFile, "CERTIFICATE", der)
	writePEM(t, id.keyFile, "PRIVATE KEY", keyDER)
	id.pair, err = tls.LoadX509KeyPair(id.certFile, id.keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// householdProvider builds the BFF's production client the way main does
// (newUserdataProviderClient → httpclient.FromEnv) in the household profile
// with moduleID's mounted identity id.
func householdProvider(t *testing.T, origin, moduleID string, id testIdentity) userdataProviderClient {
	t.Helper()
	for k, v := range map[string]string{
		"MUXCORE_PROFILE": "household", "MUXCORE_INSECURE_DISABLE_TLS": "", "MUXCORE_DEV_TLS_SKIP": "",
		"MUXCORE_MODULE_ID": "", "MUXCORE_CA_EXPORT_DIR": "",
		"MUXCORE_TLS_CERT": id.certFile, "MUXCORE_TLS_KEY": id.keyFile, "MUXCORE_TLS_CA": id.caFile,
	} {
		t.Setenv(k, v)
	}
	c, err := newUserdataProviderClient(origin, moduleID)
	if err != nil || c == nil {
		t.Fatalf("household provider client %q as %s: %v", origin, moduleID, err)
	}
	if hc, ok := c.(*httpclient.Client); ok {
		t.Cleanup(hc.CloseIdleConnections)
	}
	return c
}

// loadBlob is a provider-allowed blob load that must not return an
// application error.
func loadBlob(t *testing.T, u *serverUserdata, scope store.Scope, bearer string) store.Blob {
	t.Helper()
	blob, err := u.load(t.Context(), userdataRequest{scope: scope, bearer: bearer, provider: u.provider != nil})
	if err != nil {
		t.Fatalf("load %+v: %v", scope, err)
	}
	return blob
}

// observedRequest is what a fake TLS provider's handler saw.
type observedRequest struct {
	method, path, rawQuery, peerCN string
	header                         http.Header
}

// fakeTLSProvider is an HTTPS server on a real socket that requires and
// verifies a client certificate against clientCAs. Its handler runs only after
// a completed handshake, so hits() == 0 proves no request (and no bearer)
// reached it.
type fakeTLSProvider struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []observedRequest
	resp http.HandlerFunc
}

func newFakeTLSProvider(t *testing.T, serverID testIdentity, clientCAs *x509.CertPool) *fakeTLSProvider {
	t.Helper()
	p := &fakeTLSProvider{}
	p.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o := observedRequest{method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery, header: r.Header.Clone()}
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			o.peerCN = r.TLS.VerifiedChains[0][0].Subject.CommonName
		}
		p.mu.Lock()
		p.reqs = append(p.reqs, o)
		resp := p.resp
		p.mu.Unlock()
		if resp == nil {
			http.Error(w, "no fake response", http.StatusInternalServerError)
			return
		}
		resp(w, r)
	}))
	p.srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverID.pair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	p.srv.StartTLS()
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeTLSProvider) origin() string { return p.srv.URL }

func (p *fakeTLSProvider) set(status int, body string) {
	p.setFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func (p *fakeTLSProvider) setFunc(f http.HandlerFunc) {
	p.mu.Lock()
	p.resp = f
	p.mu.Unlock()
}

func (p *fakeTLSProvider) requests() []observedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]observedRequest(nil), p.reqs...)
}

func (p *fakeTLSProvider) hits() int { return len(p.requests()) }
