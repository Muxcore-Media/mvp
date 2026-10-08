package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
)

// T-M5-12 / C-30: state-changing BFF routes that were open to every signed-in
// session. This list is written out from the roadmap task and the review, not
// derived from parentalRouteClasses, so the table and BFF-API.md cannot be
// edited together to reopen a route: each pattern must stay table-gated and
// is proved by request below.
type operatorExpectation struct {
	// rootPathAdmin: a body naming root_folder_path also needs admin.
	rootPathAdmin bool
	// body reaches the module for a privileged caller.
	body string
}

var tM512OperatorRoutes = map[string]operatorExpectation{
	// Library removal and files.
	"DELETE /api/movies/{id}":        {},
	"DELETE /api/movies/{id}/file":   {},
	"DELETE /api/tv/{id}":            {},
	"DELETE /api/episodes/{id}/file": {},
	"DELETE /api/music/{id}":         {},
	"DELETE /api/books/{id}":         {},
	"DELETE /api/books/works/{id}":   {},
	"DELETE /api/comics/{id}":        {},
	"DELETE /api/comics/issues/{id}": {},
	"DELETE /api/audiobooks/{id}":    {},
	"POST /api/movies/{id}/refresh":  {},
	"POST /api/tv/{id}/refresh":      {},
	"POST /api/music/{id}/refresh":   {},
	// Monitoring, quality profile and root folder.
	"PATCH /api/movies/{id}":        {rootPathAdmin: true, body: `{"monitored":true}`},
	"PATCH /api/tv/{id}":            {rootPathAdmin: true, body: `{"monitored":true}`},
	"PATCH /api/tv/seasons/{id}":    {body: `{"monitored":true}`},
	"PATCH /api/episodes/{id}":      {body: `{"monitored":true}`},
	"PATCH /api/music/{id}":         {rootPathAdmin: true, body: `{"monitored":true}`},
	"PATCH /api/music/albums/{id}":  {body: `{"monitored":true}`},
	"PATCH /api/books/{id}":         {rootPathAdmin: true, body: `{"monitored":true}`},
	"PATCH /api/books/works/{id}":   {body: `{"monitored":true}`},
	"PATCH /api/comics/{id}":        {rootPathAdmin: true, body: `{"monitored":true}`},
	"PATCH /api/comics/issues/{id}": {body: `{"monitored":true}`},
	"PATCH /api/audiobooks/{id}":    {rootPathAdmin: true, body: `{"monitored":true}`},
	"PUT /api/tv/{id}/override":     {body: `{"delay_minutes":5}`},
	"POST /api/tv/{id}/override":    {body: `{"delay_minutes":5}`},
	"DELETE /api/tv/{id}/override":  {},
	// Acquisition.
	"POST /api/releases/grab":       {body: `{"guid":"g1","title":"T","download_url":"http://fixture.invalid/x.torrent","item_type":"movie","item_id":"m1"}`},
	"POST /api/releases/search-now": {body: `{"item_type":"movie","item_id":"m1"}`},
	"POST /api/releases/block":      {body: `{"guid":"g1","item_id":"m1"}`},
	"POST /api/wanted":              {body: `{"item_type":"movie","item_id":"m1","title":"T"}`},
	"POST /api/wanted/remove":       {body: `{"queue_id":"q1"}`},
	"POST /api/activity/retry":      {body: `{"history_id":"h1"}`},
	"POST /api/blocklist/clear":     {body: `{"clear_all":true}`},
	"PUT /api/delay-profiles":       {body: `{"protocol":"torrent","wait_minutes":10}`},
	"POST /api/delay-profiles":      {body: `{"protocol":"torrent","wait_minutes":10}`},
	"POST /api/debrid/add":          {body: `{"magnet":"magnet:?xt=urn:btih:0"}`},
	"POST /api/formats/sync-trash":  {body: `{"official":false}`},
	"POST /api/rename":              {body: `{"movie_id":"m1"}`},
	"POST /api/import":              {body: `{"path":"/library/import/m1"}`},
	"POST /api/subtitles/download":  {body: `{"id":"s1"}`},
	// Other.
	"POST /api/sessions/{id}/stop": {},
	"POST /api/livetv/timers":      {body: `{"channel_id":"ch1","title":"T"}`},
}

