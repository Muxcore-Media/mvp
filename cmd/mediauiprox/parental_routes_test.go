package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/store"
)

// ADR-0031 required negative tests wired in T-M4-01 S5a (C-PLAY, C-DENY).

// Negative test 3 (S5a form) and 10: a restricted principal is denied every
// C-PLAY route (classification is unavailable until S5b) and every C-DENY
// route; no request reaches a module.
func TestParentalRestrictedDeniesPlayAndDenyRoutes(t *testing.T) {
	h := newParentalHarness(t)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 3, restrictedPolicyJSON) })
	kid := h.session("kid", "", "bearer-kid")
	for _, p := range patternsOfClass(classPlay) {
		assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, parentalCodeBlocked)
	}
	for _, p := range patternsOfClass(classDeny) {
		assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, parentalCodeRestrictedRoute)
	}
	if n := h.upHits.Load(); n != 0 {
		t.Fatalf("restricted requests reached modules %d times", n)
	}
	// One provider call: the validated policy is cached for the TTL.
	if n := h.provider.count(); n != 1 {
		t.Fatalf("provider calls=%d want 1 (cached)", n)
	}
}

// Negative test 9 (regression) and 10: an unrestricted principal gets exactly
// the ungated response on every C-PLAY and C-DENY route.
func TestParentalUnrestrictedMatchesUngatedBaseline(t *testing.T) {
	h := newParentalHarness(t)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
	adult := h.session("adult", "", "bearer-adult")
	for _, p := range patternsOfClass(classPlay, classDeny) {
		got := serve(h.gated, routeRequest(p, h.tokFor(p, adult)))
		want := serve(h.baseline, routeRequest(p, h.tokFor(p, adult)))
		if got.panic != want.panic || got.status != want.status || got.body != want.body {
			t.Errorf("%s: gated=%d %q panic=%q baseline=%d %q panic=%q", p, got.status, got.body, got.panic, want.status, want.body, want.panic)
			continue
		}
		for _, k := range []string{"Content-Type", "Location", "Cache-Control"} {
			if got.header.Get(k) != want.header.Get(k) {
				t.Errorf("%s: header %s gated=%q baseline=%q", p, k, got.header.Get(k), want.header.Get(k))
			}
		}
	}
	// The previously unguarded direct streams still reach the module.
	for _, path := range []string{"/stream/movies/m1", "/stream/tv/e1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: adult})
		res := serve(h.gated, req)
		if res.status != http.StatusOK || !strings.HasPrefix(res.body, "upstream GET /stream/") {
			t.Errorf("%s: %d %q", path, res.status, res.body)
		}
	}
}

