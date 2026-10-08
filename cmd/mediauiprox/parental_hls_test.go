package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/parental"
)

// ADR-0031 required negative test 4 and the S5a HLS key binding.

// fakeClassifier stands in for the S5b media-module classifier.
type fakeClassifier struct {
	mu    sync.Mutex
	items map[string]parental.Classification // "kind/id"
	err   error
}

func (f *fakeClassifier) Classify(_ context.Context, item parentalItem) (parental.Classification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return parental.Classification{}, f.err
	}
	return f.items[item.Kind+"/"+item.ID], nil // missing → zero value → Unavailable
}

func (f *fakeClassifier) set(key string, c parental.Classification) {
	f.mu.Lock()
	f.items[key] = c
	f.mu.Unlock()
}

// transcoderKey mirrors media-transcoder's deterministic derivation shape: a
// digest of the source and parameters that anyone can compute.
func transcoderKey(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:16])
}

func hlsHarness(t *testing.T) (*parentalHarness, *fakeClassifier) {
	t.Helper()
	h := newParentalHarness(t)
	h.hlsKey = transcoderKey
	fc := &fakeClassifier{items: map[string]parental.Classification{
		"movie/ok":  {State: parental.Rated, Rating: "PG", TagsKnown: true},
		"movie/bad": {State: parental.Rated, Rating: "R", TagsKnown: true},
	}}
	h.s.parental.classifier = fc
	h.provider.doc(func(u string) string {
		if u == "adult" {
			return configuredDoc(u, "", 1, unrestrictedPolicyJSON)
		}
		return configuredDoc(u, "", 1, restrictedPolicyJSON) // kids mode: PG ceiling
	})
	return h, fc
}

func hlsGet(h *parentalHarness, path, tok string) serveResult {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return serve(h.gated, req)
}

// sourceKey is the key the transcoder derives for a BFF src.
func sourceKey(h *parentalHarness, src string) string {
	return transcoderKey(h.s.playbackSourceURL(src))
}

