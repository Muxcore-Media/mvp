package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/parental"
)

// The envelope decoder accepts exactly what the provider encodes
// (userdata-local internal/server/parental.go encodes parental.Document).
func TestDecodeParentalDocumentAcceptsProviderEncoding(t *testing.T) {
	kid := parentalPrincipal{userID: "kid", tenantID: ""}
	raw, err := json.Marshal(parental.Unconfigured(parental.Scope{UserID: "kid"}))
	if err != nil {
		t.Fatal(err)
	}
	pol, err := decodeParentalDocument(raw, kid)
	if err != nil || pol.configured {
		t.Fatalf("unconfigured: %+v %v (%s)", pol, err, raw)
	}
	rules := &parental.Rules{KidsMode: true, BlockedTags: []string{"Gore"}, AllowedTags: []string{}}
	doc := parental.Document{
		Scope: parental.Scope{UserID: "kid", TenantID: "house"}, State: "configured", Revision: 7,
		Policy: &parental.Policy{Version: 1, Mode: "restricted", Rules: rules}, UpdatedAt: "2026-10-08T00:00:00Z",
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	pol, err = decodeParentalDocument(raw, parentalPrincipal{userID: "kid", tenantID: "house"})
	if err != nil || !pol.configured || pol.revision != 7 || pol.policy.Mode != "restricted" || pol.unrestricted() {
		t.Fatalf("configured: %+v %v (%s)", pol, err, raw)
	}
	if got := pol.policy.Rules.BlockedTags; len(got) != 1 || got[0] != "gore" {
		t.Fatalf("policy not normalized by parental.DecodePolicy: %v", got)
	}
	if _, err := decodeParentalDocument(raw, parentalPrincipal{userID: "kid"}); err == nil {
		t.Fatal("tenant mismatch accepted")
	}
}

// A restricted principal may play an item the classifier allows; a
// classification lookup failure is 503 classification_unavailable.
func TestParentalPlayAllowsClassifiedItem(t *testing.T) {
	h, fc := hlsHarness(t)
	kid := h.session("kid", "", "bearer-kid")
	for _, p := range []string{"/api/playback/resolve", "/stream/trickplay", "/api/playback/chapters"} {
		if res := hlsGet(h, p+"?src="+url.QueryEscape("/stream/movies/ok"), kid); res.status != http.StatusOK {
			t.Errorf("%s allowed item: %d %s", p, res.status, res.body)
		}
		assertParentalError(t, "GET "+p, hlsGet(h, p+"?src="+url.QueryEscape("/stream/movies/bad"), kid), http.StatusForbidden, parentalCodeBlocked)
	}
	if res := hlsGet(h, "/stream/movies/ok", kid); res.status != http.StatusOK {
		t.Errorf("direct stream allowed item: %d %s", res.status, res.body)
	}
	assertParentalError(t, "/stream/movies/", hlsGet(h, "/stream/movies/bad", kid), http.StatusForbidden, parentalCodeBlocked)
	// debrid sources and unparseable paths name no item: unavailable → denied.
	assertParentalError(t, "GET /api/playback/resolve", hlsGet(h, "/api/playback/resolve?src=debrid%3Aabc", kid), http.StatusForbidden, parentalCodeBlocked)
	assertParentalError(t, "/stream/movies/", hlsGet(h, "/stream/movies/ok/extra", kid), http.StatusForbidden, parentalCodeBlocked)
	fc.mu.Lock()
	fc.err = errors.New("lookup failed")
	fc.mu.Unlock()
	assertParentalError(t, "/stream/movies/", hlsGet(h, "/stream/movies/ok", kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
}

func TestParentalItemFromStreamPath(t *testing.T) {
	cases := map[string]parentalItem{
		"/stream/movies/m1":       {Kind: "movie", ID: "m1"},
		"/stream/tv/e1":           {Kind: "episode", ID: "e1"},
		"/stream/tv/bb/1/1":       {},
		"/stream/movies/m1/":      {},
		"/stream/movies/%2e%2e":   {},
		"/stream/music/t1":        {},
		"debrid:abc":              {},
		"/stream/movies/m1?x=1":   {},
		"http://h/stream/movies/": {},
	}
	for in, want := range cases {
		if got := parentalItemFromStreamPath(in); got != want {
			t.Errorf("%q → %+v want %+v", in, got, want)
		}
	}
}

// A full cache never fails open: the validated policy is still returned and
// simply not cached.
func TestParentalPolicyCacheCapacity(t *testing.T) {
	p := newFakePolicyProvider(t)
	p.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
	clock := newFakeClock()
	g := newParentalGateWith(devUserdataProvider(t, p.srv.URL, time.Second), clock.Now)
	for i := range parentalPolicyCacheMax {
		g.cache[fmt.Sprint(i)] = parentalCacheEntry{expires: clock.Now().Add(time.Minute)}
	}
	pr := parentalPrincipal{userID: "kid", bearer: "b"}
	for range 2 {
		pol, perr := g.policy(t.Context(), pr)
		if perr != nil || !pol.unrestricted() {
			t.Fatalf("policy with full cache: %+v %v", pol, perr)
		}
	}
	if p.count() != 2 || len(g.cache) != parentalPolicyCacheMax {
		t.Fatalf("calls=%d cache=%d", p.count(), len(g.cache))
	}
	// Expired entries are reclaimed.
	clock.Advance(2 * time.Minute)
	if _, perr := g.policy(t.Context(), pr); perr != nil {
		t.Fatal(perr)
	}
	if len(g.cache) != 1 {
		t.Fatalf("cache=%d after reclaim", len(g.cache))
	}
}

// The cache key separates bearers and users: another token for the same user,
// or the same token for another user, is a separate lookup.
func TestParentalPolicyCacheKey(t *testing.T) {
	a := parentalCacheKey(parentalPrincipal{userID: "kid", bearer: "t1"})
	for _, other := range []parentalPrincipal{{userID: "kid", bearer: "t2"}, {userID: "kid2", bearer: "t1"}, {userID: "kid", tenantID: "x", bearer: "t1"}} {
		if parentalCacheKey(other) == a {
			t.Fatalf("cache key collision for %+v", other)
		}
	}
	if len(a) < 64 || a[:64] == "t1" {
		t.Fatal("cache key must hash the bearer")
	}
}