// recordingConn is a grpc.ClientConnInterface that records every RPC and
// answers with an empty reply, so a module call is observable whatever client
// the handler uses.
type recordingConn struct {
	mu    sync.Mutex
	calls []recordedRPC
}

type recordedRPC struct {
	method string
	args   any
}

func (c *recordingConn) Invoke(_ context.Context, method string, args, _ any, _ ...grpc.CallOption) error {
	c.mu.Lock()
	c.calls = append(c.calls, recordedRPC{method: method, args: args})
	c.mu.Unlock()
	return nil
}

func (c *recordingConn) NewStream(_ context.Context, _ *grpc.StreamDesc, method string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	c.mu.Lock()
	c.calls = append(c.calls, recordedRPC{method: method})
	c.mu.Unlock()
	return nil, errors.New("streams are not faked")
}

func (c *recordingConn) snapshot() []recordedRPC {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]recordedRPC(nil), c.calls...)
}

func (c *recordingConn) reset() {
	c.mu.Lock()
	c.calls = nil
	c.mu.Unlock()
}

// operatorHarness is the parental harness with every module the operator
// routes reach: gRPC clients on a recordingConn, HTTP modules on the fake
// upstream, and a temp live TV store. Every principal has an unrestricted
// configured policy, so only the role gate can stop a request.
type operatorHarness struct {
	*parentalHarness
	rpc        *recordingConn
	livetvPath string
}

func newOperatorHarness(t *testing.T) *operatorHarness {
	t.Helper()
	h := &operatorHarness{parentalHarness: newParentalHarness(t), rpc: &recordingConn{}}
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
	s := h.s
	s.movies = mgmntv1.NewMovieManagementServiceClient(h.rpc)
	s.tv = tvmgmtv1.NewTvManagementServiceClient(h.rpc)
	s.music = musicv1.NewMusicManagementServiceClient(h.rpc)
	s.automation = automationv1.NewAutomationServiceClient(h.rpc)
	s.formats = formatsv1.NewFormatServiceClient(h.rpc)
	s.rename = renamev1.NewRenameServiceClient(h.rpc)
	s.scanner = scannerv1.NewScannerServiceClient(h.rpc)
	s.subtitles = subtv1.NewSubtitleServiceClient(h.rpc)
	h.livetvPath = filepath.Join(t.TempDir(), "livetv.json")
	s.livetv = newLiveTVStore(h.livetvPath, "")
	return h
}

// moduleReached reports whether anything left the BFF: an RPC, an HTTP
// module request, or a live TV timer write.
func (h *operatorHarness) moduleReached() bool {
	if len(h.rpc.snapshot()) > 0 || h.upHits.Load() > 0 {
		return true
	}
	_, err := os.Stat(h.livetvPath)
	return err == nil
}

func (h *operatorHarness) resetModules() {
	h.rpc.reset()
	h.upHits.Store(0)
	_ = os.Remove(h.livetvPath)
}

// operatorRequest builds a request for pattern with body; DELETE requests
// carry delete_files=1, the destructive variant the review exercised.
func operatorRequest(pattern, tok, body string) *http.Request {
	method, path, _ := strings.Cut(pattern, " ")
	path = strings.NewReplacer("{id}", "id1").Replace(path)
	if method == http.MethodDelete {
		path += "?delete_files=1"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	}
	return req
}

func assertOperatorError(t *testing.T, label string, res serveResult, status int, code string) {
	t.Helper()
	if res.panic != "" {
		t.Errorf("%s: handler reached and panicked: %s", label, res.panic)
		return
	}
	if res.status != status {
		t.Errorf("%s: status=%d want %d body=%s", label, res.status, status, res.body)
		return
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(res.body), &body); err != nil || body["code"] != code || len(body) != 2 {
		t.Errorf("%s: body=%s want only {error, code=%s}", label, res.body, code)
	}
}