// Negative test 7: every provider failure is 503 parental.policy_unavailable
// on every gated class, with no module request and no cached result.
func TestParentalProviderFailuresFailClosed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(p *fakePolicyProvider)
	}{
		{"5xx", func(p *fakePolicyProvider) {
			p.set(http.StatusInternalServerError, `{"code":"policy.storage_unavailable"}`)
		}},
		{"503", func(p *fakePolicyProvider) {
			p.set(http.StatusServiceUnavailable, `{"code":"policy.identity_unavailable"}`)
		}},
		{"403", func(p *fakePolicyProvider) { p.set(http.StatusForbidden, `{"code":"policy.forbidden"}`) }},
		{"404 route missing", func(p *fakePolicyProvider) { p.set(http.StatusNotFound, `404 page not found`) }},
		{"409", func(p *fakePolicyProvider) { p.set(http.StatusConflict, `{}`) }},
		{"413", func(p *fakePolicyProvider) { p.set(http.StatusRequestEntityTooLarge, `{}`) }},
		{"redirect", func(p *fakePolicyProvider) {
			p.setFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/elsewhere", http.StatusFound)
			})
		}},
		{"malformed JSON", func(p *fakePolicyProvider) { p.set(http.StatusOK, `{"user_id":"kid",`) }},
		{"empty body", func(p *fakePolicyProvider) { p.set(http.StatusOK, ``) }},
		{"unknown field", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, strings.Replace(configuredDoc("kid", "", 1, unrestrictedPolicyJSON), `"state"`, `"extra":1,"state"`, 1))
		}},
		{"duplicate field", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, strings.Replace(configuredDoc("kid", "", 1, unrestrictedPolicyJSON), `"state"`, `"user_id":"kid","state"`, 1))
		}},
		{"trailing document", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, configuredDoc("kid", "", 1, unrestrictedPolicyJSON)+"{}")
		}},
		{"unknown policy field", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, configuredDoc("kid", "", 1, `{"version":1,"mode":"unrestricted","rules":null,"pin_hash":"x"}`))
		}},
		{"unsupported rating", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, configuredDoc("kid", "", 1, strings.Replace(restrictedPolicyJSON, `"max_rating":""`, `"max_rating":"15"`, 1)))
		}},
		{"user mismatch", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, configuredDoc("adult", "", 1, unrestrictedPolicyJSON))
		}},
		{"tenant mismatch", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, configuredDoc("kid", "other", 1, unrestrictedPolicyJSON))
		}},
		{"tenant rebound to default", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, configuredDoc("kid", "default", 1, unrestrictedPolicyJSON))
		}},
		{"null user_id", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, strings.Replace(configuredDoc("kid", "", 1, unrestrictedPolicyJSON), `"user_id":"kid"`, `"user_id":null`, 1))
		}},
		{"revision 0 with policy", func(p *fakePolicyProvider) { p.set(http.StatusOK, configuredDoc("kid", "", 0, unrestrictedPolicyJSON)) }},
		{"unconfigured with policy", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, `{"user_id":"kid","tenant_id":"","state":"unconfigured","revision":0,"policy":`+unrestrictedPolicyJSON+`}`)
		}},
		{"unconfigured with revision", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, `{"user_id":"kid","tenant_id":"","state":"unconfigured","revision":2,"policy":null}`)
		}},
		{"configured null policy", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, `{"user_id":"kid","tenant_id":"","state":"configured","revision":2,"policy":null}`)
		}},
		{"fractional revision", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, strings.Replace(configuredDoc("kid", "", 1, unrestrictedPolicyJSON), `"revision":1`, `"revision":1.5`, 1))
		}},
		{"string revision", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, strings.Replace(configuredDoc("kid", "", 1, unrestrictedPolicyJSON), `"revision":1`, `"revision":"1"`, 1))
		}},
		{"unknown state", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, strings.Replace(configuredDoc("kid", "", 1, unrestrictedPolicyJSON), `"configured"`, `"unrestricted"`, 1))
		}},
		{"missing state", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, strings.Replace(configuredDoc("kid", "", 1, unrestrictedPolicyJSON), `"state":"configured",`, ``, 1))
		}},
		{"oversized body", func(p *fakePolicyProvider) {
			p.set(http.StatusOK, configuredDoc("kid", "", 1, unrestrictedPolicyJSON)+strings.Repeat(" ", parentalPolicyMaxBody))
		}},
	}
	gated := patternsOfClass(classPlay, classDeny)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newParentalHarness(t)
			tc.setup(h.provider)
			kid := h.session("kid", "", "bearer-kid")
			for _, p := range gated {
				assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusServiceUnavailable, parentalCodeUnavailable)
			}
			// Never cached: every gated request asked the provider again.
			if n := h.provider.count(); n != len(gated) {
				t.Errorf("provider calls=%d want %d (errors must not be cached)", n, len(gated))
			}
			if n := h.upHits.Load(); n != 0 {
				t.Errorf("modules reached %d times", n)
			}
			// A working provider afterwards is used immediately.
			h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
			res := serve(h.gated, routeRequest("GET /stream/trickplay", kid))
			if res.status == http.StatusServiceUnavailable && strings.Contains(res.body, parentalCodeUnavailable) {
				t.Errorf("failure was cached: %s", res.body)
			}
		})
	}
}

