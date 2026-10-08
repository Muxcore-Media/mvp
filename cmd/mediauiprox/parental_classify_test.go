package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"github.com/Muxcore-Media/userdata-local/parental"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ADR-0031 §2/§3/§5/§6/§7/§9 acceptance for T-M4-01 S5b: classification wiring
// for C-LIST, C-ITEM and C-PLAY against fake media modules. The fakes behave
// like the v0.1.23 modules (classification fields on items, an honoured
// classification_filter with filtered totals, GetEpisode) and can be switched
// to misbehave (ignore the filter, drop the fields, fail).

// --- fake media modules ------------------------------------------------------

type fakeCatalog struct {
	mu          sync.Mutex
	movies      []*mgmntv1.MovieItem
	series      []*tvmgmtv1.TVSeries
	episodes    map[string]*tvmgmtv1.TVEpisode
	collections []*mgmntv1.CollectionSummary

	ignoreFilter bool // dishonest module: returns everything with the unfiltered total
	stripFields  bool // old module: no classification fields at all
	failMovies   error
	failSeries   error
	failEpisodes error

	calls       map[string]int
	movieLists  []*mgmntv1.ListMoviesRequest
	seriesLists []*tvmgmtv1.ListTVShowsRequest
}

func (c *fakeCatalog) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[name]
}

func (c *fakeCatalog) resetCalls() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = map[string]int{}
	c.movieLists, c.seriesLists = nil, nil
}

func (c *fakeCatalog) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		n += v
	}
	return n
}

func (c *fakeCatalog) hit(name string) {
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[name]++
}

func movieOf(id, rating, source string, collection int32, tags ...string) *mgmntv1.MovieItem {
	m := &mgmntv1.MovieItem{Id: id, Title: "Title " + id, HasFile: true, ContentRating: rating, ContentRatingSource: source, TagLabels: tags}
	if collection != 0 {
		m.CollectionId = collection
		m.CollectionName = map[int32]string{7: "Family Pack", 9: "Hidden Saga"}[collection]
	}
	return m
}

func seriesOf(id, rating string, tags ...string) *tvmgmtv1.TVSeries {
	src := ""
	if rating != "" {
		src = ratingSourceOperator
	}
	return &tvmgmtv1.TVSeries{Id: id, Name: "Show " + id, ContentRating: rating, ContentRatingSource: src, TagLabels: tags}
}

func newFakeCatalog() *fakeCatalog {
	c := &fakeCatalog{calls: map[string]int{}}
	op := ratingSourceOperator
	c.movies = []*mgmntv1.MovieItem{
		movieOf("m-g", "G", op, 0),
		movieOf("m-pg", "PG", op, 7, "Family"),
		movieOf("m-pg13", "PG-13", op, 0),
		movieOf("m-r", "R", op, 7),
		movieOf("m-nc17", "NC-17", op, 9),
		movieOf("m-nr", "NR", op, 0),
		movieOf("m-none", "", "", 0),
		movieOf("m-15", "15", op, 0),    // a real-world token that is not on the ladder
		movieOf("m-forged", "G", "", 0), // a rating with no trusted source
		movieOf("m-horror", "PG", op, 0, "Horror"),
		movieOf("m-gorey", "PG", op, 0, "gorey"),
		movieOf("m-gore", "PG", op, 0, "gore"),
		movieOf("m-r-family", "R", op, 0, "Family"),
		movieOf("dup", "G", op, 0),
	}
	c.series = []*tvmgmtv1.TVSeries{
		seriesOf("s-ok", "TV-PG", "kids"),
		seriesOf("s-bad", "TV-MA"),
		seriesOf("s-none", ""),
		seriesOf("s-nr", "NR"),
	}
	c.episodes = map[string]*tvmgmtv1.TVEpisode{
		"e-ok":       {Id: "e-ok", SeriesId: "s-ok"},
		"e-bad":      {Id: "e-bad", SeriesId: "s-bad"},
		"e-none":     {Id: "e-none", SeriesId: "s-none"},
		"e-orphan":   {Id: "e-orphan", SeriesId: ""},
		"e-ghost":    {Id: "e-ghost", SeriesId: "s-missing"},
		"e-mismatch": {Id: "someone-else", SeriesId: "s-ok"},
		"dup":        {Id: "dup", SeriesId: "s-ok"},
	}
	c.collections = []*mgmntv1.CollectionSummary{
		{CollectionId: 7, Name: "Family Pack", MovieCount: 2, Monitored: true},
		{CollectionId: 9, Name: "Hidden Saga", MovieCount: 1},
	}
	return c
}

type filterSpec struct {
	enabled, unrated bool
	max              string
	blocked, allowed []string
}

func (f filterSpec) visible(c parental.Classification) bool {
	if !f.enabled {
		return true
	}
	return parental.Evaluate(parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{
		MaxRating: f.max, AllowUnrated: f.unrated, BlockedTags: f.blocked, AllowedTags: f.allowed,
	}}, c).Allowed
}

func page(n int, p, size int32) (lo, hi int) {
	if p < 1 {
		p = 1
	}
	if size < 1 {
		size = 20
	}
	lo = int(p-1) * int(size)
	if lo > n {
		lo = n
	}
	hi = lo + int(size)
	if hi > n {
		hi = n
	}
	return lo, hi
}

type fakeMovies struct {
	mgmntv1.MovieManagementServiceClient
	c *fakeCatalog
}

func (f *fakeMovies) clone(m *mgmntv1.MovieItem) *mgmntv1.MovieItem {
	out := proto.Clone(m).(*mgmntv1.MovieItem)
	if f.c.stripFields {
		out.ContentRating, out.ContentRatingSource, out.TagLabels = "", "", nil
	}
	return out
}

func (f *fakeMovies) ListMovies(_ context.Context, in *mgmntv1.ListMoviesRequest, _ ...grpc.CallOption) (*mgmntv1.ListMoviesResponse, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.hit("ListMovies")
	f.c.movieLists = append(f.c.movieLists, proto.Clone(in).(*mgmntv1.ListMoviesRequest))
	if f.c.failMovies != nil {
		return nil, f.c.failMovies
	}
	cf := in.GetClassificationFilter()
	spec := filterSpec{enabled: cf.GetEnabled() && !f.c.ignoreFilter, max: cf.GetMaxRating(), unrated: cf.GetAllowUnrated(), blocked: cf.GetBlockedTags(), allowed: cf.GetAllowedTags()}
	var vis []*mgmntv1.MovieItem
	for _, m := range f.c.movies {
		if spec.visible(movieClassification(m)) {
			vis = append(vis, m)
		}
	}
	lo, hi := page(len(vis), in.GetPage(), in.GetPageSize())
	out := &mgmntv1.ListMoviesResponse{Total: int32(len(vis)), Page: in.GetPage(), PageSize: in.GetPageSize()}
	for _, m := range vis[lo:hi] {
		out.Movies = append(out.Movies, f.clone(m))
	}
	return out, nil
}

