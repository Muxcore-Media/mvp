package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

// Test harness for ADR-0031 enforcement: a fake userdata-local policy
// provider, a fake upstream standing in for every module, and the BFF mux both
// gated (registerRoutes) and ungated (registerClassifiedRoutes, the baseline).

const (
	unrestrictedPolicyJSON = `{"version":1,"mode":"unrestricted","rules":null}`
	restrictedPolicyJSON   = `{"version":1,"mode":"restricted","rules":{"kids_mode":true,"max_rating":"","blocked_tags":["horror"],"allowed_tags":[],"allow_unrated":true}}`
	testHLSKeyHex          = "0123456789abcdef0123456789abcdef"
)

func configuredDoc(user, tenant string, revision int64, policy string) string {
	u, _ := json.Marshal(user)
	tn, _ := json.Marshal(tenant)
	return fmt.Sprintf(`{"user_id":%s,"tenant_id":%s,"state":"configured","revision":%d,"policy":%s,"updated_at":"2026-10-08T00:00:00Z"}`, u, tn, revision, policy)
}

func unconfiguredDoc(user, tenant string) string {
	u, _ := json.Marshal(user)
	tn, _ := json.Marshal(tenant)
	return fmt.Sprintf(`{"user_id":%s,"tenant_id":%s,"state":"unconfigured","revision":0,"policy":null}`, u, tn)
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// policyRequest is what the fake provider observed.
type policyRequest struct {
	method, path, rawQuery string
	header                 http.Header
}

type fakePolicyProvider struct {
	srv  *httptest.Server
	mu   sync.Mutex
	resp func(w http.ResponseWriter, r *http.Request)
	reqs []policyRequest
}

func newFakePolicyProvider(t *testing.T) *fakePolicyProvider {
	t.Helper()
	p := &fakePolicyProvider{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.reqs = append(p.reqs, policyRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone()})
		resp := p.resp
		p.mu.Unlock()
		if resp == nil {
			http.Error(w, "no fake response", http.StatusInternalServerError)
			return
		}
		resp(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// set makes the provider answer with status and body.
func (p *fakePolicyProvider) set(status int, body string) {
	p.setFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func (p *fakePolicyProvider) setFunc(f func(w http.ResponseWriter, r *http.Request)) {
	p.mu.Lock()
	p.resp = f
	p.mu.Unlock()
}

// doc answers 200 with a document built from the requested target user, so
// several sessions can share one provider.
func (p *fakePolicyProvider) doc(build func(user string) string) {
	p.setFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(build(r.Header.Get(muxcoreUserIDHeader))))
	})
}

func (p *fakePolicyProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reqs)
}

func (p *fakePolicyProvider) requests() []policyRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]policyRequest(nil), p.reqs...)
}

type parentalAuthFixtureKey struct{}

type parentalHarness struct {
	t        *testing.T
	s        *server
	gated    http.Handler
	baseline http.Handler
	provider *fakePolicyProvider
	clock    *fakeClock
	upHits   atomic.Int64
	// hlsKey answers the fake transcoder's playlist redirect for a src.
	hlsKey func(src string) string
	// operatorTwins maps a session to its manager-role twin (tokFor).
	operatorTwins map[string]string
}

func newParentalHarness(t *testing.T) *parentalHarness {
	t.Helper()
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", t.TempDir()+"/playback.json") // defaults
	h := &parentalHarness{t: t, provider: newFakePolicyProvider(t), clock: newFakeClock()}
	h.hlsKey = func(string) string { return testHLSKeyHex }
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.upHits.Add(1)
		if r.URL.Path == "/stream/hls" {
			http.Redirect(w, r, "/stream/hls/"+h.hlsKey(r.URL.Query().Get("src"))+"/index.m3u8", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "upstream %s %s", r.Method, r.URL.RequestURI())
	}))
	t.Cleanup(up.Close)
	u := mustURL(up.URL)
	h.s = &server{
		moviesHTTP: u, tvHTTP: u, requestHTTP: u, musicHTTP: u, booksHTTP: u, comicsHTTP: u,
		audiobooksHTTP: u, transcoderHTTP: u, debridHTTP: u, graphHTTP: u, playbackMonitorHTTP: u,
		taggingHTTP: u, subtitlesHTTP: u,
		requireAuth: true,
		sessions:    newSessionStore(time.Hour),
		parental:    newParentalGateWith(devUserdataProvider(t, h.provider.srv.URL, 2*time.Second), h.clock.Now),
	}
	gated := http.NewServeMux()
	h.s.registerRoutes(gated)
	raw := http.NewServeMux()
	h.s.registerClassifiedRoutes(raw)
	// These tests vary roles for local sessions sharing a provider bearer to
	// isolate the parental/operator gate. Supply their selected identity as an
	// explicit auth fixture; independent session_revalidate tests exercise real
	// provider claim changes, failures and races.
	h.s.auth = sessionValidatorFunc(func(ctx context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
		e, _ := ctx.Value(parentalAuthFixtureKey{}).(sessionEntry)
		return &authv1.ValidateResponse{Valid: e.authToken == req.GetToken(), UserId: e.userID, Username: e.username, TenantId: e.tenantID, Roles: e.roles}, nil
	})
	withFixture := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e, _ := h.s.sessions.get(sessionTokenFromRequest(r))
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), parentalAuthFixtureKey{}, e)))
		})
	}
	h.gated = withFixture(h.s.withAuth(gated))
	h.baseline = withFixture(h.s.withAuth(raw))
	return h
}