func TestParentalProviderDownAndTimeout(t *testing.T) {
	t.Run("down", func(t *testing.T) {
		h := newParentalHarness(t)
		h.provider.srv.Close()
		kid := h.session("kid", "", "bearer-kid")
		for _, p := range patternsOfClass(classPlay, classDeny) {
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusServiceUnavailable, parentalCodeUnavailable)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		h := newParentalHarness(t)
		h.s.parental.client = newParentalPolicyClient(50 * time.Millisecond)
		h.provider.setFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			_, _ = w.Write([]byte(configuredDoc("kid", "", 1, unrestrictedPolicyJSON)))
		})
		kid := h.session("kid", "", "bearer-kid")
		for _, p := range []string{"GET /api/playback/resolve", "/stream/movies/", "/api/discover/", "/api/search"} {
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusServiceUnavailable, parentalCodeUnavailable)
		}
	})
	t.Run("no provider configured", func(t *testing.T) {
		h := newParentalHarness(t)
		h.s.parental = newParentalGate("")
		kid := h.session("kid", "", "bearer-kid")
		assertParentalError(t, "GET /stream/hls", serve(h.gated, routeRequest("GET /stream/hls", kid)), http.StatusServiceUnavailable, parentalCodeUnavailable)
		h.s.parental = nil
		assertParentalError(t, "GET /api/livetv", serve(h.gated, routeRequest("GET /api/livetv", kid)), http.StatusServiceUnavailable, parentalCodeUnavailable)
		if h.provider.count() != 0 {
			t.Fatal("provider contacted without a configured URL")
		}
	})
}

// Negative test 8: provider 401 → 401 and cache eviction; unconfigured → 403;
// a session without a bearer → 403 without asking the provider.
func TestParentalSessionAndConfigurationStates(t *testing.T) {
	t.Run("provider 401 evicts", func(t *testing.T) {
		h := newParentalHarness(t)
		h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
		kid := h.session("kid", "", "bearer-kid")
		if res := serve(h.gated, routeRequest("GET /stream/trickplay", kid)); res.status != http.StatusOK {
			t.Fatalf("prime: %d %s", res.status, res.body)
		}
		h.clock.Advance(parentalPolicyTTL)
		h.provider.set(http.StatusUnauthorized, `{"code":"policy.unauthenticated"}`)
		for _, p := range patternsOfClass(classPlay, classDeny) {
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusUnauthorized, parentalCodeSessionInvalid)
		}
		h.s.parental.mu.Lock()
		cached := len(h.s.parental.cache)
		h.s.parental.mu.Unlock()
		if cached != 0 {
			t.Fatalf("401 left %d cache entries", cached)
		}
	})
	t.Run("unconfigured", func(t *testing.T) {
		h := newParentalHarness(t)
		h.provider.doc(func(u string) string { return unconfiguredDoc(u, "") })
		kid := h.session("kid", "", "bearer-kid")
		for _, p := range patternsOfClass(classPlay, classDeny) {
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, parentalCodeUnconfigured)
		}
		if n := h.provider.count(); n != 1 {
			t.Fatalf("unconfigured is a validated state and is cached: calls=%d", n)
		}
	})
	t.Run("no bearer", func(t *testing.T) {
		h := newParentalHarness(t)
		h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
		// Quick Connect sessions are created without an auth-local token.
		qc, err := h.s.sessions.CreateWithTenant("kid", "Kid", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range patternsOfClass(classPlay, classDeny) {
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, qc))), http.StatusForbidden, parentalCodeUnverifiable)
		}
		if h.provider.count() != 0 {
			t.Fatal("provider contacted for a session without a bearer")
		}
	})
	t.Run("no session with auth required", func(t *testing.T) {
		h := newParentalHarness(t)
		gated := http.NewServeMux()
		h.s.registerRoutes(gated) // without withAuth: the gate itself refuses
		assertParentalError(t, "GET /api/playback/resolve", serve(gated, routeRequest("GET /api/playback/resolve", "")), http.StatusUnauthorized, parentalCodeSessionInvalid)
		assertParentalError(t, "/api/search", serve(gated, routeRequest("/api/search", "")), http.StatusUnauthorized, parentalCodeSessionInvalid)
	})
	t.Run("dev without auth and without session", func(t *testing.T) {
		h := newParentalHarness(t)
		h.s.requireAuth = false
		gated := http.NewServeMux()
		h.s.registerRoutes(gated)
		raw := http.NewServeMux()
		h.s.registerClassifiedRoutes(raw)
		for _, p := range []string{"GET /stream/trickplay", "GET /stream/hls"} {
			got, want := serve(gated, routeRequest(p, "")), serve(raw, routeRequest(p, ""))
			if got.status != want.status || got.body != want.body {
				t.Errorf("%s: dev passthrough %d %q, baseline %d %q", p, got.status, got.body, want.status, want.body)
			}
		}
		if h.provider.count() != 0 {
			t.Fatal("provider contacted without a session")
		}
		// A session is still enforced when auth is disabled.
		h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, restrictedPolicyJSON) })
		kid := h.session("kid", "", "bearer-kid")
		assertParentalError(t, "GET /stream/trickplay", serve(gated, routeRequest("GET /stream/trickplay", kid)), http.StatusForbidden, parentalCodeBlocked)
	})
}