func TestOperatorRoutesIndependentListIsComplete(t *testing.T) {
	if len(tM512OperatorRoutes) != 43 {
		t.Fatalf("T-M5-12 expectation list edited: %d routes, want 43", len(tM512OperatorRoutes))
	}
	admin := 0
	for p, want := range tM512OperatorRoutes {
		route, ok := parentalRouteClasses[p]
		if !ok {
			t.Errorf("%s: not in parentalRouteClasses", p)
			continue
		}
		if !route.requirePrivileged {
			t.Errorf("%s: route table does not require admin/manager", p)
		}
		if want.rootPathAdmin && !route.rootPathAdmin {
			t.Errorf("%s: root_folder_path no longer needs admin", p)
		}
		if want.rootPathAdmin {
			admin++
		}
	}
	if admin != 6 {
		t.Fatalf("root-path admin routes=%d want 6", admin)
	}
}

// Negative: user, viewer and other non-privileged sessions get 403
// operator.forbidden on every route, before the parental policy is consulted,
// and nothing reaches a module.
func TestOperatorRoutesRejectNonPrivilegedSessions(t *testing.T) {
	h := newOperatorHarness(t)
	members := map[string]string{
		"user":     h.sessionRoles("member", "", "bearer-member", "user"),
		"viewer":   h.sessionRoles("child", "", "bearer-child", "viewer"),
		"no roles": h.sessionRoles("plain", "", "bearer-plain"),
		"approver": h.sessionRoles("approver", "", "bearer-approver", "approver", "user"),
		// Lookalikes of a privileged role are not privileged.
		"lookalike": h.sessionRoles("look", "", "bearer-look", "administrator", "managers", "admin-ui"),
	}
	patterns := sortedKeys(tM512OperatorRoutes)
	for role, tok := range members {
		for _, p := range patterns {
			bodies := []string{tM512OperatorRoutes[p].body}
			if tM512OperatorRoutes[p].rootPathAdmin {
				bodies = append(bodies, `{"root_folder_path":"/etc"}`)
			}
			if p == "POST /api/formats/sync-trash" {
				bodies = append(bodies, `{"official":true}`, `{}`)
			}
			for _, body := range bodies {
				label := role + " " + p + " " + body
				assertOperatorError(t, label, serve(h.gated, operatorRequest(p, tok, body)), http.StatusForbidden, operatorCodeForbidden)
				if h.moduleReached() {
					t.Fatalf("%s: reached a module (rpc=%v http=%d)", label, h.rpc.snapshot(), h.upHits.Load())
				}
			}
		}
	}
	if n := h.provider.count(); n != 0 {
		t.Fatalf("role gate must run before the parental gate: provider calls=%d", n)
	}
}

// Positive: manager and admin pass the gate and the module is reached; a
// manager also reaches it on every root-path route when the body does not
// name root_folder_path.
func TestOperatorRoutesAdmitPrivilegedSessions(t *testing.T) {
	h := newOperatorHarness(t)
	for _, role := range []string{"manager", "admin", "Admin"} {
		tok := h.sessionRoles("op-"+strings.ToLower(role), "", "bearer-"+role, "user", role)
		for _, p := range sortedKeys(tM512OperatorRoutes) {
			h.resetModules()
			res := serve(h.gated, operatorRequest(p, tok, tM512OperatorRoutes[p].body))
			if res.panic != "" {
				t.Errorf("%s %s: panic %s", role, p, res.panic)
				continue
			}
			if strings.Contains(res.body, `"operator.`) || strings.Contains(res.body, `"parental.`) {
				t.Errorf("%s %s: gated: %d %s", role, p, res.status, res.body)
			}
			if !h.moduleReached() {
				t.Errorf("%s %s: module not reached: %d %s", role, p, res.status, res.body)
			}
		}
	}
}

