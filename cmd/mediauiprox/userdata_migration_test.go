package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/store"
)

// Review follow-ups for ADR-0033 S9b: pre-upgrade local data migration,
// TENANT_MODE isolation, fallback classes, the fixed BFF identity and URL
// redaction. Provider behaviour is the published userdata-local v0.1.6 daemon
// over mTLS (userdata_provider_real_test.go) unless a fake is named.

// countingProvider wraps the production client, counts operations and can make
// a userdata PUT fail (optionally after forwarding it: an uncertain outcome).
type countingProvider struct {
	next userdataProviderClient
	mu   sync.Mutex
	ops  map[httpclient.Operation]int
	// putHook decides for the n-th userdata PUT: forward it, and the error to
	// return instead of the response (nil: normal).
	putHook func(n int) (forward bool, err error)
}

func newCountingProvider(next userdataProviderClient) *countingProvider {
	return &countingProvider{next: next, ops: map[httpclient.Operation]int{}}
}

func (c *countingProvider) Do(ctx context.Context, op httpclient.Operation, h http.Header, body io.Reader) (*http.Response, error) {
	c.mu.Lock()
	c.ops[op]++
	n, hook := c.ops[op], c.putHook
	c.mu.Unlock()
	if op == httpclient.PutUserdata && hook != nil {
		forward, err := hook(n)
		if err != nil {
			if forward {
				if resp, e := c.next.Do(ctx, op, h, body); e == nil {
					_ = resp.Body.Close()
				}
			}
			return nil, err
		}
	}
	return c.next.Do(ctx, op, h, body)
}

func (c *countingProvider) count(op httpclient.Operation) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

var errInjectedOutage = &httpclient.UnavailableError{Reason: httpclient.ReasonTransport, Cause: errors.New("injected outage")}

func progressBlob(t *testing.T, ids ...string) store.Blob {
	t.Helper()
	b := store.Blob{Progress: map[string]json.RawMessage{}}
	for i, id := range ids {
		b.Progress[id] = json.RawMessage(fmt.Sprintf(`{"id":%q,"kind":"movie","title":"T","href":"/m/%s","positionSec":%d,"durationSec":1000,"updatedAt":"2026-10-0%dT00:00:00Z"}`, id, id, 10+i, 1+i%8))
	}
	return b
}

func providerBlobFor(t *testing.T, client userdataProviderClient, user string) store.Blob {
	t.Helper()
	u := &serverUserdata{provider: client}
	b, err := u.providerGet(t.Context(), userdataRequest{scope: store.Scope{UserID: user}, bearer: "tok-user-" + user})
	if err != nil {
		t.Fatalf("direct provider GET for %s: %v", user, err)
	}
	return b
}

func providerPutFor(t *testing.T, client userdataProviderClient, user string, b store.Blob) {
	t.Helper()
	u := &serverUserdata{provider: client}
	if _, err := u.providerPut(t.Context(), userdataRequest{scope: store.Scope{UserID: user}, bearer: "tok-user-" + user}, b); err != nil {
		t.Fatalf("direct provider PUT for %s: %v", user, err)
	}
}

// bffFor builds a BFF userdata store in dir with a session for user that holds
// the user's auth-local bearer (or none).
func bffFor(t *testing.T, dir string, client userdataProviderClient, user, bearer string) (*server, string) {
	t.Helper()
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth(user, user, "", []string{"user"}, bearer)
	if err != nil {
		t.Fatal(err)
	}
	return &server{userdata: newServerUserdata(dir, client), sessions: sessions}, tok
}

func getViaBFF(t *testing.T, s *server, session string, hdr map[string]string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: session})
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.handleUserdataGet(w, req)
	return w.Code, w.Body.String()
}

func putViaBFF(t *testing.T, s *server, session, body string, hdrs ...map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: session})
	for _, hdr := range hdrs {
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	s.handleUserdataPut(w, req)
	return w.Code, w.Body.String()
}