func TestParentalHLSKeyBinding(t *testing.T) {
	h, fc := hlsHarness(t)
	kid := h.session("kid", "", "bearer-kid")
	sibling := h.session("sibling", "", "bearer-sibling")
	adult := h.session("adult", "", "bearer-adult")

	// An authorized index binds its key to this session and item.
	idx := hlsGet(h, "/stream/hls?src="+url.QueryEscape("/stream/movies/ok"), kid)
	okKey := sourceKey(h, "/stream/movies/ok")
	if idx.status != http.StatusFound || idx.header.Get("Location") != "/stream/hls/"+okKey+"/index.m3u8" {
		t.Fatalf("authorized index: %d loc=%q body=%s", idx.status, idx.header.Get("Location"), idx.body)
	}
	for _, f := range []string{"index.m3u8", "seg_00001.ts"} {
		res := hlsGet(h, "/stream/hls/"+okKey+"/"+f, kid)
		if res.status != http.StatusOK || !strings.Contains(res.body, okKey+"/"+f) {
			t.Fatalf("bound asset %s: %d %s", f, res.status, res.body)
		}
	}

	// A blocked item's index is refused and binds nothing.
	assertParentalError(t, "GET /stream/hls", hlsGet(h, "/stream/hls?src="+url.QueryEscape("/stream/movies/bad"), kid), http.StatusForbidden, parentalCodeBlocked)
	hits := h.upHits.Load()
	// A key computed from the blocked src was never bound: refused, upstream untouched.
	badKey := sourceKey(h, "/stream/movies/bad")
	for _, f := range []string{"index.m3u8", "seg_00000.ts"} {
		assertParentalError(t, "GET /stream/hls/{key}/{file}", hlsGet(h, "/stream/hls/"+badKey+"/"+f, kid), http.StatusForbidden, parentalCodeBlocked)
	}
	// A key bound to another session is refused.
	assertParentalError(t, "GET /stream/hls/{key}/{file}", hlsGet(h, "/stream/hls/"+okKey+"/seg_00002.ts", sibling), http.StatusForbidden, parentalCodeBlocked)
	if h.upHits.Load() != hits {
		t.Fatal("refused HLS assets reached the transcoder")
	}

	// The bound item is re-evaluated on every asset request.
	fc.set("movie/ok", parental.Classification{State: parental.Rated, Rating: "PG-13", TagsKnown: true})
	assertParentalError(t, "GET /stream/hls/{key}/{file}", hlsGet(h, "/stream/hls/"+okKey+"/seg_00003.ts", kid), http.StatusForbidden, parentalCodeBlocked)
	fc.mu.Lock()
	fc.err = errors.New("media module down")
	fc.mu.Unlock()
	assertParentalError(t, "GET /stream/hls/{key}/{file}", hlsGet(h, "/stream/hls/"+okKey+"/seg_00003.ts", kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)

	// Unrestricted principals keep today's behaviour: no binding needed.
	if res := hlsGet(h, "/stream/hls/"+badKey+"/seg_00000.ts", adult); res.status != http.StatusOK {
		t.Fatalf("unrestricted asset: %d %s", res.status, res.body)
	}
}

func TestParentalHLSBindingExpiresAndSurvivesPolicyRestriction(t *testing.T) {
	h, _ := hlsHarness(t)
	kid := h.session("kid", "", "bearer-kid")
	if res := hlsGet(h, "/stream/hls?src="+url.QueryEscape("/stream/movies/ok"), kid); res.status != http.StatusFound {
		t.Fatalf("index: %d %s", res.status, res.body)
	}
	key := sourceKey(h, "/stream/movies/ok")
	// Sliding expiry: use keeps it alive, idleness ends it.
	h.clock.Advance(hlsBindingTTL - time.Minute)
	if res := hlsGet(h, "/stream/hls/"+key+"/seg_00001.ts", kid); res.status != http.StatusOK {
		t.Fatalf("binding before expiry: %d %s", res.status, res.body)
	}
	h.clock.Advance(hlsBindingTTL)
	assertParentalError(t, "GET /stream/hls/{key}/{file}", hlsGet(h, "/stream/hls/"+key+"/seg_00002.ts", kid), http.StatusForbidden, parentalCodeBlocked)
}

// With the S5a classifier (classification unavailable) a restricted principal
// cannot obtain or use any HLS key.
func TestParentalHLSDefaultClassifierRefusesRestricted(t *testing.T) {
	h := newParentalHarness(t)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, restrictedPolicyJSON) })
	kid := h.session("kid", "", "bearer-kid")
	assertParentalError(t, "GET /stream/hls", hlsGet(h, "/stream/hls?src="+url.QueryEscape("/stream/movies/m1"), kid), http.StatusForbidden, parentalCodeBlocked)
	assertParentalError(t, "GET /stream/hls/{key}/{file}", hlsGet(h, "/stream/hls/"+testHLSKeyHex+"/index.m3u8", kid), http.StatusForbidden, parentalCodeBlocked)
	if len(h.s.parental.hls.byID) != 0 || h.upHits.Load() != 0 {
		t.Fatal("restricted principal bound a key or reached the transcoder")
	}
}

func TestHLSKeyFromRedirect(t *testing.T) {
	key := strings.Repeat("ab", 16)
	resp := func(status int, loc string) *http.Response {
		r := &http.Response{StatusCode: status, Header: http.Header{}}
		r.Header.Set("Location", loc)
		return r
	}
	if got, ok := hlsKeyFromRedirect(resp(http.StatusFound, "/stream/hls/"+key+"/index.m3u8")); !ok || got != key {
		t.Fatalf("valid redirect: %q %v", got, ok)
	}
	for _, r := range []*http.Response{
		resp(http.StatusOK, "/stream/hls/"+key+"/index.m3u8"),
		resp(http.StatusFound, "http://transcoder/stream/hls/"+key+"/index.m3u8"),
		resp(http.StatusFound, "//evil/stream/hls/"+key+"/index.m3u8"),
		resp(http.StatusFound, "/stream/hls/"+key+"/seg_00001.ts"),
		resp(http.StatusFound, "/stream/hls/"+key+"/index.m3u8?x=1"),
		resp(http.StatusFound, "/stream/hls/NOTHEX0123456789/index.m3u8"),
		resp(http.StatusFound, "/stream/hls/"+key+"%2Findex.m3u8"),
		resp(http.StatusFound, "/stream/movies/m1"),
		nil,
	} {
		if got, ok := hlsKeyFromRedirect(r); ok {
			t.Errorf("accepted %v → %q", r, got)
		}
	}
}