// RULE-AUTH-3: root-folder changes are admin-only. A manager may toggle
// monitoring and the quality profile but not set root_folder_path in any
// spelling encoding/json accepts; admin may.
func TestOperatorRootFolderPathRequiresAdmin(t *testing.T) {
	h := newOperatorHarness(t)
	manager := h.sessionRoles("mgr", "", "bearer-mgr", "manager")
	admin := h.sessionRoles("adm", "", "bearer-adm", "admin")
	var rootPath []string
	for _, p := range sortedKeys(tM512OperatorRoutes) {
		if tM512OperatorRoutes[p].rootPathAdmin {
			rootPath = append(rootPath, p)
		}
	}
	denied := []string{
		`{"root_folder_path":"/etc"}`,
		`{"monitored":true,"root_folder_path":"/etc"}`,
		`{"root_folder_path":""}`,
		`{"Root_Folder_Path":"/etc"}`,
		`{"ROOT_FOLDER_PATH":"/etc"}`,
		`{"root_folder_path":null,"root_folder_path":"/etc"}`,
	}
	for _, p := range rootPath {
		for _, body := range denied {
			h.resetModules()
			assertOperatorError(t, "manager "+p+" "+body, serve(h.gated, operatorRequest(p, manager, body)), http.StatusForbidden, operatorCodeAdminRequired)
			if h.moduleReached() {
				t.Fatalf("manager %s %s reached a module", p, body)
			}
		}
		// The body read by the gate is restored for the handler.
		for _, body := range []string{`{"monitored":true}`, `{"monitored":false,"quality_profile_id":"q1"}`} {
			h.resetModules()
			res := serve(h.gated, operatorRequest(p, manager, body))
			if strings.Contains(res.body, `"operator.`) || !h.moduleReached() {
				t.Errorf("manager %s %s: %d %s", p, body, res.status, res.body)
			}
		}
		h.resetModules()
		res := serve(h.gated, operatorRequest(p, admin, `{"root_folder_path":"/data/library"}`))
		if strings.Contains(res.body, `"operator.`) || !h.moduleReached() {
			t.Errorf("admin %s: %d %s", p, res.status, res.body)
		}
	}
	// The forwarded value is the one the gate saw.
	h.resetModules()
	serve(h.gated, operatorRequest("PATCH /api/movies/{id}", manager, `{"monitored":true}`))
	calls := h.rpc.snapshot()
	if len(calls) == 0 {
		t.Fatal("manager PATCH movie: no RPC")
	}
	if req, ok := calls[0].args.(*mgmntv1.UpdateMovieRequest); !ok || !req.GetMonitored() || req.RootFolderPath != nil {
		t.Fatalf("manager PATCH movie forwarded %#v", calls[0].args)
	}
	h.resetModules()
	serve(h.gated, operatorRequest("PATCH /api/movies/{id}", admin, `{"root_folder_path":"/data/library"}`))
	if calls := h.rpc.snapshot(); len(calls) == 0 || calls[0].args.(*mgmntv1.UpdateMovieRequest).GetRootFolderPath() != "/data/library" {
		t.Fatalf("admin PATCH movie forwarded %v", calls)
	}
	// A manager body larger than the gate reads is refused, not truncated.
	h.resetModules()
	big := `{"monitored":true,"pad":"` + strings.Repeat("x", operatorRootPathMaxBody) + `"}`
	assertOperatorError(t, "oversized", serve(h.gated, operatorRequest("PATCH /api/movies/{id}", manager, big)), http.StatusRequestEntityTooLarge, operatorCodeBodyTooLarge)
	if h.moduleReached() {
		t.Fatal("oversized body reached a module")
	}
}

// The review's runtime cases, end to end through withAuth.
func TestOperatorReviewReproductionsAreClosed(t *testing.T) {
	h := newOperatorHarness(t)
	for _, role := range []string{"user", "viewer"} {
		tok := h.sessionRoles("r-"+role, "", "bearer-r-"+role, role)
		cases := []struct{ pattern, body string }{
			{"DELETE /api/movies/{id}", ""},
			{"PATCH /api/movies/{id}", `{"root_folder_path":"/etc"}`},
			{"POST /api/releases/grab", `{"download_url":"http://attacker.invalid/x.torrent","item_type":"movie","item_id":"m1"}`},
			{"POST /api/blocklist/clear", `{"clear_all":true}`},
			{"POST /api/sessions/{id}/stop", ""},
		}
		for _, c := range cases {
			assertOperatorError(t, role+" "+c.pattern, serve(h.gated, operatorRequest(c.pattern, tok, c.body)), http.StatusForbidden, operatorCodeForbidden)
		}
	}
	if h.moduleReached() {
		t.Fatalf("a member request reached a module (rpc=%v http=%d)", h.rpc.snapshot(), h.upHits.Load())
	}
	// A manager still stops a session through the playback monitor.
	mgr := h.sessionRoles("mgr", "", "bearer-mgr", "manager")
	res := serve(h.gated, operatorRequest("POST /api/sessions/{id}/stop", mgr, ""))
	if h.upHits.Load() != 1 || strings.Contains(res.body, `"operator.`) {
		t.Fatalf("manager stop: hits=%d %d %s", h.upHits.Load(), res.status, res.body)
	}
}