// Negative test 9: a policy change takes effect within the 30 s TTL.
func TestParentalPolicyChangeWithinTTL(t *testing.T) {
	h := newParentalHarness(t)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
	kid := h.session("kid", "", "bearer-kid")
	p := "GET /stream/trickplay"
	if res := serve(h.gated, routeRequest(p, h.tokFor(p, kid))); res.status != http.StatusOK {
		t.Fatalf("unrestricted: %d %s", res.status, res.body)
	}
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 2, restrictedPolicyJSON) })
	h.clock.Advance(parentalPolicyTTL - time.Second)
	if res := serve(h.gated, routeRequest(p, h.tokFor(p, kid))); res.status != http.StatusOK {
		t.Fatalf("within TTL the cached policy applies: %d %s", res.status, res.body)
	}
	h.clock.Advance(time.Second)
	assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, parentalCodeBlocked)

	h.provider.doc(func(u string) string { return configuredDoc(u, "", 3, unrestrictedPolicyJSON) })
	h.clock.Advance(parentalPolicyTTL)
	if res := serve(h.gated, routeRequest(p, h.tokFor(p, kid))); res.status != http.StatusOK {
		t.Fatalf("lifted restriction after TTL: %d %s", res.status, res.body)
	}
	if n := h.provider.count(); n != 3 {
		t.Fatalf("provider calls=%d want 3", n)
	}
}

// Negative test 2: request fields, client identity/tenant headers and the
// userdata blob never influence a decision, and their absence unlocks nothing.
func TestParentalIgnoresClientInputsAndBlob(t *testing.T) {
	t.Setenv("USERDATA_PREFER_MESH", "0")
	t.Setenv("TENANT_MODE", "1")
	h := newParentalHarness(t)
	h.s.userdata = newServerUserdata(t.TempDir())
	blocking, _ := json.Marshal(map[string]any{"parental": map[string]any{"blocked_tags": "horror", "allow_unrated": false}})
	for _, scope := range []store.Scope{{UserID: "kid", TenantID: "default"}, {UserID: "kid"}, {UserID: "adult", TenantID: "default"}} {
		if _, err := h.s.userdata.store.Put(scope, store.Blob{Prefs: blocking}); err != nil {
			t.Fatal(err)
		}
	}
	spoof := func(req *http.Request) *http.Request {
		q := req.URL.Query()
		q.Set("tags", "horror")
		q.Set("parental_rating", "G")
		q.Set("unrated", "1")
		q.Set("user_id", "adult")
		q.Set("tenant_id", "other")
		req.URL.RawQuery = q.Encode()
		req.Header.Set(muxcoreUserIDHeader, "adult")
		req.Header.Set("X-Tenant-ID", "other")
		req.Header.Set("X-Caller-Id", "adult")
		return req
	}
	h.provider.doc(func(u string) string {
		if u == "adult" {
			return configuredDoc(u, "", 1, unrestrictedPolicyJSON)
		}
		return configuredDoc(u, "", 1, restrictedPolicyJSON)
	})
	kid := h.session("kid", "", "bearer-kid")
	adult := h.session("adult", "", "bearer-adult")
	for _, p := range patternsOfClass(classPlay, classDeny) {
		code := parentalCodeBlocked
		if parentalRouteClasses[p].class == classDeny {
			code = parentalCodeRestrictedRoute
		}
		// Restricted: denied with and without the legacy inputs.
		assertParentalError(t, p, serve(h.gated, spoof(routeRequest(p, h.tokFor(p, kid)))), http.StatusForbidden, code)
		assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, code)
		// Unrestricted: the blocking blob and query inputs change nothing.
		got, want := serve(h.gated, spoof(routeRequest(p, h.tokFor(p, adult)))), serve(h.baseline, spoof(routeRequest(p, h.tokFor(p, adult))))
		if got.status != want.status || got.body != want.body || got.panic != want.panic {
			t.Errorf("%s: unrestricted spoofed request gated=%d %q baseline=%d %q", p, got.status, got.body, want.status, want.body)
		}
	}
	res := serve(h.gated, spoof(routeRequest("GET /api/playback/resolve", adult)))
	if res.status != http.StatusOK || !strings.Contains(res.body, `"stream_url"`) {
		t.Fatalf("blob prefs.parental / tags=horror must not block resolve: %d %s", res.status, res.body)
	}
	// The provider only ever saw the session principal, one bearer, no query.
	for _, pr := range h.provider.requests() {
		user := pr.header.Get(muxcoreUserIDHeader)
		if pr.method != http.MethodGet || pr.path != parentalPolicyPath || pr.rawQuery != "" {
			t.Fatalf("policy request %s %s?%s", pr.method, pr.path, pr.rawQuery)
		}
		if auth := pr.header.Values("Authorization"); len(auth) != 1 || auth[0] != "Bearer bearer-"+user {
			t.Fatalf("policy request for %q carried Authorization %q", user, auth)
		}
		if v := pr.header.Values(muxcoreUserIDHeader); len(v) != 1 || (user != "kid" && user != "adult") {
			t.Fatalf("policy request target %q", v)
		}
		for _, k := range []string{"X-Tenant-Id", "X-Caller-Id", "Cookie"} {
			if pr.header.Get(k) != "" {
				t.Fatalf("policy request forwarded %s", k)
			}
		}
	}
}