func TestHLSKeyBindingsUnit(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	b := newHLSKeyBindings()
	key1, key2 := strings.Repeat("1", 32), strings.Repeat("2", 32)
	item := parentalItem{Kind: "movie", ID: "ok"}
	if b.bind("", key1, item, now) || b.bind("s1", "not-a-key", item, now) {
		t.Fatal("bound without a session or with an invalid key")
	}
	if !b.bind("s1", key1, item, now) || !b.bind("s1", key2, item, now) {
		t.Fatal("bind failed")
	}
	if got, ok := b.lookup("s1", key1, now); !ok || got != item {
		t.Fatal("lookup of a bound key failed")
	}
	if _, ok := b.lookup("s2", key1, now); ok {
		t.Fatal("another session used the key")
	}
	if _, ok := b.lookup("s1", key1, now.Add(hlsBindingTTL)); ok {
		t.Fatal("expired binding served")
	}
	assertBindingIndex(t, b)
	var nilB *hlsKeyBindings
	if nilB.bind("s1", key1, item, now) {
		t.Fatal("nil table bound a key")
	}
	if _, ok := nilB.lookup("s1", key1, now); ok {
		t.Fatal("nil table found a key")
	}
}

func hlsTestKey(i int) string { return fmt.Sprintf("%032x", i) }

// assertBindingIndex checks the per-session index matches the table.
func assertBindingIndex(t *testing.T, b *hlsKeyBindings) {
	t.Helper()
	n := 0
	for sid, ids := range b.bySession {
		if len(ids) == 0 {
			t.Fatalf("empty index for session %q", sid)
		}
		for id := range ids {
			if e, ok := b.byID[id]; !ok || e.sessionID != sid {
				t.Fatalf("index entry %q for %q not in table", id, sid)
			}
			n++
		}
	}
	if n != len(b.byID) {
		t.Fatalf("index holds %d bindings, table %d", n, len(b.byID))
	}
}

// One session at its cap evicts only its own least recently used binding; it
// cannot push another session's playback out of the table.
func TestHLSKeyBindingsPerSessionCap(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	b := newHLSKeyBindings()
	item := parentalItem{Kind: "movie", ID: "ok"}
	victimKey := strings.Repeat("f", 32)
	if !b.bind("victim", victimKey, item, now) {
		t.Fatal("victim bind failed")
	}
	for i := range hlsBindingPerSession * 4 {
		now = now.Add(time.Second)
		if !b.bind("attacker", hlsTestKey(i), item, now) {
			t.Fatalf("attacker bind %d refused", i)
		}
		if i == 0 {
			// Keep the first key in use: it must survive as most recently used.
			continue
		}
		if _, ok := b.lookup("attacker", hlsTestKey(0), now); !ok {
			t.Fatalf("recently used key evicted at bind %d", i)
		}
	}
	if got := len(b.bySession["attacker"]); got != hlsBindingPerSession {
		t.Fatalf("attacker holds %d bindings, cap %d", got, hlsBindingPerSession)
	}
	if _, ok := b.lookup("attacker", hlsTestKey(1), now); ok {
		t.Fatal("least recently used attacker binding was not evicted")
	}
	if _, ok := b.lookup("victim", victimKey, now); !ok {
		t.Fatal("another session's binding was evicted")
	}
	assertBindingIndex(t, b)
}

// A full table never refuses a new binding: the least recently used binding
// in the whole table goes, and expired ones go first.
func TestHLSKeyBindingsGlobalCap(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	b := newHLSKeyBindings()
	b.max, b.perSession = 4, 4
	item := parentalItem{Kind: "movie", ID: "ok"}
	for i := range 4 {
		now = now.Add(time.Second)
		if !b.bind(fmt.Sprintf("s%d", i), hlsTestKey(i), item, now) {
			t.Fatal("bind failed")
		}
	}
	now = now.Add(time.Second)
	if _, ok := b.lookup("s0", hlsTestKey(0), now); !ok { // s0 is now the most recently used
		t.Fatal("lookup failed")
	}
	if !b.bind("s9", hlsTestKey(9), item, now) {
		t.Fatal("full table refused a new binding")
	}
	if _, ok := b.lookup("s1", hlsTestKey(1), now); ok {
		t.Fatal("least recently used binding survived a full table")
	}
	for _, sid := range []int{0, 2, 3, 9} {
		if _, ok := b.lookup(fmt.Sprintf("s%d", sid), hlsTestKey(sid), now); !ok {
			t.Fatalf("binding s%d evicted", sid)
		}
	}
	if len(b.byID) != 4 {
		t.Fatalf("table size %d", len(b.byID))
	}
	assertBindingIndex(t, b)
}