func (f *fakeMovies) GetMovie(_ context.Context, in *mgmntv1.GetMovieRequest, _ ...grpc.CallOption) (*mgmntv1.GetMovieResponse, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.hit("GetMovie")
	if f.c.failMovies != nil {
		return nil, f.c.failMovies
	}
	for _, m := range f.c.movies {
		if m.GetId() == in.GetMovieId() {
			return &mgmntv1.GetMovieResponse{Movie: f.clone(m)}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "movie not found")
}

func (f *fakeMovies) ListCollections(_ context.Context, _ *mgmntv1.ListCollectionsRequest, _ ...grpc.CallOption) (*mgmntv1.ListCollectionsResponse, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.hit("ListCollections")
	return &mgmntv1.ListCollectionsResponse{Collections: f.c.collections}, nil
}

func (f *fakeMovies) GetCollectionMovies(_ context.Context, in *mgmntv1.GetCollectionMoviesRequest, _ ...grpc.CallOption) (*mgmntv1.GetCollectionMoviesResponse, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.hit("GetCollectionMovies")
	if f.c.failMovies != nil {
		return nil, f.c.failMovies
	}
	out := &mgmntv1.GetCollectionMoviesResponse{CollectionId: in.GetCollectionId()}
	for _, cs := range f.c.collections {
		if cs.GetCollectionId() == in.GetCollectionId() {
			out.Name = cs.GetName()
		}
	}
	for _, m := range f.c.movies {
		if m.GetCollectionId() == in.GetCollectionId() {
			out.Movies = append(out.Movies, f.clone(m))
		}
	}
	return out, nil
}

func (f *fakeMovies) ListTags(context.Context, *mgmntv1.ListTagsRequest, ...grpc.CallOption) (*mgmntv1.ListTagsResponse, error) {
	return &mgmntv1.ListTagsResponse{}, nil
}

func (f *fakeMovies) GetCollectionPrefs(context.Context, *mgmntv1.GetCollectionPrefsRequest, ...grpc.CallOption) (*mgmntv1.GetCollectionPrefsResponse, error) {
	return &mgmntv1.GetCollectionPrefsResponse{}, nil
}

type fakeTV struct {
	tvmgmtv1.TvManagementServiceClient
	c *fakeCatalog
}

func (f *fakeTV) clone(s *tvmgmtv1.TVSeries) *tvmgmtv1.TVSeries {
	out := proto.Clone(s).(*tvmgmtv1.TVSeries)
	if f.c.stripFields {
		out.ContentRating, out.ContentRatingSource, out.TagLabels = "", "", nil
	}
	return out
}

func (f *fakeTV) ListTVShows(_ context.Context, in *tvmgmtv1.ListTVShowsRequest, _ ...grpc.CallOption) (*tvmgmtv1.ListTVShowsResponse, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.hit("ListTVShows")
	f.c.seriesLists = append(f.c.seriesLists, proto.Clone(in).(*tvmgmtv1.ListTVShowsRequest))
	if f.c.failSeries != nil {
		return nil, f.c.failSeries
	}
	cf := in.GetClassificationFilter()
	spec := filterSpec{enabled: cf.GetEnabled() && !f.c.ignoreFilter, max: cf.GetMaxRating(), unrated: cf.GetAllowUnrated(), blocked: cf.GetBlockedTags(), allowed: cf.GetAllowedTags()}
	var vis []*tvmgmtv1.TVSeries
	for _, s := range f.c.series {
		if spec.visible(seriesClassification(s)) {
			vis = append(vis, s)
		}
	}
	lo, hi := page(len(vis), in.GetPage(), in.GetPageSize())
	out := &tvmgmtv1.ListTVShowsResponse{Total: int32(len(vis)), Page: in.GetPage(), PageSize: in.GetPageSize()}
	for _, s := range vis[lo:hi] {
		out.Series = append(out.Series, f.clone(s))
	}
	return out, nil
}

func (f *fakeTV) GetTVShow(_ context.Context, in *tvmgmtv1.GetTVShowRequest, _ ...grpc.CallOption) (*tvmgmtv1.GetTVShowResponse, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.hit("GetTVShow")
	if f.c.failSeries != nil {
		return nil, f.c.failSeries
	}
	for _, s := range f.c.series {
		if s.GetId() == in.GetSeriesId() {
			return &tvmgmtv1.GetTVShowResponse{Series: f.clone(s)}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "series not found")
}

func (f *fakeTV) GetEpisode(_ context.Context, in *tvmgmtv1.GetEpisodeRequest, _ ...grpc.CallOption) (*tvmgmtv1.GetEpisodeResponse, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.hit("GetEpisode")
	if f.c.failEpisodes != nil {
		return nil, f.c.failEpisodes
	}
	if ep, ok := f.c.episodes[in.GetEpisodeId()]; ok {
		return &tvmgmtv1.GetEpisodeResponse{Episode: proto.Clone(ep).(*tvmgmtv1.TVEpisode)}, nil
	}
	return nil, status.Error(codes.NotFound, "episode not found")
}

// --- harness ---------------------------------------------------------------

type s5b struct {
	*parentalHarness
	cat *fakeCatalog
	mu  sync.Mutex
	pol map[string]string
}

// newS5b wires the production classifier to fake media modules and serves
// per-user policies from the fake provider. A user with no entry is
// unconfigured.
func newS5b(t *testing.T) *s5b {
	t.Helper()
	h := &s5b{parentalHarness: newParentalHarness(t), cat: newFakeCatalog(), pol: map[string]string{}}
	h.s.movies = &fakeMovies{c: h.cat}
	h.s.tv = &fakeTV{c: h.cat}
	h.s.parental.classifier = newMediaClassifier(h.s)
	h.provider.doc(func(u string) string {
		h.mu.Lock()
		defer h.mu.Unlock()
		if p, ok := h.pol[u]; ok {
			return configuredDoc(u, "", 1, p)
		}
		return unconfiguredDoc(u, "")
	})
	h.setPolicy("adult", unrestrictedPolicyJSON)
	return h
}

func (h *s5b) setPolicy(user, policy string) {
	h.mu.Lock()
	h.pol[user] = policy
	h.mu.Unlock()
}

func policyJSON(kids bool, max string, unrated bool, blocked, allowed []string) string {
	b, _ := json.Marshal(map[string]any{"version": 1, "mode": "restricted", "rules": map[string]any{
		"kids_mode": kids, "max_rating": max, "blocked_tags": nonNil(blocked), "allowed_tags": nonNil(allowed), "allow_unrated": unrated,
	}})
	return string(b)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// kid signs in as "kid" under the given policy.
func (h *s5b) kid(policy string) string {
	h.setPolicy("kid", policy)
	h.clock.Advance(parentalPolicyTTL + time.Second) // never reuse an earlier policy
	return h.session("kid", "", "bearer-kid")
}

func (h *s5b) get(path, tok string) serveResult { return hlsGet(h.parentalHarness, path, tok) }

type listBody struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	Total int `json:"total"`
}

func (h *s5b) list(t *testing.T, path, tok string) ([]string, int) {
	t.Helper()
	res := h.get(path, tok)
	if res.status != http.StatusOK {
		t.Fatalf("%s: %d %s (panic %q)", path, res.status, res.body, res.panic)
	}
	var b listBody
	if err := json.Unmarshal([]byte(res.body), &b); err != nil {
		t.Fatalf("%s: %v: %s", path, err, res.body)
	}
	ids := make([]string, 0, len(b.Items))
	for _, it := range b.Items {
		ids = append(ids, it.ID)
	}
	return ids, b.Total
}

// listAll walks every page and returns the ids and the totals the pages report.
func (h *s5b) listAll(t *testing.T, base, tok string, size int) (ids []string, totals []int) {
	t.Helper()
	for p := 1; p < 50; p++ {
		got, total := h.list(t, fmt.Sprintf("%s?page=%d&page_size=%d", base, p, size), tok)
		ids = append(ids, got...)
		totals = append(totals, total)
		if len(got) == 0 || len(ids) >= total {
			return ids, totals
		}
	}
	t.Fatalf("%s: paging did not terminate", base)
	return nil, nil
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool { return strings.Join(sorted(a), ",") == strings.Join(sorted(b), ",") }

// gateDenied reports a parental gate answer (any parental.* or the resolve alias).
func gateDenied(res serveResult) bool {
	if res.panic != "" {
		return false
	}
	var b map[string]string
	if json.Unmarshal([]byte(res.body), &b) != nil {
		return false
	}
	return strings.HasPrefix(b["code"], "parental.") || b["code"] == playbackCodeParentalBlocked || strings.HasPrefix(b["parental_code"], "parental.")
}

var allMovieIDs = func() []string {
	var ids []string
	for _, m := range newFakeCatalog().movies {
		ids = append(ids, m.GetId())
	}
	return ids
}()

// --- C-PLAY ----------------------------------------------------------------

// playRoutes lists every C-PLAY route by the request that names an item.
func playRequests(kind, id string) map[string]string {
	src := "/stream/movies/" + id
	if kind == "episode" {
		src = "/stream/tv/" + id
	}
	q := url.QueryEscape(src)
	return map[string]string{
		"GET /api/playback/resolve":   "/api/playback/resolve?src=" + q,
		"GET /stream/transcode":       "/stream/transcode?src=" + q,
		"GET /stream/hls":             "/stream/hls?src=" + q,
		"GET /stream/trickplay":       "/stream/trickplay?src=" + q + "&duration=60",
		"GET /api/playback/subtitles": "/api/playback/subtitles?src=" + q,
		"GET /api/playback/chapters":  "/api/playback/chapters?src=" + q,
		"GET /api/playback/analysis":  "/api/playback/analysis?src=" + q,
		"GET /api/playback/segments":  "/api/playback/segments?media_id=" + url.QueryEscape(id),
		"direct stream":               src,
	}
}

// Required negative test 3 (play half) and 5: a blocked movie, an episode of a
// blocked series and every unavailable rating are refused on every C-PLAY route
// and never reach a module's stream; allowed items pass the gate.
func TestS5bPlayRoutesEnforceClassification(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil)) // kids mode: PG ceiling, no unrated
	cases := []struct {
		kind, id string
		allowed  bool
	}{
		{"movie", "m-g", true}, {"movie", "m-pg", true},
		{"movie", "m-pg13", false}, {"movie", "m-r", false}, {"movie", "m-nc17", false},
		{"movie", "m-nr", false},     // unrated, allow_unrated=false
		{"movie", "m-none", false},   // unavailable
		{"movie", "m-15", false},     // unknown token
		{"movie", "m-forged", false}, // rating without a trusted source
		{"movie", "m-missing", false},
		{"episode", "e-ok", true}, {"episode", "e-bad", false}, {"episode", "e-none", false},
		{"episode", "e-orphan", false},   // no series: ambiguous mapping
		{"episode", "e-ghost", false},    // series gone
		{"episode", "e-mismatch", false}, // module answered for another episode
		{"episode", "e-missing", false},
	}
	for _, c := range cases {
		for route, path := range playRequests(c.kind, c.id) {
			res := h.get(path, kid)
			if c.allowed {
				if gateDenied(res) {
					t.Errorf("%s %s/%s: allowed item refused: %d %s", route, c.kind, c.id, res.status, res.body)
				}
				continue
			}
			code := parentalCodeBlocked
			assertParentalError(t, route, res, http.StatusForbidden, code)
		}
	}
	// The blocked requests never reached a media stream, the transcoder or any
	// other upstream.
	hits := h.upHits.Load()
	for _, c := range cases {
		if c.allowed {
			continue
		}
		for _, path := range playRequests(c.kind, c.id) {
			h.get(path, kid)
		}
	}
	if h.upHits.Load() != hits {
		t.Fatalf("refused playback reached an upstream %d times", h.upHits.Load()-hits)
	}
}

// Ambiguous mapping fails closed: an id that is both a movie and an episode
// cannot be classified from the segments route's media_id, and a lookup that
// cannot be completed is 503, not "unrated".
func TestS5bAmbiguousAndFailedMappingsFailClosed(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(false, "R", true, nil, nil)) // permissive: only the mapping can deny

	assertParentalError(t, "GET /api/playback/segments", h.get("/api/playback/segments?media_id=dup", kid), http.StatusForbidden, parentalCodeBlocked)
	// Each owner alone is fine: m-g only as a movie, e-ok only as an episode.
	for _, id := range []string{"m-g", "e-ok"} {
		if res := h.get("/api/playback/segments?media_id="+id, kid); gateDenied(res) || res.status != http.StatusOK {
			t.Errorf("media_id=%s: %d %s", id, res.status, res.body)
		}
	}
	// The same id through its explicit kinds is not ambiguous.
	if res := h.get("/stream/movies/dup", kid); gateDenied(res) {
		t.Errorf("explicit movie dup: %s", res.body)
	}
	assertParentalError(t, "GET /api/playback/segments", h.get("/api/playback/segments?media_id=", kid), http.StatusForbidden, parentalCodeBlocked)
}

