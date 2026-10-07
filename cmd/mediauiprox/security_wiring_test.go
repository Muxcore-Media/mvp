package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/module/netguard"
)

func TestPlaybackSourceConfinement(t *testing.T) {
	s := &server{moviesHTTP: mustURL("http://movies:9430"), tvHTTP: mustURL("http://tv:9450")}
	for _, src := range []string{"/stream/movies/m1", "/stream/tv/e1", "/stream/tv/bb/1/1"} {
		if got := s.playbackSourceURL(src); got == "" {
			t.Errorf("legitimate source refused: %q", src)
		}
	}
	for _, src := range []string{
		"http://169.254.169.254/latest/meta-data", "https://media.example/video", "//media.example/video", "/etc/passwd",
		"/stream/movies/../admin", "/stream/movies/%2e%2e/admin", "/stream/tv/%252e%252e/admin",
		"/stream/movies/m1%2fother", "/stream/movies/%5c..", "/stream/tv/", "/stream/tv//e1",
		"/stream/movies/m1?src=/etc/passwd", "/stream/movies/m1#fragment", "/stream/movies/m1%00",
	} {
		t.Run(src, func(t *testing.T) {
			if got := s.playbackSourceURL(src); got != "" {
				t.Fatalf("unsafe source became %q", got)
			}
			// With no transcoder, unsafe sources must not become redirects either.
			for _, handler := range []http.HandlerFunc{s.handleTranscodeStream, s.handleHLSIndex, s.handleTrickplaySprite} {
				w := httptest.NewRecorder()
				handler(w, httptest.NewRequest(http.MethodGet, "/stream/test?src="+url.QueryEscape(src), nil))
				if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
					t.Fatalf("unsafe source: status=%d location=%q", w.Code, w.Header().Get("Location"))
				}
			}
		})
	}
}

func TestPlaybackDirectFallbackRemainsRelative(t *testing.T) {
	s := &server{}
	for _, handler := range []http.HandlerFunc{s.handleTranscodeStream, s.handleHLSIndex} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodGet, "/stream/test?src=%2Fstream%2Ftv%2Fbb%2F1%2F1", nil))
		if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "/stream/tv/bb/1/1" {
			t.Fatalf("fallback: status=%d location=%q", w.Code, w.Header().Get("Location"))
		}
	}
}