// The policy scope is the session's tenant exactly. An empty verified tenant
// is the household scope and is never rebound to TENANT_MODE's "default".
func TestParentalTenantScope(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	h := newParentalHarness(t)
	h.provider.doc(func(u string) string {
		if u == "kid" {
			return configuredDoc(u, "", 1, unrestrictedPolicyJSON)
		}
		return configuredDoc(u, "house-a", 1, unrestrictedPolicyJSON)
	})
	kid := h.session("kid", "", "bearer-kid")
	tenantUser := h.session("member", "house-a", "bearer-member")
	other := h.session("guest", "house-b", "bearer-guest")
	for tok, want := range map[string]int{kid: http.StatusOK, tenantUser: http.StatusOK, other: http.StatusServiceUnavailable} {
		if res := serve(h.gated, routeRequest("GET /stream/trickplay", tok)); res.status != want {
			t.Errorf("status=%d want %d body=%s", res.status, want, res.body)
		}
	}
}

// The policy client is the production one: no redirects followed.
func TestParentalProductionClientDoesNotFollowRedirects(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(configuredDoc("kid", "", 1, unrestrictedPolicyJSON)))
	}))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+parentalPolicyPath, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	g := newParentalGate(redir.URL + "/")
	_, perr := g.policy(t.Context(), parentalPrincipal{userID: "kid", bearer: "b"})
	if perr != errParentalUnavail || hits != 0 {
		t.Fatalf("perr=%v redirect target hits=%d", perr, hits)
	}
	for _, bad := range []string{"", "  ", "ftp://x", "http://", "http://h/?q=1", "http://h/#f", "http://u:p@h"} {
		if parentalPolicyURL(bad) != "" {
			t.Errorf("policy URL accepted for %q", bad)
		}
	}
	if got := parentalPolicyURL("http://userdata-local:9672/"); got != "http://userdata-local:9672/api/parental-policy" {
		t.Errorf("policy URL %q", got)
	}
}

// C-EXEMPT operator rows claim the handler already rejects members. Prove the
// role gate fires for every one of them, so the exemption cannot silently
// widen if a handler loses its gate.
func TestParentalOperatorExemptionsAreRoleGated(t *testing.T) {
	h := newParentalHarness(t)
	member := h.session("member", "", "bearer-member")
	for _, p := range patternsOfClass(classExempt) {
		if !parentalRouteClasses[p].roleGated {
			continue
		}
		res := serve(h.gated, routeRequest(p, member))
		if res.panic != "" || res.status != http.StatusForbidden {
			t.Errorf("%s: member got %d (panic=%q) body=%s", p, res.status, res.panic, res.body)
		}
	}
	if n := h.upHits.Load(); n != 0 {
		t.Errorf("member requests reached modules %d times", n)
	}
	if h.provider.count() != 0 {
		t.Error("exempt routes must not consult the parental policy")
	}
}