// Required negative tests 3, 5 and 6 across every surface: the same decision on
// the list, the detail and playback of one movie, for ladder, unrated,
// unavailable, unknown-token and exact-tag cases.
func TestS5bLadderAndTagsAgreeAcrossListDetailAndPlay(t *testing.T) {
	cases := []struct {
		name    string
		policy  string
		allowed []string
	}{
		{"kids mode without max denies PG-13, allows PG", policyJSON(true, "", false, nil, nil),
			[]string{"m-g", "m-pg", "m-horror", "m-gorey", "m-gore", "dup"}},
		{"explicit max overrides kids mode", policyJSON(true, "PG-13", false, nil, nil),
			[]string{"m-g", "m-pg", "m-pg13", "m-horror", "m-gorey", "m-gore", "dup"}},
		{"NR denied when allow_unrated is false", policyJSON(false, "R", false, nil, nil),
			[]string{"m-g", "m-pg", "m-pg13", "m-r", "m-horror", "m-gorey", "m-gore", "m-r-family", "dup"}},
		{"NR allowed when allow_unrated; unavailable, unknown token, forged source still denied", policyJSON(false, "R", true, nil, nil),
			[]string{"m-g", "m-pg", "m-pg13", "m-r", "m-nr", "m-horror", "m-gorey", "m-gore", "m-r-family", "dup"}},
		{"no ceiling allows NC-17", policyJSON(false, "", true, nil, nil),
			[]string{"m-g", "m-pg", "m-pg13", "m-r", "m-nc17", "m-nr", "m-horror", "m-gorey", "m-gore", "m-r-family", "dup"}},
		{"blocked tag matches exactly, case-folded, and not as a substring", policyJSON(false, "R", false, []string{"GORE", "horror"}, nil),
			[]string{"m-g", "m-pg", "m-pg13", "m-r", "m-gorey", "m-r-family", "dup"}},
		{"blocked tag beats an allowed tag", policyJSON(false, "R", false, []string{"family"}, []string{"family"}),
			[]string{}},
		{"a non-empty allowed set requires a match", policyJSON(false, "R", false, nil, []string{"family"}),
			[]string{"m-pg", "m-r-family"}},
		{"an allowed match never unlocks a rating denial", policyJSON(true, "", false, nil, []string{"family"}),
			[]string{"m-pg"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newS5b(t)
			kid := h.kid(c.policy)
			var viaDetail, viaPlay []string
			for _, id := range allMovieIDs {
				if res := h.get("/api/movies/"+id, kid); !gateDenied(res) {
					if res.status != http.StatusOK {
						t.Fatalf("detail %s: %d %s", id, res.status, res.body)
					}
					viaDetail = append(viaDetail, id)
				} else {
					assertParentalError(t, "/api/movies/", res, http.StatusForbidden, parentalCodeBlocked)
				}
				if res := h.get("/stream/movies/"+id, kid); !gateDenied(res) {
					viaPlay = append(viaPlay, id)
				}
			}
			viaList, totals := h.listAll(t, "/api/movies", kid, 4)
			if !sameSet(viaDetail, c.allowed) {
				t.Errorf("detail allowed %v want %v", sorted(viaDetail), sorted(c.allowed))
			}
			if !sameSet(viaPlay, c.allowed) {
				t.Errorf("play allowed %v want %v", sorted(viaPlay), sorted(c.allowed))
			}
			if !sameSet(viaList, c.allowed) {
				t.Errorf("list shows %v want %v", sorted(viaList), sorted(c.allowed))
			}
			for _, tot := range totals {
				if tot != len(c.allowed) {
					t.Errorf("total=%d want %d (hidden items must not be counted)", tot, len(c.allowed))
				}
			}
		})
	}
}