func TestOperatorSettingsRejectManager(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	token, err := s.sessions.CreateWithRoles("manager", "manager", "", []string{"manager"})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		method string
		handle http.HandlerFunc
	}{
		"lists": {"GET", s.handleListSources}, "create-list": {"POST", s.handleCreateListSource},
		"update-list": {"PATCH", s.handleUpdateListSource}, "delete-list": {"DELETE", s.handleDeleteListSource},
		"sync-lists": {"POST", s.handleSyncListSources}, "sync-list": {"POST", s.handleSyncListSource},
		"test-list": {"POST", s.handleTestListSource}, "list-history": {"GET", s.handleListSyncHistory},
		"list-items": {"GET", s.handleListSyncItems}, "create-indexer": {"POST", s.handleCreateIndexer},
		"update-indexer": {"PATCH", s.handleUpdateIndexer}, "delete-indexer": {"DELETE", s.handleDeleteIndexer},
		"migrate": {"POST", s.handleMigrate}, "create-root": {"POST", s.handleCreateRoot},
		"patch-root": {"PATCH", s.handlePatchRoot}, "post-root": {"POST", s.handlePatchRoot},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/test", strings.NewReader(`{}`))
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
			w := httptest.NewRecorder()
			tc.handle(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("manager accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestArrClientIntegrationGuard(t *testing.T) {
	client := &arrClient{}
	for _, raw := range []string{"http://169.254.169.254/latest/meta-data", "http://[::ffff:169.254.169.254]/", "http://metadata.google.internal/", "file:///etc/passwd"} {
		if _, err := client.get(context.Background(), raw, "test-key"); !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("%s: want blocked, got %v", raw, err)
		}
	}
	received := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Error("missing integration credential")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, target.URL, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer origin.Close()
	if body, err := client.get(context.Background(), origin.URL, "test-key"); err != nil || string(body) != `[]` {
		t.Fatalf("legitimate loopback integration: %s %v", body, err)
	}
	if _, err := client.get(context.Background(), origin.URL+"/redirect", "test-key"); err == nil || received != 0 {
		t.Fatalf("redirect leaked request: received=%d err=%v", received, err)
	}
}

func meshHTTPTestIdentity(t *testing.T, name string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	dir := t.TempDir()
	for name, data := range map[string][]byte{"cert.pem": certPEM, "key.pem": keyPEM} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MUXCORE_TLS_CERT", filepath.Join(dir, "cert.pem"))
	t.Setenv("MUXCORE_TLS_KEY", filepath.Join(dir, "key.pem"))
	t.Setenv("MUXCORE_TLS_CA", filepath.Join(dir, "cert.pem"))
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return cert, pool
}

func TestTranscoderMeshTransportEveryRoute(t *testing.T) {
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", filepath.Join(t.TempDir(), "absent.json"))
	cert, pool := meshHTTPTestIdentity(t, "media-ui")
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.VerifiedChains) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "media-ui" {
			t.Error("missing authenticated BFF identity")
		}
		for key := range r.Header {
			if isClientIdentityHeader(key) {
				t.Errorf("browser identity forwarded: %s", key)
			}
		}
		_, _ = w.Write([]byte("ok"))
	}))
	upstream.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
	upstream.StartTLS()
	defer upstream.Close()
	transport, err := newTranscoderTransport(mustURL(upstream.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer transport.(*http.Transport).CloseIdleConnections()
	s := &server{transcoderHTTP: mustURL(upstream.URL), transcoderTransport: transport, moviesHTTP: mustURL("http://movies:9430")}
	for _, handler := range []http.HandlerFunc{s.handleTranscodeStream, s.handleHLSIndex, s.handleHLSAsset, s.handleTrickplaySprite} {
		req := httptest.NewRequest("GET", "/stream/test?src=%2Fstream%2Fmovies%2Fm1", nil)
		req.SetPathValue("key", "0123456789abcdef0123456789abcdef")
		req.SetPathValue("file", "seg_00000.ts")
		for _, key := range []string{"Cookie", "Authorization", "X-Caller-Id", "X-Tenant-ID", "X-MuxCore-Session", "X-User-ID"} {
			req.Header.Set(key, "browser-controlled")
		}
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("mTLS proxy: %d %s", w.Code, w.Body.String())
		}
	}
	if !s.transcoderModuleLive(context.Background()) {
		t.Fatal("health check did not use mesh transport")
	}
	// A different identity trust root must not authenticate this server.
	meshHTTPTestIdentity(t, "untrusted")
	wrong, err := newTranscoderTransport(mustURL(upstream.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.(*http.Transport).CloseIdleConnections()
	s.transcoderTransport = wrong
	if s.transcoderModuleLive(context.Background()) {
		t.Fatal("untrusted server certificate accepted")
	}
	// Trust the server but keep the unrelated client certificate: the server
	// must reject a client that was not issued by its mesh trust root.
	wrong.(*http.Transport).TLSClientConfig.RootCAs = pool
	if s.transcoderModuleLive(context.Background()) {
		t.Fatal("untrusted client certificate accepted")
	}
}

func TestTranscoderDevTokenAndProfile(t *testing.T) {
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	if _, err := newTranscoderTransport(mustURL("http://localhost:9526")); err == nil {
		t.Fatal("household accepted plaintext")
	}
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-dev-token" || r.Header.Get("Cookie") != "" {
			t.Error("expected operator token with no browser cookie")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	transport, err := newTranscoderTransport(mustURL(upstream.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer transport.(*http.Transport).CloseIdleConnections()
	s := &server{transcoderHTTP: mustURL(upstream.URL), transcoderTransport: transport, transcoderToken: "test-dev-token"}
	req := httptest.NewRequest("GET", "/stream/hls/key/index.m3u8", nil)
	req.SetPathValue("key", "0123456789abcdef0123456789abcdef")
	req.SetPathValue("file", "index.m3u8")
	req.Header.Set("Authorization", "Bearer browser-token")
	req.Header.Set("Cookie", "session=browser-token")
	w := httptest.NewRecorder()
	s.handleHLSAsset(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dev proxy: %d", w.Code)
	}
	t.Setenv("TRANSCODER_HTTP_URL", "")
	if got := defaultTranscoderURL(); got != "" {
		t.Fatalf("explicit disable ignored: %q", got)
	}
}