// Composition with ADR-0031: the role gate is outermost. A restricted member
// gets the role error without a policy lookup (no information about the
// policy or the item); a restricted manager still gets the parental gate of
// the route's class.
func TestOperatorGateComposesWithParentalGate(t *testing.T) {
	h := newOperatorHarness(t)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 2, restrictedPolicyJSON) })
	member := h.sessionRoles("kid", "", "bearer-kid", "user")
	for _, p := range sortedKeys(tM512OperatorRoutes) {
		assertOperatorError(t, p, serve(h.gated, operatorRequest(p, member, tM512OperatorRoutes[p].body)), http.StatusForbidden, operatorCodeForbidden)
	}
	if n := h.provider.count(); n != 0 {
		t.Fatalf("provider consulted %d times for a member", n)
	}
	manager := h.sessionRoles("kid-mgr", "", "bearer-kid-mgr", "manager")
	for _, p := range sortedKeys(tM512OperatorRoutes) {
		if parentalRouteClasses[p].class != classDeny {
			continue
		}
		res := serve(h.gated, operatorRequest(p, manager, tM512OperatorRoutes[p].body))
		assertParentalError(t, p, res, http.StatusForbidden, parentalCodeRestrictedRoute)
	}
	if h.moduleReached() {
		t.Fatal("restricted principals reached a module")
	}
}

// Without a session the role gate refuses too (auth disabled in dev), like
// the handler-gated operator routes.
func TestOperatorRoutesRefuseWithoutSession(t *testing.T) {
	h := newOperatorHarness(t)
	h.s.requireAuth = false
	mux := http.NewServeMux()
	h.s.registerRoutes(mux)
	for _, p := range sortedKeys(tM512OperatorRoutes) {
		assertOperatorError(t, p, serve(mux, operatorRequest(p, "", tM512OperatorRoutes[p].body)), http.StatusForbidden, operatorCodeForbidden)
	}
	if h.moduleReached() {
		t.Fatal("anonymous request reached a module")
	}
}

var operatorDocRow = regexp.MustCompile("^\\|\\s*`([A-Z]+ /[^`]*)`\\s*\\|\\s*(admin/manager|admin/manager; admin for `root_folder_path`)\\s*\\|")

// TestOperatorRoutesMatchBFFAPIDoc keeps the BFF-API.md "Operator route
// roles" table equal to the requirePrivileged rows of parentalRouteClasses.
func TestOperatorRoutesMatchBFFAPIDoc(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "BFF-API.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	docs := map[string]string{}
	in := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Operator route roles"
			continue
		}
		if m := operatorDocRow.FindStringSubmatch(line); in && m != nil {
			if _, dup := docs[m[1]]; dup {
				t.Errorf("BFF-API.md documents %q twice", m[1])
			}
			docs[m[1]] = m[2]
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for p, route := range parentalRouteClasses {
		if !route.requirePrivileged {
			continue
		}
		role := "admin/manager"
		if route.rootPathAdmin {
			role = "admin/manager; admin for `root_folder_path`"
		}
		want[normalizeRoute(p)] = role
	}
	if len(want) != len(tM512OperatorRoutes) {
		t.Errorf("table gates %d routes, independent list has %d", len(want), len(tM512OperatorRoutes))
	}
	for r, role := range want {
		if docs[r] != role {
			t.Errorf("BFF-API.md role for %s = %q, want %q", r, docs[r], role)
		}
	}
	for r := range docs {
		if _, ok := want[r]; !ok {
			t.Errorf("BFF-API.md lists %s, which the table does not gate", r)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