// Required negative test 3 (list/totals) and ADR §2.1: the restriction is sent
// to the module, pagination and total count visible items only, and the BFF
// re-checks what comes back.
func TestS5bMovieListNarrowsAtModuleWithCorrectPagination(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", true, []string{"horror"}, []string{"Family", "kids"}))
	ids, totals := h.listAll(t, "/api/movies", kid, 1)
	if !sameSet(ids, []string{"m-pg"}) {
		t.Fatalf("visible=%v", ids)
	}
	if len(totals) != 1 || totals[0] != 1 {
		t.Fatalf("totals=%v", totals)
	}
	h.cat.mu.Lock()
	req := h.cat.movieLists[0]
	h.cat.mu.Unlock()
	cf := req.GetClassificationFilter()
	if !cf.GetEnabled() || cf.GetMaxRating() != "PG" || !cf.GetAllowUnrated() ||
		strings.Join(cf.GetBlockedTags(), ",") != "horror" || strings.Join(cf.GetAllowedTags(), ",") != "family,kids" {
		t.Fatalf("filter sent to the module: %v", cf)
	}

	// Explicit max_rating wins over kids_mode.
	kid = h.kid(policyJSON(true, "R", true, nil, nil))
	h.cat.resetCalls()
	h.list(t, "/api/movies?page=1&page_size=2", kid)
	if got := h.cat.movieLists[0].GetClassificationFilter().GetMaxRating(); got != "R" {
		t.Fatalf("explicit max not sent: %q", got)
	}

	// 1 visible per page over 3 pages: page 2 and 3 are not empty and the total
	// never exceeds the visible count.
	kid = h.kid(policyJSON(true, "", false, nil, nil))
	visible := []string{"m-g", "m-pg", "m-horror", "m-gorey", "m-gore", "dup"}
	ids, totals = h.listAll(t, "/api/movies", kid, 2)
	if !sameSet(ids, visible) || len(ids) != len(visible) {
		t.Fatalf("paged visible=%v", ids)
	}
	for _, tot := range totals {
		if tot != len(visible) {
			t.Fatalf("total=%d want %d", tot, len(visible))
		}
	}
}

