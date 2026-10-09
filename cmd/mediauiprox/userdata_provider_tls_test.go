package main

import (
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/store"
)

// ADR-0033 S9b: the BFF's policy and blob calls use the checked provider client
// over real TLS sockets. Every forged/misidentified provider below is
// rejected during the handshake, before any request — and so before the bearer —
// is sent.

const forgedUnrestricted = `{"user_id":"kid","tenant_id":"","state":"configured","revision":9,"policy":{"version":1,"mode":"unrestricted","rules":null},"updated_at":"2026-10-09T00:00:00Z"}`

// A genuine provider identity: the BFF presents media-ui, reads the policy with
// exactly one bearer and the session target, and blob calls use the canonical
// /api/userdata route without a query string.
func TestUserdataProviderTLSGenuineProvider(t *testing.T) {
	pki := newTestPKI(t, "muxcore-ca")
	prov := newFakeTLSProvider(t, pki.enrolled(t, "userdata-local"), pki.pool)
	bff := pki.enrolled(t, "media-ui")
	client := householdProvider(t, prov.origin(), "media-ui", bff)

	prov.setFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case parentalPolicyPath:
			_, _ = w.Write([]byte(configuredDoc("kid", "", 4, restrictedPolicyJSON)))
		case "/api/userdata":
			_, _ = w.Write([]byte(`{"prefs":{"theme":"dark"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	g := newParentalGateWith(client, newFakeClock().Now)
	pol, perr := g.policy(t.Context(), parentalPrincipal{userID: "kid", bearer: "bearer-kid"})
	if perr != nil || !pol.configured || pol.revision != 4 || pol.unrestricted() {
		t.Fatalf("policy over TLS: %+v %v", pol, perr)
	}

	u := newServerUserdata(t.TempDir(), client)
	scope := store.Scope{UserID: "kid"}
	if blob := u.load(t.Context(), scope, "bearer-kid"); string(blob.Prefs) != `{"theme":"dark"}` {
		t.Fatalf("blob GET over TLS: %+v", blob)
	}
	if _, err := u.save(t.Context(), scope, store.Blob{Prefs: []byte(`{"theme":"light"}`)}, "bearer-kid"); err != nil {
		t.Fatal(err)
	}
	if u.degraded.Load() {
		t.Fatal("provider success recorded as degraded")
	}
	reqs := prov.requests()
	if len(reqs) != 3 {
		t.Fatalf("provider saw %d requests", len(reqs))
	}
	want := []struct{ method, path string }{{"GET", parentalPolicyPath}, {"GET", "/api/userdata"}, {"PUT", "/api/userdata"}}
	for i, r := range reqs {
		if r.method != want[i].method || r.path != want[i].path || r.rawQuery != "" {
			t.Errorf("request %d: %s %s?%s", i, r.method, r.path, r.rawQuery)
		}
		if r.peerCN != "media-ui" {
			t.Errorf("request %d: provider verified client CN %q, want media-ui", i, r.peerCN)
		}
		if a := r.header.Values("Authorization"); len(a) != 1 || a[0] != "Bearer bearer-kid" {
			t.Errorf("request %d: Authorization %q", i, a)
		}
		if v := r.header.Values(muxcoreUserIDHeader); len(v) != 1 || v[0] != "kid" {
			t.Errorf("request %d: target %q", i, v)
		}
	}
}

// Forged providers: each serves an unrestricted document (and a blob) but must
// be rejected before any request is sent, so the gate fails closed with 503
// and the bearer never leaves the BFF. The blob path falls back to the local
// store and records the outage.
func TestUserdataProviderTLSRejectsForgedProviders(t *testing.T) {
	pki := newTestPKI(t, "muxcore-ca")
	other := newTestPKI(t, "attacker-ca")
	bff := pki.enrolled(t, "media-ui")
	loopback := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	cases := []struct {
		name   string
		server testIdentity
	}{
		// Another enrolled module: same CA, shared loopback SANs (what core adds to
		// every certificate), its own CN.
		{"same-CA impostor with loopback SAN", pki.enrolled(t, "metadata-tmdb")},
		// Wrong CN even though it carries the userdata-local service SAN.
		{"wrong CN with userdata-local SAN", pki.issue(t, leafOpts{cn: "jellyfin", dns: []string{"userdata-local", "localhost"}, ips: loopback})},
		// A spoofed provider presenting admin-ui's identity.
		{"admin-ui CN spoofing the provider", pki.issue(t, leafOpts{cn: "admin-ui", dns: []string{"userdata-local", "admin-ui"}, ips: loopback})},
		// Right CN and SAN, wrong CA.
		{"wrong CA", other.enrolled(t, "userdata-local")},
		// Right CN, CA and loopback SAN but without the service SAN.
		{"missing service SAN", pki.issue(t, leafOpts{cn: "userdata-local", dns: []string{"localhost"}, ips: loopback})},
		{"expired provider certificate", pki.issue(t, leafOpts{cn: "userdata-local", dns: []string{"userdata-local"}, notAfter: time.Now().Add(-time.Minute)})},
		{"client-only EKU provider certificate", pki.issue(t, leafOpts{cn: "userdata-local", dns: []string{"userdata-local"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := newFakeTLSProvider(t, tc.server, pki.pool)
			prov.setFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == parentalPolicyPath {
					_, _ = w.Write([]byte(forgedUnrestricted))
					return
				}
				_, _ = w.Write([]byte(`{"prefs":{"forged":true}}`))
			})
			client := householdProvider(t, prov.origin(), "media-ui", bff)
			assertProviderRejected(t, client, prov.hits)
		})
	}
	t.Run("plaintext server behind an https origin", func(t *testing.T) {
		var hits atomic.Int64
		plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			_, _ = w.Write([]byte(forgedUnrestricted))
		}))
		t.Cleanup(plain.Close)
		client := householdProvider(t, strings.Replace(plain.URL, "http://", "https://", 1), "media-ui", bff)
		assertProviderRejected(t, client, func() int { return int(hits.Load()) })
	})
}

func assertProviderRejected(t *testing.T, client userdataProviderClient, hits func() int) {
	t.Helper()
	g := newParentalGateWith(client, newFakeClock().Now)
	_, perr := g.policy(t.Context(), parentalPrincipal{userID: "kid", bearer: "bearer-kid"})
	if perr != errParentalUnavail {
		t.Fatalf("forged provider: perr=%v, want parental.policy_unavailable", perr)
	}
	if len(g.cache) != 0 {
		t.Fatal("transport failure was cached")
	}
	_, err := client.Do(t.Context(), httpclient.GetPolicy, http.Header{"Authorization": {"Bearer bearer-kid"}}, nil)
	var ue *httpclient.UnavailableError
	if !errors.As(err, &ue) || ue.Reason != httpclient.ReasonTransport {
		t.Fatalf("client error %v, want transport unavailability", err)
	}
	u := newServerUserdata(t.TempDir(), client)
	scope := store.Scope{UserID: "kid"}
	if _, err := u.store.Put(scope, store.Blob{Prefs: []byte(`{"local":true}`)}); err != nil {
		t.Fatal(err)
	}
	if blob := u.load(t.Context(), scope, "bearer-kid"); string(blob.Prefs) != `{"local":true}` {
		t.Fatalf("blob from forged provider was accepted: %+v", blob)
	}
	if !u.degraded.Load() {
		t.Fatal("blob fallback was counted as provider success")
	}
	if n := hits(); n != 0 {
		t.Fatalf("forged provider handler ran %d times: the bearer was sent", n)
	}
}

// Secure configuration refuses plaintext, partial identity and a different
// module's certificate before any dial (no fallback to http://).
func TestUserdataProviderSecureConfiguration(t *testing.T) {
	pki := newTestPKI(t, "muxcore-ca")
	bff := pki.enrolled(t, "media-ui")
	set := func(profile, cert, key, ca string) {
		t.Setenv("MUXCORE_PROFILE", profile)
		t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
		t.Setenv("MUXCORE_DEV_TLS_SKIP", "")
		t.Setenv("MUXCORE_MODULE_ID", "")
		t.Setenv("MUXCORE_TLS_CERT", cert)
		t.Setenv("MUXCORE_TLS_KEY", key)
		t.Setenv("MUXCORE_TLS_CA", ca)
		t.Setenv("MUXCORE_TLS_DIR", t.TempDir()) // empty: no implicit identity
		t.Setenv("MUXCORE_CA_EXPORT_DIR", "")
	}
	set("household", bff.certFile, bff.keyFile, bff.caFile)
	if c, err := newUserdataProviderClient("https://userdata-local:9672/", "media-ui"); err != nil || c == nil {
		t.Fatalf("household https origin: %v", err)
	}
	for _, bad := range []string{"http://userdata-local:9672", "https://userdata-local:9672/api", "https://u:p@userdata-local:9672", "https://0.0.0.0:9672"} {
		if c, err := newUserdataProviderClient(bad, "media-ui"); err == nil || c != nil {
			t.Errorf("household accepted %q", bad)
		}
	}
	// The certificate must be the BFF's own.
	if c, err := newUserdataProviderClient("https://userdata-local:9672", "admin-ui"); err == nil || c != nil {
		t.Error("client accepted media-ui's certificate as admin-ui")
	}
	set("household", bff.certFile, "", bff.caFile)
	if _, err := newUserdataProviderClient("https://userdata-local:9672", "media-ui"); err == nil {
		t.Error("partial identity accepted")
	}
	set("household", bff.certFile, bff.keyFile, "")
	if _, err := newUserdataProviderClient("https://userdata-local:9672", "media-ui"); err == nil {
		t.Error("missing CA accepted (system roots must never be used)")
	}
	// The household profile rejects the insecure flag outright.
	set("household", bff.certFile, bff.keyFile, bff.caFile)
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	if _, err := newUserdataProviderClient("http://userdata-local:9672", "media-ui"); err == nil {
		t.Error("household accepted the insecure flag")
	}
	// Explicit insecure dev keeps plaintext, and only plaintext.
	set("dev", "", "", "")
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	if c, err := newUserdataProviderClient("http://userdata-local:9672", "media-ui"); err != nil || c == nil {
		t.Fatalf("dev http origin: %v", err)
	}
	if _, err := newUserdataProviderClient("https://userdata-local:9672", "media-ui"); err == nil {
		t.Error("insecure dev accepted an https origin")
	}
	if p := mustUserdataProvider("http://userdata-local:9672/x", "media-ui"); p != nil {
		t.Error("mustUserdataProvider returned a client for an invalid origin")
	}
}

// Policy status mapping over TLS (ADR-0031 §3 + ADR-0033 §2): provider 401
// evicts and returns 401; module admission denial (userdata.module_forbidden,
// with and without its body) and application policy.forbidden are 503
// unavailability and never revoke the session; errors are never cached; a
// redirect is refused without contacting its target.
func TestUserdataProviderTLSStatusMapping(t *testing.T) {
	pki := newTestPKI(t, "muxcore-ca")
	prov := newFakeTLSProvider(t, pki.enrolled(t, "userdata-local"), pki.pool)
	h := newParentalHarness(t)
	h.s.parental.provider = householdProvider(t, prov.origin(), "media-ui", pki.enrolled(t, "media-ui"))
	kid := h.session("kid", "", "bearer-kid")
	route := "GET /stream/trickplay"
	okDoc := func() {
		prov.setFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(configuredDoc(r.Header.Get(muxcoreUserIDHeader), "", 1, unrestrictedPolicyJSON)))
		})
	}

	okDoc()
	if res := serve(h.gated, routeRequest(route, kid)); res.status != http.StatusOK {
		t.Fatalf("prime over TLS: %d %s", res.status, res.body)
	}

	t.Run("401 evicts", func(t *testing.T) {
		h.clock.Advance(parentalPolicyTTL)
		prov.set(http.StatusUnauthorized, `{"code":"policy.unauthenticated"}`)
		assertParentalError(t, route, serve(h.gated, routeRequest(route, kid)), http.StatusUnauthorized, parentalCodeSessionInvalid)
		h.s.parental.mu.Lock()
		n := len(h.s.parental.cache)
		h.s.parental.mu.Unlock()
		if n != 0 {
			t.Fatalf("401 left %d cache entries", n)
		}
	})

	denials := map[string]http.HandlerFunc{
		"module_forbidden body": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"userdata.module_forbidden"}`))
		},
		"module_forbidden header only": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-MuxCore-Error-Code", "userdata.module_forbidden")
			w.WriteHeader(http.StatusForbidden)
		},
		"application policy.forbidden": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"policy.forbidden"}`))
		},
	}
	for name, deny := range denials {
		t.Run(name+" is 503 and keeps the session", func(t *testing.T) {
			before := len(prov.requests())
			prov.setFunc(deny)
			for range 2 {
				assertParentalError(t, route, serve(h.gated, routeRequest(route, kid)), http.StatusServiceUnavailable, parentalCodeUnavailable)
			}
			if n := len(prov.requests()) - before; n != 2 {
				t.Fatalf("provider calls=%d want 2 (errors must not be cached)", n)
			}
			if _, ok := h.s.sessions.get(kid); !ok {
				t.Fatal("module/application denial revoked the BFF session")
			}
			okDoc()
			if res := serve(h.gated, routeRequest(route, kid)); res.status != http.StatusOK {
				t.Fatalf("same session after denial: %d %s", res.status, res.body)
			}
			h.clock.Advance(parentalPolicyTTL)
		})
	}

	t.Run("module_forbidden is typed unavailability in the client", func(t *testing.T) {
		prov.setFunc(denials["module_forbidden body"])
		_, err := h.s.parental.provider.Do(t.Context(), httpclient.GetPolicy, nil, nil)
		var ue *httpclient.UnavailableError
		if !errors.Is(err, httpclient.ErrUnavailable) || !errors.As(err, &ue) || ue.Reason != httpclient.ReasonModuleForbidden {
			t.Fatalf("err=%v", err)
		}
		prov.setFunc(denials["application policy.forbidden"])
		resp, err := h.s.parental.provider.Do(t.Context(), httpclient.GetPolicy, nil, nil)
		if err != nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("application denial: %v", err)
		}
		_ = resp.Body.Close()
	})

	t.Run("redirect refused", func(t *testing.T) {
		target := newFakeTLSProvider(t, pki.enrolled(t, "userdata-local"), pki.pool)
		target.setFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(forgedUnrestricted)) })
		for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			prov.setFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.origin()+parentalPolicyPath, code)
			})
			h.clock.Advance(parentalPolicyTTL)
			assertParentalError(t, route, serve(h.gated, routeRequest(route, kid)), http.StatusServiceUnavailable, parentalCodeUnavailable)
			// Same-origin redirects are refused too.
			prov.setFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, parentalPolicyPath+"x", code)
			})
			assertParentalError(t, route, serve(h.gated, routeRequest(route, kid)), http.StatusServiceUnavailable, parentalCodeUnavailable)
		}
		if n := target.hits(); n != 0 {
			t.Fatalf("redirect target reached %d times", n)
		}
		for _, r := range prov.requests() {
			if r.path != parentalPolicyPath {
				t.Fatalf("followed a redirect to %s", r.path)
			}
		}
	})

	t.Run("transport failure after cache expiry fails closed", func(t *testing.T) {
		okDoc()
		h.clock.Advance(parentalPolicyTTL)
		if res := serve(h.gated, routeRequest(route, kid)); res.status != http.StatusOK {
			t.Fatalf("prime: %d %s", res.status, res.body)
		}
		prov.srv.Close()
		// Within the cache bound the validated policy is still used...
		if res := serve(h.gated, routeRequest(route, kid)); res.status != http.StatusOK {
			t.Fatalf("cached within TTL: %d %s", res.status, res.body)
		}
		// ...after it, the outage is 503, never unrestricted.
		h.clock.Advance(parentalPolicyTTL)
		assertParentalError(t, route, serve(h.gated, routeRequest(route, kid)), http.StatusServiceUnavailable, parentalCodeUnavailable)
	})
}