func progressKeys(t *testing.T, body string) map[string]bool {
	t.Helper()
	out, err := progressKeysOf(body)
	if err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

// progressKeysOf is progressKeys for goroutines (no t.Fatal).
func progressKeysOf(body string) (map[string]bool, error) {
	var b store.Blob
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for k := range b.Progress {
		out[k] = true
	}
	return out, nil
}

func markerState(t *testing.T, u *serverUserdata, user string) string {
	t.Helper()
	path := u.migration.markerPath(store.Scope{UserID: user})
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("marker %s mode %v, want 0600", path, st.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["user_id"] != user {
		t.Fatalf("marker user %q", m["user_id"])
	}
	return m["state"]
}

// Finding 1: data written before the upgrade lives only in the local store
// (the old proxy called /userdata). It must reach the provider once.
func TestRealUserdataProviderMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the userdata-local daemon")
	}
	p := startRealProvider(t, true)
	media := householdProvider(t, p.origin, "media-ui", p.pki.enrolled(t, "media-ui"))

	t.Run("an unset provider blob carries exactly the vendored default prefs", func(t *testing.T) {
		b := providerBlobFor(t, media, "fresh-user")
		if blobHasCollections(b) || rawJSONEmpty(b.Prefs) || prefsCustomized(b.Prefs) {
			t.Fatalf("fresh provider blob %+v (prefs %s): providerDefaultPrefsJSON drifted from userdata-local", b, b.Prefs)
		}
	})

	t.Run("pre-upgrade data appears after the upgrade, once", func(t *testing.T) {
		dir := t.TempDir()
		pre, _ := store.New(dir)
		if _, err := pre.Put(store.Scope{UserID: "mig1"}, progressBlob(t, "m1", "m2", "m3")); err != nil {
			t.Fatal(err)
		}
		if _, err := pre.Put(store.Scope{UserID: "mig1"}, store.Blob{Favorites: map[string]json.RawMessage{"f1": json.RawMessage(`{"id":"f1","kind":"movie","title":"F","href":"/m/f1"}`)}}); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(dir + "/mig1.json")
		cp := newCountingProvider(media)
		s, tok := bffFor(t, dir, cp, "mig1", "tok-user-mig1")
		code, body := getViaBFF(t, s, tok, nil)
		if code != http.StatusOK || len(progressKeys(t, body)) != 3 || !strings.Contains(body, `"f1"`) {
			t.Fatalf("post-upgrade GET: %d %s", code, body)
		}
		if got := providerBlobFor(t, media, "mig1"); len(got.Progress) != 3 || len(got.Favorites) != 1 {
			t.Fatalf("provider after migration: %+v", got)
		}
		if st := markerState(t, s.userdata, "mig1"); st != migrationMigrated || cp.count(httpclient.PutUserdata) != 1 {
			t.Fatalf("marker=%q puts=%d", st, cp.count(httpclient.PutUserdata))
		}
		// Idempotent: again, and after a BFF restart over the same directory.
		for range 2 {
			if code, body := getViaBFF(t, s, tok, nil); code != http.StatusOK || len(progressKeys(t, body)) != 3 {
				t.Fatalf("re-run: %d %s", code, body)
			}
		}
		s2, tok2 := bffFor(t, dir, cp, "mig1", "tok-user-mig1")
		if code, body := getViaBFF(t, s2, tok2, nil); code != http.StatusOK || len(progressKeys(t, body)) != 3 {
			t.Fatalf("after restart: %d %s", code, body)
		}
		if n := cp.count(httpclient.PutUserdata); n != 1 {
			t.Fatalf("migration PUT repeated: %d", n)
		}
		if after, _ := os.ReadFile(dir + "/mig1.json"); !bytes.Equal(before, after) {
			t.Fatal("migration modified the local file")
		}
	})

	t.Run("provider already populated keeps the provider copy and the local file", func(t *testing.T) {
		dir := t.TempDir()
		pre, _ := store.New(dir)
		if _, err := pre.Put(store.Scope{UserID: "mig2"}, progressBlob(t, "local-a", "local-b")); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(dir + "/mig2.json")
		providerPutFor(t, media, "mig2", progressBlob(t, "remote-z"))
		cp := newCountingProvider(media)
		s, tok := bffFor(t, dir, cp, "mig2", "tok-user-mig2")
		code, body := getViaBFF(t, s, tok, nil)
		if keys := progressKeys(t, body); code != http.StatusOK || len(keys) != 1 || !keys["remote-z"] {
			t.Fatalf("GET: %d %s", code, body)
		}
		if cp.count(httpclient.PutUserdata) != 0 || markerState(t, s.userdata, "mig2") != migrationProviderKept {
			t.Fatalf("puts=%d marker=%q", cp.count(httpclient.PutUserdata), markerState(t, s.userdata, "mig2"))
		}
		if after, _ := os.ReadFile(dir + "/mig2.json"); !bytes.Equal(before, after) {
			t.Fatal("local file modified")
		}
	})

	t.Run("failed migration serves local, marks nothing and retries", func(t *testing.T) {
		dir := t.TempDir()
		pre, _ := store.New(dir)
		if _, err := pre.Put(store.Scope{UserID: "mig3"}, progressBlob(t, "x1", "x2")); err != nil {
			t.Fatal(err)
		}
		cp := newCountingProvider(media)
		cp.putHook = func(n int) (bool, error) {
			switch n {
			case 1:
				return false, errInjectedOutage // not applied
			case 2:
				return true, errInjectedOutage // applied, response lost
			}
			return false, nil
		}
		s, tok := bffFor(t, dir, cp, "mig3", "tok-user-mig3")
		// Attempt 1 (GET): the PUT is refused before it is applied.
		if code, body := getViaBFF(t, s, tok, nil); code != http.StatusOK || len(progressKeys(t, body)) != 2 {
			t.Fatalf("attempt 1: local copy not served: %d %s", code, body)
		}
		if st := markerState(t, s.userdata, "mig3"); st != migrationAttempted {
			t.Fatalf("attempt 1 recorded %q after a failed PUT", st)
		}
		if got := providerBlobFor(t, media, "mig3"); len(got.Progress) != 0 {
			t.Fatal("refused PUT reached the provider")
		}
		// Attempt 2 (a write): the migration PUT is applied but its response is
		// lost; the write lands locally because the local copy stays
		// authoritative while the migration is pending.
		if code, body := putViaBFF(t, s, tok, string(mustJSON(t, progressBlob(t, "x3")))); code != http.StatusOK || len(progressKeys(t, body)) != 3 {
			t.Fatalf("attempt 2 (PUT while pending): %d %s", code, body)
		}
		if st := markerState(t, s.userdata, "mig3"); st != migrationAttempted {
			t.Fatalf("attempt 2 recorded %q after an uncertain PUT", st)
		}
		if got := providerBlobFor(t, media, "mig3"); len(got.Progress) != 2 {
			t.Fatalf("uncertain PUT: provider has %d keys", len(got.Progress))
		}
		// Attempt 3: the partially populated provider is recognised as our own
		// earlier attempt and the full local copy (x1..x3) is re-sent once.
		if code, body := getViaBFF(t, s, tok, nil); code != http.StatusOK || len(progressKeys(t, body)) != 3 {
			t.Fatalf("attempt 3: %d %s", code, body)
		}
		got := providerBlobFor(t, media, "mig3")
		if len(got.Progress) != 3 || markerState(t, s.userdata, "mig3") != migrationMigrated || cp.count(httpclient.PutUserdata) != 3 {
			t.Fatalf("after retry: provider %d keys, marker %q, puts %d", len(got.Progress), markerState(t, s.userdata, "mig3"), cp.count(httpclient.PutUserdata))
		}
	})

	t.Run("concurrent first loads migrate once without losing keys", func(t *testing.T) {
		dir := t.TempDir()
		pre, _ := store.New(dir)
		ids := []string{"c1", "c2", "c3", "c4", "c5"}
		if _, err := pre.Put(store.Scope{UserID: "mig4"}, progressBlob(t, ids...)); err != nil {
			t.Fatal(err)
		}
		cp := newCountingProvider(media)
		s, tok := bffFor(t, dir, cp, "mig4", "tok-user-mig4")
		var wg sync.WaitGroup
		errs := make(chan string, 8)
		for range 8 {
			wg.Go(func() {
				code, body := getViaBFF(t, s, tok, nil)
				if keys, err := progressKeysOf(body); code != http.StatusOK || err != nil || len(keys) != len(ids) {
					errs <- fmt.Sprintf("%d %s", code, body)
				}
			})
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error("concurrent load:", e)
		}
		if n := cp.count(httpclient.PutUserdata); n != 1 {
			t.Fatalf("migration PUTs=%d, want a single winner", n)
		}
		if got := providerBlobFor(t, media, "mig4"); len(got.Progress) != len(ids) {
			t.Fatalf("provider keys %d", len(got.Progress))
		}
	})

	t.Run("first operation is a write: migrate first, then write", func(t *testing.T) {
		dir := t.TempDir()
		pre, _ := store.New(dir)
		if _, err := pre.Put(store.Scope{UserID: "mig5"}, progressBlob(t, "old1", "old2")); err != nil {
			t.Fatal(err)
		}
		s, tok := bffFor(t, dir, media, "mig5", "tok-user-mig5")
		if code, body := putViaBFF(t, s, tok, string(mustJSON(t, progressBlob(t, "new1")))); code != http.StatusOK || len(progressKeys(t, body)) != 3 {
			t.Fatalf("first PUT: %d %s", code, body)
		}
		if got := providerBlobFor(t, media, "mig5"); len(got.Progress) != 3 {
			t.Fatalf("provider %d keys", len(got.Progress))
		}
	})

	t.Run("customized provider prefs are never overwritten", func(t *testing.T) {
		dir := t.TempDir()
		pre, _ := store.New(dir)
		local := progressBlob(t, "p1")
		local.Prefs = json.RawMessage(`{"display":{"theme":"light"}}`)
		if _, err := pre.Put(store.Scope{UserID: "mig6"}, local); err != nil {
			t.Fatal(err)
		}
		// An admin-ui PIN write customizes the provider prefs only.
		var def map[string]any
		_ = json.Unmarshal([]byte(providerDefaultPrefsJSON), &def)
		def["parental"] = map[string]any{"pin_hash": "abc"}
		pin, _ := json.Marshal(def)
		providerPutFor(t, media, "mig6", store.Blob{Prefs: pin})
		s, tok := bffFor(t, dir, media, "mig6", "tok-user-mig6")
		if code, body := getViaBFF(t, s, tok, nil); code != http.StatusOK || !progressKeys(t, body)["p1"] {
			t.Fatalf("GET: %d %s", code, body)
		}
		got := providerBlobFor(t, media, "mig6")
		if !strings.Contains(string(got.Prefs), "pin_hash") || strings.Contains(string(got.Prefs), `"light"`) {
			t.Fatalf("provider prefs overwritten: %s", got.Prefs)
		}
	})

	t.Run("nothing to migrate and bearer-less sessions", func(t *testing.T) {
		dir := t.TempDir()
		cp := newCountingProvider(media)
		s, tok := bffFor(t, dir, cp, "mig7", "tok-user-mig7")
		if code, _ := getViaBFF(t, s, tok, nil); code != http.StatusOK || markerState(t, s.userdata, "mig7") != migrationNothing || cp.count(httpclient.PutUserdata) != 0 {
			t.Fatalf("empty local: %d marker=%q", code, markerState(t, s.userdata, "mig7"))
		}
		// Quick Connect / auth-less: no bearer → local store only, no provider call.
		qdir := t.TempDir()
		qc := newCountingProvider(media)
		qs, qtok := bffFor(t, qdir, qc, "qc-user", "")
		if code, body := putViaBFF(t, qs, qtok, string(mustJSON(t, progressBlob(t, "q1"))), nil); code != http.StatusOK || !progressKeys(t, body)["q1"] {
			t.Fatalf("bearer-less PUT: %d %s", code, body)
		}
		if code, body := getViaBFF(t, qs, qtok, nil); code != http.StatusOK || !progressKeys(t, body)["q1"] {
			t.Fatalf("bearer-less GET: %d %s", code, body)
		}
		if len(qc.ops) != 0 || markerState(t, qs.userdata, "qc-user") != "" {
			t.Fatalf("bearer-less session reached the provider: %v", qc.ops)
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Finding 2: in TENANT_MODE a tenant-A admin overriding the target to a tenant-B
// user must not reach the provider (keyed by user ID only, tenant-blind admin).
func TestRealUserdataProviderTenantModeIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the userdata-local daemon")
	}
	p := startRealProvider(t, true)
	media := householdProvider(t, p.origin, "media-ui", p.pki.enrolled(t, "media-ui"))
	// bob (tenant tB) has a favorite in the provider.
	if _, err := (&serverUserdata{provider: media}).providerPut(t.Context(),
		userdataRequest{scope: store.Scope{UserID: "bob"}, bearer: "tok-bob"},
		store.Blob{Favorites: map[string]json.RawMessage{"bob-secret": json.RawMessage(`{"id":"bob-secret","kind":"movie","title":"B","href":"/m/b"}`)}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TENANT_MODE", "1")
	cp := newCountingProvider(media)
	sessions := newSessionStore(time.Hour)
	admin, err := sessions.CreateWithAuth("admin", "admin", "tA", []string{"admin"}, "tok-admin")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{userdata: newServerUserdata(t.TempDir(), cp), sessions: sessions}
	override := map[string]string{muxcoreUserIDHeader: "bob"}
	code, body := getViaBFF(t, s, admin, override)
	if code != http.StatusOK || strings.Contains(body, "bob-secret") {
		t.Fatalf("tenant-A admin read tenant-B blob through the provider: %d %s", code, body)
	}
	if code, body := putViaBFF(t, s, admin, `{"favorites":{"planted":{"id":"planted"}}}`, override); code != http.StatusOK {
		t.Fatalf("PUT: %d %s", code, body)
	}
	if n := len(cp.ops); n != 0 {
		t.Fatalf("TENANT_MODE reached the provider: %v", cp.ops)
	}
	if strings.Contains(string(mustJSON(t, providerBlobFor(t, media, "bob").Favorites)), "planted") {
		t.Fatal("tenant-A admin wrote into tenant-B provider blob")
	}
}

func TestUserdataProviderAllowedMatrix(t *testing.T) {
	u := &serverUserdata{provider: devUserdataProvider(t, "http://127.0.0.1:9", time.Second)}
	alice := store.Scope{UserID: "alice"}
	bob := store.Scope{UserID: "bob"}
	cases := []struct {
		name       string
		tenantMode bool
		scope      store.Scope
		bearer     string
		privileged bool
		want       bool
	}{
		{"self", false, alice, "b", false, true},
		{"self without bearer", false, alice, "", false, false},
		{"other user, member", false, bob, "b", false, false},
		{"other user, privileged", false, bob, "b", true, true},
		{"tenant mode self", true, alice, "b", false, false},
		{"tenant mode privileged override", true, bob, "b", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tenantMode {
				t.Setenv("TENANT_MODE", "1")
			}
			if got := u.providerAllowed(tc.scope, tc.bearer, "alice", tc.privileged); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	if (&serverUserdata{}).providerAllowed(alice, "b", "alice", false) {
		t.Fatal("allowed without a provider")
	}
}

// Finding 6: only outages fall back to the local store; a provider 4xx is the
// client's answer (no provider body, nothing written locally).
func TestUserdataProviderFallbackClasses(t *testing.T) {
	pki := newTestPKI(t, "muxcore-ca")
	prov := newFakeTLSProvider(t, pki.enrolled(t, "userdata-local"), pki.pool)
	client := householdProvider(t, prov.origin(), "media-ui", pki.enrolled(t, "media-ui"))
	type want struct {
		status   int
		code     string
		fallback bool
	}
	cases := map[int]want{
		400: {400, "userdata.invalid", false},
		401: {401, "userdata.session_invalid", false},
		403: {403, "userdata.forbidden", false},
		404: {404, "userdata.provider_rejected", false},
		409: {409, "userdata.provider_rejected", false},
		413: {413, "userdata.too_large", false},
		408: {200, "", true},
		429: {200, "", true},
		500: {200, "", true},
		502: {200, "", true},
		503: {200, "", true},
	}
	for status, w := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			t.Run(fmt.Sprintf("%s %d", method, status), func(t *testing.T) {
				prov.setFunc(func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && method == http.MethodPut {
						_, _ = rw.Write([]byte(`{}`)) // migration check: empty provider
						return
					}
					rw.WriteHeader(status)
					_, _ = rw.Write([]byte(`{"code":"SECRET-PROVIDER-BODY"}`))
				})
				dir := t.TempDir()
				s, tok := bffFor(t, dir, client, "carol", "tok-carol")
				var code int
				var body string
				if method == http.MethodGet {
					if _, err := s.userdata.store.Put(store.Scope{UserID: "carol"}, progressBlob(t, "local")); err != nil {
						t.Fatal(err)
					}
					code, body = getViaBFF(t, s, tok, nil)
				} else {
					code, body = putViaBFF(t, s, tok, string(mustJSON(t, progressBlob(t, "written"))), nil)
				}
				if strings.Contains(body, "SECRET-PROVIDER-BODY") {
					t.Fatalf("provider body leaked: %s", body)
				}
				if code != w.status || (w.code != "" && !strings.Contains(body, `"`+w.code+`"`)) {
					t.Fatalf("got %d %s, want %d %s", code, body, w.status, w.code)
				}
				local := s.userdata.store.Get(store.Scope{UserID: "carol"})
				if method == http.MethodPut {
					if _, wrote := local.Progress["written"]; wrote != w.fallback {
						t.Fatalf("local write=%v, want %v", wrote, w.fallback)
					}
				} else if w.fallback && !progressKeys(t, body)["local"] {
					t.Fatalf("outage did not serve the local copy: %s", body)
				}
				if s.userdata.degraded.Load() != w.fallback {
					t.Fatalf("degraded=%v want %v", s.userdata.degraded.Load(), w.fallback)
				}
			})
		}
	}
	t.Run("transport outage falls back", func(t *testing.T) {
		dead := householdProvider(t, "https://127.0.0.1:1", "media-ui", pki.enrolled(t, "media-ui"))
		s, tok := bffFor(t, t.TempDir(), dead, "carol", "tok-carol")
		if code, body := putViaBFF(t, s, tok, string(mustJSON(t, progressBlob(t, "w"))), nil); code != http.StatusOK || !progressKeys(t, body)["w"] {
			t.Fatalf("transport outage: %d %s", code, body)
		}
	})
}

// Finding 4: the provider client is bound to the BFF's fixed identity, so a
// MUXCORE_MODULE_ID override fails at startup (not as module_forbidden 503s).
func TestUserdataProviderStartupIdentity(t *testing.T) {
	pki := newTestPKI(t, "muxcore-ca")
	householdProvider(t, "https://userdata-local:9672", "media-ui", pki.enrolled(t, "media-ui")) // sets the household env
	t.Setenv("USERDATA_LOCAL_URL", "https://userdata-local:9672")
	if startupUserdataProvider() == nil {
		t.Fatal("no provider with the default identity")
	}
	other := pki.enrolled(t, "media-ui-2")
	t.Setenv("MUXCORE_TLS_CERT", other.certFile)
	t.Setenv("MUXCORE_TLS_KEY", other.keyFile)
	t.Setenv("MUXCORE_MODULE_ID", "media-ui-2")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	if startupUserdataProvider() != nil {
		t.Fatal("MUXCORE_MODULE_ID override accepted at startup")
	}
	if !strings.Contains(buf.String(), "MUXCORE_MODULE_ID") {
		t.Fatalf("startup error does not name the override: %s", buf.String())
	}
}

// Finding 5: a rejected USERDATA_LOCAL_URL is logged without credentials.
func TestUserdataProviderURLRedaction(t *testing.T) {
	pki := newTestPKI(t, "muxcore-ca")
	householdProvider(t, "https://userdata-local:9672", "media-ui", pki.enrolled(t, "media-ui"))
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	for _, raw := range []string{"https://op:s3cr3t-pw@userdata-local:9672", "https://userdata-local:9672/?token=s3cr3t-pw", "https://userdata-local:9672/#s3cr3t-pw", "http://op:s3cr3t-pw@[::1"} {
		if mustUserdataProvider(raw, "media-ui") != nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	out := buf.String()
	if strings.Contains(out, "s3cr3t-pw") || !strings.Contains(out, "REDACTED") || !strings.Contains(out, "unparseable") {
		t.Fatalf("log leaks or lacks redaction: %s", out)
	}
	if got := redactURLForLog("https://userdata-local:9672"); got != `"https://userdata-local:9672"` {
		t.Fatalf("plain origin rendered %s", got)
	}
}