// The BFF re-evaluates every returned item: a module that ignores the filter,
// or an old module that returns no classification fields, cannot reveal titles.
func TestS5bListRecheckDropsWhatTheModuleLetThrough(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil))
	visible := []string{"m-g", "m-pg", "m-horror", "m-gorey", "m-gore", "dup"}

	h.cat.ignoreFilter = true
	ids, _ := h.list(t, "/api/movies?page=1&page_size=100", kid)
	if !sameSet(ids, visible) {
		t.Fatalf("dishonest module leaked: %v", ids)
	}
	h.cat.ignoreFilter = false

	h.cat.stripFields = true
	ids, _ = h.list(t, "/api/movies?page=1&page_size=100", kid)
	if len(ids) != 0 {
		t.Fatalf("module without classification fields leaked: %v", ids)
	}
	tvIDs, _ := h.list(t, "/api/tv?page=1&page_size=100", kid)
	if len(tvIDs) != 0 {
		t.Fatalf("tv module without classification fields leaked: %v", tvIDs)
	}
}

func TestS5bTVListNarrowsAndSeriesDetailIsChecked(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil))
	ids, totals := h.listAll(t, "/api/tv", kid, 1)
	if !sameSet(ids, []string{"s-ok"}) || totals[0] != 1 {
		t.Fatalf("tv list=%v totals=%v", ids, totals)
	}
	h.cat.mu.Lock()
	cf := h.cat.seriesLists[0].GetClassificationFilter()
	h.cat.mu.Unlock()
	if !cf.GetEnabled() || cf.GetMaxRating() != "PG" || cf.GetAllowUnrated() {
		t.Fatalf("tv filter: %v", cf)
	}
	if res := h.get("/api/tv/s-ok", kid); res.status != http.StatusOK {
		t.Errorf("allowed series detail: %d %s", res.status, res.body)
	}
	for _, id := range []string{"s-bad", "s-none", "s-nr", "s-missing"} {
		assertParentalError(t, "/api/tv/", h.get("/api/tv/"+id, kid), http.StatusForbidden, parentalCodeBlocked)
	}
	// allow_unrated lets the explicit NR series in, never the unavailable one.
	kid = h.kid(policyJSON(true, "", true, nil, nil))
	ids, _ = h.list(t, "/api/tv?page=1&page_size=10", kid)
	if !sameSet(ids, []string{"s-ok", "s-nr"}) {
		t.Fatalf("allow_unrated tv list=%v", ids)
	}
}

// Required negative test 3 (per-item reads): detail, tags, titles, history,
// artwork, files, subtitles and the episode file all deny a blocked movie,
// series and episode, and carry no item metadata.
func TestS5bPerItemReadsEnforceClassification(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil))
	routes := func(p string) []string {
		return []string{p + "/", p + "/tags", p + "/titles", p + "/history", p + "/artwork", p + "/subtitles", p + "/files"}
	}
	var blocked []string
	for _, id := range []string{"m-r", "m-none", "m-15", "m-nr", "m-missing"} {
		for _, r := range routes("/api/movies/" + id) {
			blocked = append(blocked, strings.TrimSuffix(r, "/"))
		}
	}
	for _, id := range []string{"s-bad", "s-none", "s-missing"} {
		for _, r := range routes("/api/tv/" + id) {
			blocked = append(blocked, strings.TrimSuffix(r, "/"))
		}
	}
	for _, id := range []string{"e-bad", "e-none", "e-orphan", "e-ghost", "e-mismatch", "e-missing"} {
		blocked = append(blocked, "/api/episodes/"+id+"/file")
	}
	hits := h.upHits.Load()
	for _, p := range blocked {
		res := h.get(p, kid)
		assertParentalError(t, p, res, http.StatusForbidden, parentalCodeBlocked)
		for _, leak := range []string{"Title ", "Show ", "m-r", "s-bad"} {
			if strings.Contains(res.body, leak) {
				t.Errorf("%s: blocked response carries item data %q: %s", p, leak, res.body)
			}
		}
	}
	if h.upHits.Load() != hits {
		t.Fatal("blocked per-item reads reached an upstream")
	}
	// An allowed item passes the gate on every per-item read.
	for _, p := range []string{"/api/movies/m-pg", "/api/movies/m-pg/tags", "/api/movies/m-pg/titles", "/api/movies/m-pg/history",
		"/api/movies/m-pg/artwork", "/api/movies/m-pg/subtitles", "/api/movies/m-pg/files",
		"/api/tv/s-ok", "/api/tv/s-ok/tags", "/api/tv/s-ok/titles", "/api/tv/s-ok/history", "/api/tv/s-ok/artwork", "/api/tv/s-ok/subtitles",
		"/api/episodes/e-ok/file"} {
		if res := h.get(p, kid); gateDenied(res) {
			t.Errorf("%s: allowed item refused: %d %s", p, res.status, res.body)
		}
	}
	// A path that does not name exactly one item is unavailable.
	for _, p := range []string{"/api/movies/m-pg/extra", "/api/movies/m-pg%2F..%2Fm-r", "/api/tv/s-ok/seasons/1"} {
		if res := h.get(p, kid); !gateDenied(res) {
			t.Errorf("%s: not an item path but passed the gate: %d %s", p, res.status, res.body)
		}
	}
}