// session creates a BFF session holding an auth-local bearer (login path).
func (h *parentalHarness) session(user, tenant, bearer string) string {
	h.t.Helper()
	tok, err := h.s.sessions.CreateWithAuth(user, user, tenant, []string{"user"}, bearer)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

// sessionRoles creates a BFF session with the given roles (login path).
func (h *parentalHarness) sessionRoles(user, tenant, bearer string, roles ...string) string {
	h.t.Helper()
	tok, err := h.s.sessions.CreateWithAuth(user, user, tenant, roles, bearer)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

// tokFor returns tok for routes without the operator role gate and, for
// routes whose table row sets requirePrivileged (T-M5-12), a manager session
// of the same principal (user, tenant, auth-local bearer, so the same policy
// cache entry). Parental tests use it to reach the parental gate behind the
// role gate; the role gate itself is proved in operator_routes_test.go.
func (h *parentalHarness) tokFor(pattern, tok string) string {
	h.t.Helper()
	if !parentalRouteClasses[pattern].requirePrivileged {
		return tok
	}
	if twin, ok := h.operatorTwins[tok]; ok {
		return twin
	}
	user, _, tenant, _, ok := h.s.sessions.LookupRoles(tok)
	if !ok {
		h.t.Fatalf("tokFor: unknown session for %s", pattern)
	}
	twin := h.sessionRoles(user, tenant, h.s.sessions.LookupAuthToken(tok), "manager")
	if h.operatorTwins == nil {
		h.operatorTwins = map[string]string{}
	}
	h.operatorTwins[tok] = twin
	return twin
}

type serveResult struct {
	status int
	body   string
	header http.Header
	panic  string
}

// serve runs one request, recovering a handler panic (several handlers
// dereference module clients this harness leaves nil) so gated and baseline
// runs can still be compared.
func serve(h http.Handler, req *http.Request) (res serveResult) {
	rec := httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			res = serveResult{panic: fmt.Sprint(p)}
		}
	}()
	h.ServeHTTP(rec, req)
	return serveResult{status: rec.Code, body: rec.Body.String(), header: rec.Header()}
}

// routeRequest builds a concrete request for a registered pattern.
func routeRequest(pattern, sessionTok string) *http.Request {
	method, path := http.MethodGet, pattern
	if i := strings.IndexByte(pattern, ' '); i > 0 && !strings.HasPrefix(pattern, "/") {
		method, path = pattern[:i], pattern[i+1:]
	}
	path = strings.NewReplacer("{key}", testHLSKeyHex, "{file}", "index.m3u8", "{id}", "id1",
		"{titleId}", "t1", "{fileId}", "f1", "{action}", "act").Replace(path)
	if strings.HasSuffix(path, "/") && path != "/" {
		path += "item1"
	}
	q := url.Values{}
	q.Set("src", "/stream/movies/m1")
	q.Set("media_id", "m1")
	q.Set("mux_id", "m1")
	q.Set("rating_key", "1")
	q.Set("id", "tmdb:movie:1")
	q.Set("q", "x")
	req := httptest.NewRequest(method, path+"?"+q.Encode(), nil)
	req.Header.Set("Accept", "application/json")
	if sessionTok != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: sessionTok})
	}
	return req
}

// patternsOfClass lists registered patterns of the given classes, sorted.
func patternsOfClass(classes ...routeClass) []string {
	var out []string
	for p, route := range parentalRouteClasses {
		for _, c := range classes {
			if route.class == c {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// assertParentalError checks a gate response: status, code, no-store and a
// body that carries nothing but the error fields.
func assertParentalError(t *testing.T, pattern string, res serveResult, status int, code string) {
	t.Helper()
	if res.panic != "" {
		t.Errorf("%s: handler reached and panicked: %s", pattern, res.panic)
		return
	}
	if res.status != status {
		t.Errorf("%s: status=%d want %d body=%s", pattern, res.status, status, res.body)
		return
	}
	if cc := res.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("%s: Cache-Control=%q want no-store", pattern, cc)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(res.body), &body); err != nil {
		t.Errorf("%s: body not a JSON error object: %s", pattern, res.body)
		return
	}
	got := body["code"]
	if pattern == "GET /api/playback/resolve" && code == parentalCodeBlocked {
		if got != playbackCodeParentalBlocked || body["parental_code"] != parentalCodeBlocked {
			t.Errorf("%s: resolve alias body=%v", pattern, body)
		}
	} else if got != code {
		t.Errorf("%s: code=%q want %q", pattern, got, code)
	}
	for k := range body {
		switch k {
		case "error", "code", "parental_code":
		default:
			t.Errorf("%s: unexpected field %q in gate response", pattern, k)
		}
	}
}