// Collections: counts are derived from visible movies only, a collection with
// no visible movie neither appears nor can be read, and its name never leaks.
func TestS5bCollectionsDeriveSafeCountsAndLeakNothing(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil)) // m-pg (collection 7) visible; m-r and m-nc17 hidden

	res := h.get("/api/collections", kid)
	if res.status != http.StatusOK {
		t.Fatalf("list: %d %s", res.status, res.body)
	}
	var lb struct {
		Items []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Count int    `json:"movie_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(res.body), &lb); err != nil {
		t.Fatal(err)
	}
	if len(lb.Items) != 1 || lb.Items[0].ID != "7" || lb.Items[0].Name != "Family Pack" || lb.Items[0].Count != 1 || lb.Total != 1 {
		t.Fatalf("restricted collections=%s", res.body)
	}
	if strings.Contains(res.body, "Hidden Saga") {
		t.Fatalf("hidden collection name leaked: %s", res.body)
	}
	if h.cat.count("ListCollections") != 1 {
		// Only the monitored flag of an already visible collection is read from it.
		t.Fatalf("ListCollections calls=%d", h.cat.count("ListCollections"))
	}

	res = h.get("/api/collections/7", kid)
	if res.status != http.StatusOK || strings.Contains(res.body, "m-r") || !strings.Contains(res.body, "m-pg") {
		t.Fatalf("collection 7: %d %s", res.status, res.body)
	}
	var cb struct {
		Total  int              `json:"total"`
		Movies []map[string]any `json:"movies"`
	}
	_ = json.Unmarshal([]byte(res.body), &cb)
	if cb.Total != 1 || len(cb.Movies) != 1 {
		t.Fatalf("collection 7 shows hidden movies: %s", res.body)
	}

	// A fully hidden collection answers like an absent one.
	hidden, absent := h.get("/api/collections/9", kid), h.get("/api/collections/424242", kid)
	if hidden.status != http.StatusNotFound || hidden.body != absent.body || strings.Contains(hidden.body, "Hidden") {
		t.Fatalf("hidden=%d %s absent=%d %s", hidden.status, hidden.body, absent.status, absent.body)
	}
	if res := h.get("/api/collections/9/", kid); res.status != http.StatusNotFound {
		t.Fatalf("trailing slash form: %d %s", res.status, res.body)
	}

	// Unrestricted principals still get the module's own list.
	adult := h.session("adult", "", "bearer-adult")
	res = h.get("/api/collections", adult)
	if !strings.Contains(res.body, "Hidden Saga") || !strings.Contains(res.body, `"movie_count":2`) {
		t.Fatalf("unrestricted collections: %s", res.body)
	}
	if res = h.get("/api/collections/9", adult); res.status != http.StatusOK || !strings.Contains(res.body, "m-nc17") {
		t.Fatalf("unrestricted collection 9: %d %s", res.status, res.body)
	}
}

// The `?library=` view pages the same module data, so it is narrowed too.
func TestS5bLibraryViewIsNarrowed(t *testing.T) {
	h := newS5b(t)
	h.s.libraryPaths = newLibraryPathsStore("", t.TempDir())
	for _, m := range h.cat.movies {
		m.RootFolderPath = "/media/homevideos"
	}
	kid := h.kid(policyJSON(true, "", false, nil, nil))
	ids, total := h.list(t, "/api/movies?library=homevideos&page=1&page_size=100", kid)
	if !sameSet(ids, []string{"m-g", "m-pg", "m-horror", "m-gorey", "m-gore", "dup"}) || total != len(ids) {
		t.Fatalf("library view=%v total=%d", ids, total)
	}
	for _, r := range h.cat.movieLists {
		if !r.GetClassificationFilter().GetEnabled() {
			t.Fatalf("a library scan page was requested without the filter: %v", r)
		}
	}
	h.cat.ignoreFilter = true
	ids, _ = h.list(t, "/api/movies?library=homevideos&page=1&page_size=100", kid)
	if !sameSet(ids, []string{"m-g", "m-pg", "m-horror", "m-gorey", "m-gore", "dup"}) {
		t.Fatalf("library view leaked past a dishonest module: %v", ids)
	}
}

// Required negative test 2: legacy request fields, identity headers and the
// blob never move a decision on list, detail or playback.
func TestS5bIgnoresRequestFieldsOnClassifiedRoutes(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil))
	spoof := func(path string) serveResult {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		req := httptest.NewRequest(http.MethodGet, path+sep+"tags=none&parental_rating=G&unrated=1&user_id=adult&tenant_id=other&content_rating=G&content_rating_source=operator", nil)
		req.Header.Set(muxcoreUserIDHeader, "adult")
		req.Header.Set("X-Tenant-ID", "other")
		req.Header.Set("X-Caller-Id", "adult")
		req.AddCookie(&http.Cookie{Name: "session", Value: kid})
		return serve(h.gated, req)
	}
	for _, p := range []string{"/api/movies/m-r", "/api/movies/m-none", "/api/tv/s-bad", "/stream/movies/m-r", "/stream/tv/e-bad",
		"/api/playback/resolve?src=" + url.QueryEscape("/stream/movies/m-r"), "/api/episodes/e-bad/file"} {
		pattern := p
		if strings.HasPrefix(p, "/api/playback/resolve") {
			pattern = "GET /api/playback/resolve" // keeps the playback.parental_blocked alias
		}
		assertParentalError(t, pattern, spoof(p), http.StatusForbidden, parentalCodeBlocked)
	}
	ids, _ := h.list(t, "/api/movies?page=1&page_size=100", kid)
	spoofed := spoof("/api/movies?page=1&page_size=100")
	var b listBody
	_ = json.Unmarshal([]byte(spoofed.body), &b)
	var got []string
	for _, it := range b.Items {
		got = append(got, it.ID)
	}
	if !sameSet(got, ids) {
		t.Fatalf("spoofed list=%v plain=%v", got, ids)
	}
}

// Required negative test 9 and ADR §3: an unrestricted principal reproduces
// the ungated responses and costs no classification lookup.
func TestS5bUnrestrictedBypassesLookupsAndMatchesBaseline(t *testing.T) {
	h := newS5b(t)
	adult := h.session("adult", "", "bearer-adult")
	paths := []string{"/api/movies?page=1&page_size=100", "/api/tv?page=1&page_size=100", "/api/movies/m-r", "/api/tv/s-bad",
		"/api/collections", "/api/collections/9", "/stream/movies/m-r", "/stream/tv/e-bad",
		"/api/playback/resolve?src=" + url.QueryEscape("/stream/movies/m-r"), "/api/playback/segments?media_id=m-r"}
	for _, p := range paths {
		h.cat.resetCalls()
		got := h.get(p, adult)
		gotCalls := h.cat.total()
		h.cat.resetCalls()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: adult})
		want := serve(h.baseline, req)
		wantCalls := h.cat.total()
		if got.status != want.status || got.body != want.body || got.panic != want.panic {
			t.Errorf("%s: gated=%d %q baseline=%d %q", p, got.status, got.body, want.status, want.body)
		}
		if gotCalls != wantCalls {
			t.Errorf("%s: unrestricted gate made %d module calls, ungated handler %d", p, gotCalls, wantCalls)
		}
	}
	for _, r := range h.cat.movieLists {
		if r.GetClassificationFilter() != nil {
			t.Fatalf("unrestricted list was narrowed: %v", r)
		}
	}
	// Play routes consult no module at all.
	h.cat.resetCalls()
	h.get("/api/playback/resolve?src="+url.QueryEscape("/stream/tv/e-bad"), adult)
	h.get("/stream/tv/e-bad", adult)
	if h.cat.total() != 0 {
		t.Fatalf("unrestricted playback made %d classification lookups", h.cat.total())
	}
}

// Required negative test 7, 8 and ADR §3 for C-LIST/C-ITEM: policy failure
// fails the response, and a module failure is 503 classification_unavailable.
func TestS5bPolicyAndModuleFailuresFailClosed(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil))

	// Module failure on a list: no partial or empty-200 list.
	h.cat.failMovies = errors.New("movies down")
	h.cat.failSeries = errors.New("tv down")
	for _, p := range []string{"/api/movies", "/api/movies?library=homevideos", "/api/tv", "/api/collections", "/api/collections/7"} {
		assertParentalError(t, p, h.get(p, kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	}
	for _, p := range []string{"/api/movies/m-pg", "/api/movies/m-pg/tags", "/api/tv/s-ok", "/api/tv/s-ok/history", "/api/episodes/e-ok/file", "/stream/movies/m-pg", "/stream/tv/e-ok"} {
		assertParentalError(t, p, h.get(p, kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	}
	h.cat.failMovies, h.cat.failSeries = nil, nil
	h.cat.failEpisodes = errors.New("episode lookup down")
	assertParentalError(t, "/stream/tv/", h.get("/stream/tv/e-ok", kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	assertParentalError(t, "/api/playback/segments", h.get("/api/playback/segments?media_id=m-g", kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	h.cat.failEpisodes = nil

	// Policy failure: 503 / 403 / 401 on lists and detail, no module call made.
	h.cat.resetCalls()
	h.provider.set(http.StatusInternalServerError, `{}`)
	h.clock.Advance(parentalPolicyTTL + time.Second)
	for _, p := range []string{"/api/movies", "/api/tv", "/api/collections", "/api/movies/m-g", "/api/tv/s-ok"} {
		assertParentalError(t, p, h.get(p, kid), http.StatusServiceUnavailable, parentalCodeUnavailable)
	}
	h.provider.set(http.StatusUnauthorized, `{}`)
	assertParentalError(t, "/api/movies", h.get("/api/movies", kid), http.StatusUnauthorized, parentalCodeSessionInvalid)
	h.provider.doc(func(u string) string { return unconfiguredDoc(u, "") })
	assertParentalError(t, "/api/movies", h.get("/api/movies", kid), http.StatusForbidden, parentalCodeUnconfigured)
	qc, err := h.s.sessions.Create("qc", "qc")
	if err != nil {
		t.Fatal(err)
	}
	assertParentalError(t, "/api/tv", h.get("/api/tv", qc), http.StatusForbidden, parentalCodeUnverifiable)
	if h.cat.total() != 0 {
		t.Fatalf("policy failures reached the media modules %d times", h.cat.total())
	}
}

// Unavailable is not unrated: allow_unrated lets an explicit NR through and
// still denies an item with nothing recorded, an unknown token or a forged
// source (ADR-0031 §2.5/§2.6).
func TestS5bUnavailableIsNotUnratedEvenWithAllowUnrated(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(false, "", true, nil, nil))
	for id, want := range map[string]bool{"m-nr": true, "m-none": false, "m-15": false, "m-forged": false, "m-missing": false} {
		res := h.get("/api/movies/"+id, kid)
		if got := !gateDenied(res); got != want {
			t.Errorf("%s: allowed=%v want %v (%d %s)", id, got, want, res.status, res.body)
		}
	}
	for id, want := range map[string]bool{"s-nr": true, "s-none": false} {
		if got := !gateDenied(h.get("/api/tv/"+id, kid)); got != want {
			t.Errorf("series %s: allowed=%v want %v", id, got, want)
		}
	}
}

// classificationFromFields is the single place the module's fields become a
// Classification; it accepts only a trusted source and a ladder token.
func TestClassificationFromFields(t *testing.T) {
	cases := []struct {
		rating, source string
		want           parental.RatingState
		wantRating     string
	}{
		{"PG-13", "operator", parental.Rated, "PG-13"},
		{" tv-ma ", "operator", parental.Rated, "TV-MA"},
		{"G", "tmdb", parental.Rated, "G"},
		{"NR", "operator", parental.Unrated, ""},
		{"ur", "operator", parental.Unrated, ""},
		{"", "", parental.Unavailable, ""},
		{"", "operator", parental.Unavailable, ""},
		{"15", "operator", parental.Unavailable, ""},
		{"12A", "operator", parental.Unavailable, ""},
		{"PG", "", parental.Unavailable, ""},
		{"PG", "request", parental.Unavailable, ""},
		{"NR", "", parental.Unavailable, ""},
	}
	for _, c := range cases {
		got := classificationFromFields(c.rating, c.source, nil)
		if got.State != c.want || got.Rating != c.wantRating {
			t.Errorf("(%q,%q) = %+v want state %v rating %q", c.rating, c.source, got, c.want, c.wantRating)
		}
	}
}

// kidsCeilingToken must name what parental.Evaluate applies for kids_mode with
// no max_rating, or the module filter would disagree with the BFF re-check.
func TestKidsCeilingMatchesEvaluate(t *testing.T) {
	kids := parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{KidsMode: true}}
	explicit := parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{MaxRating: kidsCeilingToken}}
	for _, tok := range []string{"G", "TV-Y7", "PG", "TV-PG", "PG-13", "TV-14", "R", "TV-MA", "NC-17"} {
		c := parental.Classification{State: parental.Rated, Rating: tok, TagsKnown: true}
		if a, b := parental.Evaluate(kids, c).Allowed, parental.Evaluate(explicit, c).Allowed; a != b {
			t.Errorf("%s: kids_mode=%v max_rating=%s=%v", tok, a, kidsCeilingToken, b)
		}
	}
}

// A classification with an unknown tag lookup is denied when the policy has tag
// rules (ADR-0031 §2.6 "tag lookup failure").
func TestAuthorizeDeniesUnknownTagsWhenPolicyHasTagRules(t *testing.T) {
	g := newParentalGateWith("http://127.0.0.1:1", newParentalPolicyClient(time.Second), time.Now)
	g.classifier = &fakeClassifier{items: map[string]parental.Classification{
		"movie/x": {State: parental.Rated, Rating: "G", TagsKnown: false},
	}}
	withRules := parentalPolicy{configured: true, revision: 1, policy: parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{MaxRating: "R", BlockedTags: []string{"gore"}}}}
	if perr := g.authorizeItemFresh(context.Background(), withRules, parentalItem{Kind: kindMovie, ID: "x"}); perr != errParentalBlocked {
		t.Fatalf("unknown tags with tag rules: %v", perr)
	}
	noRules := parentalPolicy{configured: true, revision: 1, policy: parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{MaxRating: "R"}}}
	if perr := g.authorizeItemFresh(context.Background(), noRules, parentalItem{Kind: kindMovie, ID: "x"}); perr != nil {
		t.Fatalf("unknown tags without tag rules: %v", perr)
	}
}

// --- classification cache (ADR-0031 §4) -------------------------------------

// Only C-PLAY lookups are cached: for at most 30 s, keyed by kind and id, and a
// failure is never cached. C-ITEM and list/detail always ask the module.
func TestS5bPlaybackClassificationCache(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil))

	for i := 0; i < 3; i++ {
		h.get("/stream/movies/m-pg", kid)
	}
	if n := h.cat.count("GetMovie"); n != 1 {
		t.Fatalf("3 playback requests made %d GetMovie calls, want 1 (cached)", n)
	}

	// Within the TTL a change at the module is not seen; after it, it is.
	h.cat.mu.Lock()
	for _, m := range h.cat.movies {
		if m.GetId() == "m-pg" {
			m.ContentRating = "R"
		}
	}
	h.cat.mu.Unlock()
	h.clock.Advance(playbackClassificationTTL - time.Second)
	if res := h.get("/stream/movies/m-pg", kid); gateDenied(res) {
		t.Fatalf("a rating change was applied inside the cache window: %s", res.body)
	}
	h.clock.Advance(2 * time.Second)
	assertParentalError(t, "/stream/movies/", h.get("/stream/movies/m-pg", kid), http.StatusForbidden, parentalCodeBlocked)

	// Keyed by kind and id: the movie "dup" and the episode "dup" do not share a slot.
	h.cat.resetCalls()
	h.get("/stream/movies/dup", kid)
	h.get("/stream/tv/dup", kid)
	if h.cat.count("GetMovie") != 1 || h.cat.count("GetEpisode") != 1 {
		t.Fatalf("kinds shared a cache entry: movie=%d episode=%d", h.cat.count("GetMovie"), h.cat.count("GetEpisode"))
	}

	// Failures are never cached: one failed lookup does not poison the next.
	h.cat.failMovies = errors.New("blip")
	assertParentalError(t, "/stream/movies/", h.get("/stream/movies/m-g", kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	h.cat.failMovies = nil
	if res := h.get("/stream/movies/m-g", kid); gateDenied(res) {
		t.Fatalf("cached a lookup failure: %s", res.body)
	}
	// A missing item is not cached either: it is allowed as soon as it exists.
	assertParentalError(t, "/stream/movies/", h.get("/stream/movies/m-late", kid), http.StatusForbidden, parentalCodeBlocked)
	h.cat.mu.Lock()
	h.cat.movies = append(h.cat.movies, movieOf("m-late", "G", ratingSourceOperator, 0))
	h.cat.mu.Unlock()
	if res := h.get("/stream/movies/m-late", kid); gateDenied(res) {
		t.Fatalf("a not-found result was cached: %s", res.body)
	}

	// C-ITEM and detail never use the cache.
	h.cat.resetCalls()
	for i := 0; i < 3; i++ {
		h.get("/api/movies/m-g/tags", kid)
	}
	if n := h.cat.count("GetMovie"); n != 3 {
		t.Fatalf("3 C-ITEM requests made %d GetMovie calls, want 3 (never cached)", n)
	}
	h.cat.mu.Lock()
	for _, m := range h.cat.movies {
		if m.GetId() == "m-g" {
			m.ContentRating = "R"
		}
	}
	h.cat.mu.Unlock()
	assertParentalError(t, "/api/movies/m-g/tags", h.get("/api/movies/m-g/tags", kid), http.StatusForbidden, parentalCodeBlocked)
}

func TestCachingClassifierBoundsItsTable(t *testing.T) {
	clock := newFakeClock()
	inner := &fakeClassifier{items: map[string]parental.Classification{}}
	c := newCachingClassifier(inner, clock.Now)
	for i := 0; i < playbackClassificationMax+50; i++ {
		if _, err := c.Classify(context.Background(), parentalItem{Kind: kindMovie, ID: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.m) > playbackClassificationMax {
		t.Fatalf("cache grew to %d entries", len(c.m))
	}
	clock.Advance(playbackClassificationTTL + time.Second)
	if _, err := c.Classify(context.Background(), parentalItem{Kind: kindMovie, ID: "fresh"}); err != nil {
		t.Fatal(err)
	}
	if len(c.m) != 1 {
		t.Fatalf("expired entries were not reclaimed: %d", len(c.m))
	}
}

// A changed policy reaches list and detail within the 30 s policy bound.
func TestS5bPolicyChangeReachesListsWithinTTL(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", false, nil, nil))
	ids, _ := h.list(t, "/api/movies?page=1&page_size=100", kid)
	if len(ids) != 6 {
		t.Fatalf("restricted list=%v", ids)
	}
	h.setPolicy("kid", unrestrictedPolicyJSON)
	if ids2, _ := h.list(t, "/api/movies?page=1&page_size=100", kid); len(ids2) != len(ids) {
		t.Fatalf("policy change applied before the cache expired")
	}
	h.clock.Advance(parentalPolicyTTL + time.Second)
	if ids2, _ := h.list(t, "/api/movies?page=1&page_size=100", kid); len(ids2) != len(allMovieIDs) {
		t.Fatalf("unrestricted list=%d want %d", len(ids2), len(allMovieIDs))
	}
}

// Every route class that S5b wires is enforced for a restricted principal
// through the real route table: no C-LIST/C-ITEM/C-PLAY route can be reached
// with an unavailable classification.
func TestS5bEveryItemAndPlayRouteDeniesUnavailableClassification(t *testing.T) {
	h := newS5b(t)
	kid := h.kid(policyJSON(true, "", true, nil, nil))
	h.cat.stripFields = true // the module has nothing recorded for anything
	for _, p := range patternsOfClass(classItem, classPlay) {
		res := serve(h.gated, routeRequest(p, h.tokFor(p, kid)))
		if !gateDenied(res) {
			t.Errorf("%s: reached its handler with no classification: %d %s panic=%q", p, res.status, res.body, res.panic)
		}
	}
	for _, p := range patternsOfClass(classList) {
		res := serve(h.gated, routeRequest(p, h.tokFor(p, kid)))
		if res.panic != "" || res.status >= 500 {
			t.Errorf("%s: %d %s %s", p, res.status, res.body, res.panic)
		}
		if strings.Contains(res.body, "Title ") || strings.Contains(res.body, "Hidden Saga") || strings.Contains(res.body, "Family Pack") {
			t.Errorf("%s: list carries titles with no classification: %s", p, res.body)
		}
	}
}
