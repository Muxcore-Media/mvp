package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
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

// S5b's regression: policy absence must hide titles as well as playback.
func TestParentalBrowsePolicyRequired(t *testing.T) {
	h := newParentalHarness(t)
	h.provider.doc(func(u string) string { return unconfiguredDoc(u, "") })
	kid := h.session("kid", "", "bearer-kid")
	for _, p := range patternsOfClass(classList, classItem) {
		t.Run(p, func(t *testing.T) {
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, parentalCodeUnconfigured)
		})
	}
}

type classificationMovies struct {
	mgmntv1.MovieManagementServiceClient
	items        map[string]*mgmntv1.MovieItem
	calls        int
	requests     []*mgmntv1.ListMoviesRequest
	getErr       error
	listErr      error
	listOverride func(*mgmntv1.ListMoviesRequest) *mgmntv1.ListMoviesResponse
	collections  map[int32][]*mgmntv1.MovieItem
	update       *mgmntv1.MovieItem
	updates      int

	collectionLists, collectionReads, collectionPrefsReads int
}

func (f *classificationMovies) GetMovie(_ context.Context, r *mgmntv1.GetMovieRequest, _ ...grpc.CallOption) (*mgmntv1.GetMovieResponse, error) {
	f.calls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	if m, ok := f.items[r.GetMovieId()]; ok {
		return &mgmntv1.GetMovieResponse{Movie: m}, nil
	}
	return nil, status.Error(codes.NotFound, "not found")
}
func filterFixturePolicy(max string, unrated bool, blocked, allowed []string) parental.Policy {
	return parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{MaxRating: max, AllowUnrated: unrated, BlockedTags: blocked, AllowedTags: allowed}}
}
func (f *classificationMovies) ListMovies(_ context.Context, r *mgmntv1.ListMoviesRequest, _ ...grpc.CallOption) (*mgmntv1.ListMoviesResponse, error) {
	f.requests = append(f.requests, proto.Clone(r).(*mgmntv1.ListMoviesRequest))
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOverride != nil {
		return f.listOverride(r), nil
	}
	items := make([]*mgmntv1.MovieItem, 0)
	for _, m := range f.items {
		if q := r.GetClassificationFilter(); q.GetEnabled() && !parental.Evaluate(filterFixturePolicy(q.GetMaxRating(), q.GetAllowUnrated(), q.GetBlockedTags(), q.GetAllowedTags()), catalogClassification(m.GetContentRating(), m.GetContentRatingSource(), m.GetTagLabels())).Allowed {
			continue
		}
		items = append(items, m)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].GetId() < items[j].GetId() })
	page, size := r.GetPage(), r.GetPageSize()
	start := min(int((page-1)*size), len(items))
	end := min(start+int(size), len(items))
	return &mgmntv1.ListMoviesResponse{Movies: items[start:end], Total: int32(len(items)), Page: page, PageSize: size}, nil
}
func (f *classificationMovies) ListTags(context.Context, *mgmntv1.ListTagsRequest, ...grpc.CallOption) (*mgmntv1.ListTagsResponse, error) {
	return &mgmntv1.ListTagsResponse{}, nil
}
func (f *classificationMovies) ListCollections(context.Context, *mgmntv1.ListCollectionsRequest, ...grpc.CallOption) (*mgmntv1.ListCollectionsResponse, error) {
	f.collectionLists++
	var out []*mgmntv1.CollectionSummary
	for id, items := range f.collections {
		out = append(out, &mgmntv1.CollectionSummary{CollectionId: id, Name: fmt.Sprint("collection-", id), MovieCount: int32(len(items))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CollectionId < out[j].CollectionId })
	return &mgmntv1.ListCollectionsResponse{Collections: out}, f.listErr
}
func (f *classificationMovies) GetCollectionMovies(_ context.Context, r *mgmntv1.GetCollectionMoviesRequest, _ ...grpc.CallOption) (*mgmntv1.GetCollectionMoviesResponse, error) {
	f.collectionReads++
	return &mgmntv1.GetCollectionMoviesResponse{CollectionId: r.CollectionId, Name: fmt.Sprint("collection-", r.CollectionId), Movies: f.collections[r.CollectionId]}, f.listErr
}
func (f *classificationMovies) GetCollectionPrefs(context.Context, *mgmntv1.GetCollectionPrefsRequest, ...grpc.CallOption) (*mgmntv1.GetCollectionPrefsResponse, error) {
	f.collectionPrefsReads++
	return &mgmntv1.GetCollectionPrefsResponse{}, nil
}
func (f *classificationMovies) UpdateMovie(context.Context, *mgmntv1.UpdateMovieRequest, ...grpc.CallOption) (*mgmntv1.UpdateMovieResponse, error) {
	f.updates++
	return &mgmntv1.UpdateMovieResponse{Movie: f.update}, nil
}

type classificationTV struct {
	update  *tvmgmtv1.TVSeries
	updates int
	tvmgmtv1.TvManagementServiceClient
	series                        map[string]*tvmgmtv1.TVSeries
	episodes                      map[string]*tvmgmtv1.TVEpisode
	calls, episodeCalls           int
	getErr, errorEpisode, listErr error
	requests                      []*tvmgmtv1.ListTVShowsRequest
	listOverride                  func(*tvmgmtv1.ListTVShowsRequest) *tvmgmtv1.ListTVShowsResponse
}

func (f *classificationTV) GetTVShow(_ context.Context, r *tvmgmtv1.GetTVShowRequest, _ ...grpc.CallOption) (*tvmgmtv1.GetTVShowResponse, error) {
	f.calls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	if m, ok := f.series[r.SeriesId]; ok {
		return &tvmgmtv1.GetTVShowResponse{Series: m}, nil
	}
	return nil, status.Error(codes.NotFound, "not found")
}
func (f *classificationTV) GetEpisode(_ context.Context, r *tvmgmtv1.GetEpisodeRequest, _ ...grpc.CallOption) (*tvmgmtv1.GetEpisodeResponse, error) {
	f.episodeCalls++
	if f.errorEpisode != nil {
		return nil, f.errorEpisode
	}
	if ep, ok := f.episodes[r.EpisodeId]; ok {
		return &tvmgmtv1.GetEpisodeResponse{Episode: ep}, nil
	}
	return nil, status.Error(codes.NotFound, "not found")
}
func (f *classificationTV) ListTVShows(_ context.Context, r *tvmgmtv1.ListTVShowsRequest, _ ...grpc.CallOption) (*tvmgmtv1.ListTVShowsResponse, error) {
	f.requests = append(f.requests, proto.Clone(r).(*tvmgmtv1.ListTVShowsRequest))
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOverride != nil {
		return f.listOverride(r), nil
	}
	var items []*tvmgmtv1.TVSeries
	for _, m := range f.series {
		if q := r.GetClassificationFilter(); q.GetEnabled() && !parental.Evaluate(filterFixturePolicy(q.GetMaxRating(), q.GetAllowUnrated(), q.GetBlockedTags(), q.GetAllowedTags()), catalogClassification(m.ContentRating, m.ContentRatingSource, m.TagLabels)).Allowed {
			continue
		}
		items = append(items, m)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Id < items[j].Id })
	page, size := r.Page, r.PageSize
	start := min(int((page-1)*size), len(items))
	end := min(start+int(size), len(items))
	return &tvmgmtv1.ListTVShowsResponse{Series: items[start:end], Total: int32(len(items)), Page: page, PageSize: size}, nil
}
func classificationFixture(t *testing.T) (*parentalHarness, *classificationMovies, *classificationTV, string) {
	t.Helper()
	h := newParentalHarness(t)
	movies := &classificationMovies{items: map[string]*mgmntv1.MovieItem{}}
	tv := &classificationTV{series: map[string]*tvmgmtv1.TVSeries{}, episodes: map[string]*tvmgmtv1.TVEpisode{}}
	for _, id := range []string{"m1", "id1", "item1"} {
		movies.items[id] = &mgmntv1.MovieItem{Id: id, Title: "visible-" + id, ContentRating: "PG", ContentRatingSource: "operator"}
		tv.series[id] = &tvmgmtv1.TVSeries{Id: id, Name: "visible-" + id, ContentRating: "PG", ContentRatingSource: "operator"}
		tv.episodes[id] = &tvmgmtv1.TVEpisode{Id: id, SeriesId: "series1"}
	}
	tv.series["series1"] = &tvmgmtv1.TVSeries{Id: "series1", Name: "series-title", ContentRating: "PG", ContentRatingSource: "operator"}
	tv.episodes["e1"] = &tvmgmtv1.TVEpisode{Id: "e1", SeriesId: "series1"}
	h.s.movies = movies
	h.s.tv = tv
	h.s.parental.classifier = newCatalogClassifier(h.s, h.clock.Now)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, restrictedPolicyJSON) })
	return h, movies, tv, h.session("kid", "", "kid-bearer")
}
func classificationRequest(h *parentalHarness, tok, path string) serveResult {
	req := httptest.NewRequest("GET", path, nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return serve(h.gated, req)
}
func requireBody(t *testing.T, res serveResult) map[string]any {
	t.Helper()
	if res.status != 200 || res.panic != "" {
		t.Fatalf("response=%+v", res)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(res.body), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestParentalCatalogRouteDenials(t *testing.T) {
	for _, rating := range []string{"R", "", "15"} {
		t.Run("rating_"+rating, func(t *testing.T) {
			h, m, tv, kid := classificationFixture(t)
			for _, item := range m.items {
				item.ContentRating = rating
				item.Title = "blocked-movie-secret"
			}
			for _, item := range tv.series {
				item.ContentRating = rating
				item.Name = "blocked-series-secret"
			}
			for _, p := range patternsOfClass(classItem, classPlay) {
				t.Run(p, func(t *testing.T) {
					assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), 403, parentalCodeBlocked)
				})
			}
			for _, path := range []string{"/api/movies", "/api/tv", "/api/movies?library=homevideos"} {
				body := requireBody(t, classificationRequest(h, kid, path))
				if body["total"] != float64(0) || len(body["items"].([]any)) != 0 {
					t.Errorf("%s leaked list: %v", path, body)
				}
			}
			if h.upHits.Load() != 0 {
				t.Fatalf("blocked requests reached downstream=%d", h.upHits.Load())
			}
		})
	}
}

func TestParentalCatalogAllowsTrustedRoutes(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	// Exercise every gate with a marker handler; successful subtitle handlers
	// remain outside this fixture, which never probes the filesystem.
	for _, p := range patternsOfClass(classItem, classPlay) {
		if p == "GET /api/playback/subtitles/{id}" || p == "GET /api/playback/segments" || p == "GET /stream/hls/{key}/{file}" {
			continue
		}
		t.Run(p, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.Handle(p, h.s.parentalWrap(p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })))
			res := serve(mux, routeRequest(p, h.tokFor(p, kid)))
			if res.status != 204 {
				t.Fatalf("gate response=%+v", res)
			}
		})
	}
	for _, path := range []string{"/api/movies/m1", "/api/movies/m1/", "/api/tv/series1", "/api/tv/series1/", "/stream/movies/m1", "/stream/tv/e1"} {
		res := classificationRequest(h, kid, path)
		if res.status != 200 {
			t.Fatalf("%s: %+v", path, res)
		}
	}
	if m.calls == 0 || tv.calls == 0 || tv.episodeCalls == 0 {
		t.Fatalf("classification did not use catalog: movie=%d tv=%d ep=%d", m.calls, tv.calls, tv.episodeCalls)
	}
	for _, p := range []string{"GET /api/playback/subtitles/{id}", "GET /api/playback/segments"} {
		assertParentalError(t, p, serve(h.gated, routeRequest(p, kid)), 403, parentalCodeBlocked)
	}
}

func TestParentalCatalogFilterAndPagination(t *testing.T) {
	for _, tc := range []struct{ name, policy, max string }{
		{"kids default", restrictedPolicyJSON, "PG"},
		{"explicit maximum", strings.Replace(restrictedPolicyJSON, `"max_rating":""`, `"max_rating":"R"`, 1), "R"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m, tv, kid := classificationFixture(t)
			h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, tc.policy) })
			m.items["z-hidden"] = &mgmntv1.MovieItem{Id: "z-hidden", ContentRating: "NC-17", ContentRatingSource: "operator"}
			tv.series["z-hidden"] = &tvmgmtv1.TVSeries{Id: "z-hidden", ContentRating: "NC-17", ContentRatingSource: "operator"}
			for _, path := range []string{"/api/movies?page=2&page_size=1", "/api/tv?page=2&page_size=1"} {
				body := requireBody(t, classificationRequest(h, kid, path))
				want := 3.
				if strings.HasPrefix(path, "/api/tv") {
					want = 4
				}
				if body["total"] != want || body["page"] != 2. || len(body["items"].([]any)) != 1 {
					t.Fatalf("filtered pagination=%v", body)
				}
			}
			mf, tf := m.requests[0].ClassificationFilter, tv.requests[0].ClassificationFilter
			if !mf.GetEnabled() || mf.GetMaxRating() != tc.max || !mf.GetAllowUnrated() || !reflect.DeepEqual(mf.GetBlockedTags(), []string{"horror"}) || !tf.GetEnabled() || tf.GetMaxRating() != tc.max || !reflect.DeepEqual(tf.GetBlockedTags(), mf.GetBlockedTags()) {
				t.Fatalf("filters movies=%v tv=%v", mf, tf)
			}
			if m.calls != 0 || tv.calls != 0 || tv.episodeCalls != 0 {
				t.Fatal("list classification made N+1 item calls")
			}
		})
	}
}

func TestParentalCatalogInconsistentPageFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, rating, source string
		tags                 []string
		nilItem              bool
	}{
		{name: "rating above", rating: "R", source: "operator"},
		{name: "missing source", rating: "PG"},
		{name: "unknown token", rating: "12A", source: "operator"},
		{name: "blocked tag", rating: "PG", source: "operator", tags: []string{"horror"}},
		{name: "nil row", nilItem: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m, tv, kid := classificationFixture(t)
			badMovie := &mgmntv1.MovieItem{Id: "secret", Title: "blocked-title", ContentRating: tc.rating, ContentRatingSource: tc.source, TagLabels: tc.tags}
			badSeries := &tvmgmtv1.TVSeries{Id: "secret", Name: "blocked-title", ContentRating: tc.rating, ContentRatingSource: tc.source, TagLabels: tc.tags}
			if tc.nilItem {
				badMovie = nil
				badSeries = nil
			}
			m.listOverride = func(r *mgmntv1.ListMoviesRequest) *mgmntv1.ListMoviesResponse {
				return &mgmntv1.ListMoviesResponse{Movies: []*mgmntv1.MovieItem{m.items["m1"], badMovie}, Total: 8123, Page: r.Page, PageSize: r.PageSize}
			}
			tv.listOverride = func(r *tvmgmtv1.ListTVShowsRequest) *tvmgmtv1.ListTVShowsResponse {
				return &tvmgmtv1.ListTVShowsResponse{Series: []*tvmgmtv1.TVSeries{tv.series["series1"], badSeries}, Total: 8123, Page: r.Page, PageSize: r.PageSize}
			}
			for _, path := range []string{"/api/movies?page=2", "/api/tv?page=2"} {
				assertParentalError(t, path, classificationRequest(h, kid, path), 503, parentalCodeClassificationUnavailable)
			}
		})
	}
}

func TestParentalCatalogClassificationStates(t *testing.T) {
	for _, tc := range []struct {
		name, rating, source, policy string
		tags                         []string
		status                       int
	}{
		{name: "kids PG", rating: "PG", source: "operator", status: 200},
		{name: "kids PG13", rating: "PG-13", source: "operator", status: 403},
		{name: "explicit NR", rating: "NR", source: "operator", status: 200},
		{name: "unrated disabled", rating: "NR", source: "operator", policy: strings.Replace(restrictedPolicyJSON, `"allow_unrated":true`, `"allow_unrated":false`, 1), status: 403},
		{name: "unavailable despite allow", source: "operator", status: 403},
		{name: "unknown token", rating: "12A", source: "operator", status: 403},
		{name: "untrusted source", rating: "PG", source: "tmdb", status: 403},
		{name: "missing source", rating: "PG", status: 403},
		{name: "exact tags", rating: "PG", source: "operator", tags: []string{"horrory"}, status: 200},
		{name: "normalized blocked", rating: "PG", source: "operator", tags: []string{" HORROR "}, status: 403},
		{name: "allowed missing", rating: "PG", source: "operator", policy: strings.Replace(restrictedPolicyJSON, `"allowed_tags":[]`, `"allowed_tags":["family"]`, 1), status: 403},
		{name: "blocked wins", rating: "PG", source: "operator", tags: []string{"family", "horror"}, policy: strings.Replace(restrictedPolicyJSON, `"allowed_tags":[]`, `"allowed_tags":["family"]`, 1), status: 403},
		{name: "allowed cannot unlock rating", rating: "R", source: "operator", tags: []string{"family"}, policy: strings.Replace(restrictedPolicyJSON, `"allowed_tags":[]`, `"allowed_tags":["family"]`, 1), status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m, tv, kid := classificationFixture(t)
			p := tc.policy
			if p == "" {
				p = restrictedPolicyJSON
			}
			h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, p) })
			m.items["m1"].ContentRating = tc.rating
			m.items["m1"].ContentRatingSource = tc.source
			m.items["m1"].TagLabels = tc.tags
			tv.series["series1"].ContentRating = tc.rating
			tv.series["series1"].ContentRatingSource = tc.source
			tv.series["series1"].TagLabels = tc.tags
			for _, path := range []string{"/api/movies/m1", "/api/tv/series1", "/stream/movies/m1", "/stream/tv/e1"} {
				res := classificationRequest(h, kid, path)
				if tc.status == 403 {
					assertParentalError(t, path, res, 403, parentalCodeBlocked)
				} else if res.status != 200 {
					t.Errorf("%s response=%+v", path, res)
				}
			}
		})
	}
}

func TestParentalCatalogCacheFreshnessAndPolicy(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	for _, path := range []string{"/stream/movies/m1", "/stream/tv/e1", "/stream/movies/m1", "/stream/tv/e1"} {
		if res := classificationRequest(h, kid, path); res.status != 200 {
			t.Fatalf("%s=%+v", path, res)
		}
	}
	if m.calls != 1 || tv.calls != 1 || tv.episodeCalls != 1 {
		t.Fatalf("not cached movie=%d tv=%d episode=%d", m.calls, tv.calls, tv.episodeCalls)
	}
	m.items["m1"].ContentRating = "R"
	tv.series["series1"].ContentRating = "R"
	// Details are fresh even while playback has a valid cache entry.
	for _, path := range []string{"/api/movies/m1", "/api/tv/series1"} {
		assertParentalError(t, path, classificationRequest(h, kid, path), 403, parentalCodeBlocked)
	}
	h.clock.Advance(29 * time.Second)
	if res := classificationRequest(h, kid, "/stream/tv/e1"); res.status != 200 {
		t.Fatalf("premature expiration=%+v", res)
	}
	h.clock.Advance(time.Second)
	for _, path := range []string{"/stream/movies/m1", "/stream/tv/e1"} {
		assertParentalError(t, path, classificationRequest(h, kid, path), 403, parentalCodeBlocked)
	}
	if tv.episodeCalls != 2 {
		t.Fatalf("episode mapping did not expire: %d", tv.episodeCalls)
	}
	// A refreshed policy evaluates cached classification, not a cached allow.
	m.items["m1"].ContentRating = "PG"
	h.clock.Advance(30 * time.Second)
	if res := classificationRequest(h, kid, "/stream/movies/m1"); res.status != 200 {
		t.Fatalf("refresh=%+v", res)
	}
	h.provider.doc(func(u string) string {
		return configuredDoc(u, "", 2, strings.Replace(restrictedPolicyJSON, `"max_rating":""`, `"max_rating":"G"`, 1))
	})
	h.s.parental.mu.Lock()
	clear(h.s.parental.cache)
	h.s.parental.mu.Unlock()
	before := m.calls
	assertParentalError(t, "stricter policy", classificationRequest(h, kid, "/stream/movies/m1"), 403, parentalCodeBlocked)
	if m.calls != before {
		t.Fatal("policy test missed warm classification cache")
	}
}

func TestParentalCatalogLookupFailuresAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*classificationMovies, *classificationTV)
		path   string
		status int
	}{
		{"movie unavailable", func(m *classificationMovies, _ *classificationTV) {
			m.getErr = status.Error(codes.Unavailable, "private detail")
		}, "/stream/movies/m1", 503},
		{"movie legacy missing Unknown", func(m *classificationMovies, _ *classificationTV) {
			m.getErr = status.Error(codes.Unknown, "movie not found")
		}, "/stream/movies/m1", 503},
		{"movie detail missing", func(m *classificationMovies, _ *classificationTV) { delete(m.items, "m1") }, "/api/movies/m1", 403},
		{"series detail missing", func(_ *classificationMovies, tv *classificationTV) { delete(tv.series, "series1") }, "/api/tv/series1", 403},
		{"movie not found", func(m *classificationMovies, _ *classificationTV) { delete(m.items, "m1") }, "/stream/movies/m1", 403},
		{"movie nil", func(m *classificationMovies, _ *classificationTV) { m.items["m1"] = nil }, "/stream/movies/m1", 503},
		{"movie wrong id", func(m *classificationMovies, _ *classificationTV) { m.items["m1"].Id = "other" }, "/stream/movies/m1", 503},
		{"movie detail wrong id", func(m *classificationMovies, _ *classificationTV) { m.items["m1"].Id = "other" }, "/api/movies/m1", 503},
		{"episode unavailable", func(_ *classificationMovies, tv *classificationTV) {
			tv.errorEpisode = status.Error(codes.Unavailable, "private detail")
		}, "/stream/tv/e1", 503},
		{"episode unsupported RPC", func(_ *classificationMovies, tv *classificationTV) {
			tv.errorEpisode = status.Error(codes.Unimplemented, "unsupported")
		}, "/stream/tv/e1", 503},
		{"episode missing", func(_ *classificationMovies, tv *classificationTV) { delete(tv.episodes, "e1") }, "/stream/tv/e1", 403},
		{"episode wrong id", func(_ *classificationMovies, tv *classificationTV) { tv.episodes["e1"].Id = "other" }, "/stream/tv/e1", 503},
		{"episode orphan", func(_ *classificationMovies, tv *classificationTV) { tv.episodes["e1"].SeriesId = "" }, "/stream/tv/e1", 503},
		{"series unavailable", func(_ *classificationMovies, tv *classificationTV) {
			tv.getErr = status.Error(codes.Unavailable, "tag lookup failed")
		}, "/stream/tv/e1", 503},
		{"series wrong id", func(_ *classificationMovies, tv *classificationTV) { tv.series["series1"].Id = "other" }, "/stream/tv/e1", 503},
		{"series detail wrong id", func(_ *classificationMovies, tv *classificationTV) { tv.series["series1"].Id = "other" }, "/api/tv/series1", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m, tv, kid := classificationFixture(t)
			tc.mutate(m, tv)
			code := parentalCodeClassificationUnavailable
			if tc.status == 403 {
				code = parentalCodeBlocked
			}
			for range 2 {
				assertParentalError(t, tc.path, classificationRequest(h, kid, tc.path), tc.status, code)
			}
			if strings.Contains(tc.path, "/movies/") && m.calls != 2 {
				t.Fatal("failed classification cached")
			}
			if strings.HasPrefix(tc.path, "/stream/tv/") && tv.episodeCalls != 2 {
				t.Fatal("failed episode classification cached")
			}
		})
	}
}

func TestParentalCatalogCollectionsAndLibraries(t *testing.T) {
	h, m, _, kid := classificationFixture(t)
	allowed := m.items["m1"]
	allowed.Title = "family vacation"
	allowed.RootFolderPath = "/media/homevideos"
	hidden := &mgmntv1.MovieItem{Id: "hidden", Title: "secret-title", ContentRating: "R", ContentRatingSource: "operator"}
	m.collections = map[int32][]*mgmntv1.MovieItem{1: {allowed, hidden}, 2: {hidden}}
	body := requireBody(t, classificationRequest(h, kid, "/api/collections"))
	if body["total"] != 1. {
		t.Fatalf("collection summary=%v", body)
	}
	first := body["items"].([]any)[0].(map[string]any)
	if first["movie_count"] != 1. || strings.Contains(fmt.Sprint(body), "collection-2") {
		t.Fatalf("collection counts=%v", body)
	}
	body = requireBody(t, classificationRequest(h, kid, "/api/collections/1"))
	if body["total"] != 1. || strings.Contains(fmt.Sprint(body), "secret-title") {
		t.Fatalf("collection=%v", body)
	}
	body = requireBody(t, classificationRequest(h, kid, "/api/collections/2"))
	if body["total"] != 0. || body["name"] != nil || body["monitored"] != nil {
		t.Fatalf("hidden collection leaked metadata=%v", body)
	}
	m.items = map[string]*mgmntv1.MovieItem{}
	for i := 0; i < 1005; i++ {
		id := fmt.Sprintf("m%04d", i)
		m.items[id] = &mgmntv1.MovieItem{Id: id, RootFolderPath: "/media/homevideos", ContentRating: "PG", ContentRatingSource: "operator"}
	}
	m.items["hidden"] = hidden
	body = requireBody(t, classificationRequest(h, kid, "/api/movies?library=homevideos&page=11&page_size=100"))
	items := body["items"].([]any)
	if body["total"] != 1005. || len(items) != 5 || items[0].(map[string]any)["id"] != "m1000" {
		t.Fatalf("library page incomplete/unstable=%v", body)
	}
	if len(m.requests) != 11 {
		t.Fatalf("library scan=%d pages, want 11", len(m.requests))
	}
	for _, r := range m.requests {
		if !r.GetClassificationFilter().GetEnabled() {
			t.Fatal("library list omitted classification filter")
		}
	}
}

func TestParentalCatalogLibraryIncompleteFailsClosed(t *testing.T) {
	for _, mode := range []string{"over-bound", "short-page", "duplicate", "changed-total", "wrong-page", "nil"} {
		t.Run(mode, func(t *testing.T) {
			h, m, _, kid := classificationFixture(t)
			m.listOverride = func(r *mgmntv1.ListMoviesRequest) *mgmntv1.ListMoviesResponse {
				if mode == "nil" {
					return nil
				}
				out := &mgmntv1.ListMoviesResponse{Total: 101, Page: r.Page, PageSize: 100}
				for i := 0; i < 100; i++ {
					out.Movies = append(out.Movies, &mgmntv1.MovieItem{Id: fmt.Sprint(i), ContentRating: "PG", ContentRatingSource: "operator"})
				}
				switch mode {
				case "over-bound":
					out.Total = 100001
				case "short-page":
					out.Movies = nil
				case "duplicate":
					out.Movies[1] = out.Movies[0]
				case "changed-total":
					if r.Page == 2 {
						out.Total = 102
					}
				case "wrong-page":
					out.Page = 4
				}
				return out
			}
			assertParentalError(t, mode, classificationRequest(h, kid, "/api/movies?library=homevideos"), 503, parentalCodeClassificationUnavailable)
		})
	}
}

func TestParentalCatalogUnrestrictedMakesNoAddedLookups(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
	m.getErr = status.Error(codes.Unavailable, "must not classify")
	tv.getErr = m.getErr
	tv.errorEpisode = m.getErr
	for _, path := range []string{"/stream/movies/m1", "/stream/tv/e1", "/api/movies", "/api/tv"} {
		if res := classificationRequest(h, kid, path); res.status != 200 {
			t.Fatalf("%s=%+v", path, res)
		}
	}
	if m.calls != 0 || tv.calls != 0 || tv.episodeCalls != 0 {
		t.Fatal("unrestricted made added lookups")
	}
	if m.requests[0].ClassificationFilter != nil || tv.requests[0].ClassificationFilter != nil {
		t.Fatal("unrestricted requested narrowing filter")
	}
}

func TestParentalCatalogPatchPreflightAndReturnedFields(t *testing.T) {
	h, m, _, kid := classificationFixture(t)
	manager := h.tokFor("PATCH /api/movies/{id}", kid)
	patch := func(tok string) serveResult {
		req := httptest.NewRequest("PATCH", "/api/movies/m1", strings.NewReader(`{"monitored":true}`))
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		return serve(h.gated, req)
	}
	m.items["m1"].ContentRating = "R"
	assertParentalError(t, "PATCH /api/movies/{id}", patch(manager), 403, parentalCodeBlocked)
	if m.updates != 0 {
		t.Fatal("denied patch reached mutation")
	}
	m.items["m1"].ContentRating = "PG"
	m.update = &mgmntv1.MovieItem{Id: "m1", Title: "raced-hidden-title", ContentRating: "R", ContentRatingSource: "operator"}
	assertParentalError(t, "PATCH /api/movies/{id}", patch(manager), 403, parentalCodeBlocked)
	if m.updates != 1 {
		t.Fatal("allowed preflight missed mutation")
	}
	// Existing role gate remains outermost, including before policy resolution.
	before := h.provider.count()
	res := patch(kid)
	if res.status != 403 || !strings.Contains(res.body, "operator.forbidden") || h.provider.count() != before {
		t.Fatalf("role regression=%+v", res)
	}
}

func TestParentalCatalogIgnoresClientClassification(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	m.items["m1"].ContentRating = "R"
	tv.series["series1"].ContentRating = "R"
	for _, path := range []string{"/api/movies", "/api/tv", "/api/movies/m1", "/api/tv/series1", "/stream/movies/m1", "/stream/tv/e1"} {
		baseline := classificationRequest(h, kid, path)
		forged := httptest.NewRequest("GET", path+"?tags=family&parental_rating=G&content_rating=G&unrated=false&user_id=adult&tenant_id=other", nil)
		forged.AddCookie(&http.Cookie{Name: "session", Value: kid})
		forged.Header.Set("X-MuxCore-User-Id", "adult")
		forged.Header.Set("X-Tenant-ID", "other")
		got := serve(h.gated, forged)
		if got.status != baseline.status || got.body != baseline.body || got.panic != "" {
			t.Errorf("%s forged=%+v baseline=%+v", path, got, baseline)
		}
	}
}

func TestParentalCatalogCacheKeepsKindsSeparate(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	m.items["item1"].ContentRating = "PG"
	tv.series["series1"].ContentRating = "R"
	if got := classificationRequest(h, kid, "/stream/movies/item1"); got.status != 200 {
		t.Fatalf("movie=%+v", got)
	}
	assertParentalError(t, "episode same ID", classificationRequest(h, kid, "/stream/tv/item1"), 403, parentalCodeBlocked)
	if tv.episodeCalls != 1 {
		t.Fatal("episode identity reused movie classification")
	}
}

func TestParentalCatalogHLSBindingsUseClassification(t *testing.T) {
	h, _, tv, kid := classificationFixture(t)
	other := h.session("other", "", "other-bearer")
	index := classificationRequest(h, kid, "/stream/hls?src=%2Fstream%2Ftv%2Fe1")
	if index.status != 302 {
		t.Fatalf("index=%+v", index)
	}
	asset := "/stream/hls/" + testHLSKeyHex + "/index.m3u8"
	if got := classificationRequest(h, kid, asset); got.status != 200 {
		t.Fatalf("bound asset=%+v", got)
	}
	assertParentalError(t, "other session", classificationRequest(h, other, asset), 403, parentalCodeBlocked)
	tv.series["series1"].ContentRating = "R"
	h.clock.Advance(30 * time.Second)
	assertParentalError(t, "reclassified bound asset", classificationRequest(h, kid, asset), 403, parentalCodeBlocked)
}

func TestParentalCatalogMissingProvidersFailClosed(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	m.listErr = status.Error(codes.Unavailable, "private database detail")
	tv.listErr = m.listErr
	for _, path := range []string{"/api/movies", "/api/tv", "/api/collections", "/api/collections/1", "/api/movies?library=homevideos"} {
		assertParentalError(t, path, classificationRequest(h, kid, path), 503, parentalCodeClassificationUnavailable)
	}
	h.s.movies = nil
	h.s.tv = nil
	for _, path := range []string{"/api/movies", "/api/tv", "/api/movies/m1", "/api/tv/series1", "/api/collections", "/api/collections/1", "/api/movies?library=homevideos", "/stream/movies/m1", "/stream/tv/e1"} {
		assertParentalError(t, path, classificationRequest(h, kid, path), 503, parentalCodeClassificationUnavailable)
	}
}

func (f *classificationTV) UpdateTVShow(context.Context, *tvmgmtv1.UpdateTVShowRequest, ...grpc.CallOption) (*tvmgmtv1.UpdateTVShowResponse, error) {
	f.updates++
	return &tvmgmtv1.UpdateTVShowResponse{Series: f.update}, nil
}
func TestParentalCatalogTVPatchChecksBothVersions(t *testing.T) {
	h, _, tv, kid := classificationFixture(t)
	manager := h.tokFor("PATCH /api/tv/{id}", kid)
	patch := func() serveResult {
		req := httptest.NewRequest("PATCH", "/api/tv/series1", strings.NewReader(`{"monitored":true}`))
		req.AddCookie(&http.Cookie{Name: "session", Value: manager})
		return serve(h.gated, req)
	}
	tv.series["series1"].ContentRating = "R"
	assertParentalError(t, "PATCH /api/tv/{id}", patch(), 403, parentalCodeBlocked)
	if tv.updates != 0 {
		t.Fatal("blocked series mutated")
	}
	tv.series["series1"].ContentRating = "PG"
	tv.update = &tvmgmtv1.TVSeries{Id: "series1", Name: "raced-hidden-series", ContentRating: "R", ContentRatingSource: "operator"}
	assertParentalError(t, "PATCH /api/tv/{id}", patch(), 403, parentalCodeBlocked)
	if tv.updates != 1 {
		t.Fatal("allowed preflight missed mutation")
	}
}

func TestParentalCatalogAllowedTagsWithoutCeiling(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	policy := `{"version":1,"mode":"restricted","rules":{"kids_mode":false,"max_rating":"","blocked_tags":[" GoRe "],"allowed_tags":[" FaMiLy "," KIDS "],"allow_unrated":false}}`
	h.provider.doc(func(user string) string { return configuredDoc(user, "", 1, policy) })
	// A high-rated item with an allowed label is visible when there is no
	// ceiling. The other fixture items have no matching allowed tag.
	m.items["m1"].ContentRating = "NC-17"
	m.items["m1"].TagLabels = []string{" Family "}
	tv.series["series1"].ContentRating = "NC-17"
	tv.series["series1"].TagLabels = []string{" KIDS "}
	for _, tc := range []struct{ path, id string }{{"/api/movies", "m1"}, {"/api/tv", "series1"}} {
		body := requireBody(t, classificationRequest(h, kid, tc.path))
		items := body["items"].([]any)
		if body["total"] != float64(1) || len(items) != 1 || items[0].(map[string]any)["id"] != tc.id {
			t.Fatalf("%s: allowed-tag list=%v", tc.path, body)
		}
	}
	if len(m.requests) != 1 || len(tv.requests) != 1 {
		t.Fatalf("list calls: movie=%d tv=%d", len(m.requests), len(tv.requests))
	}
	mf, tf := m.requests[0].GetClassificationFilter(), tv.requests[0].GetClassificationFilter()
	for name, got := range map[string]struct {
		enabled          bool
		max              string
		unrated          bool
		blocked, allowed []string
	}{
		"movie": {mf.GetEnabled(), mf.GetMaxRating(), mf.GetAllowUnrated(), mf.GetBlockedTags(), mf.GetAllowedTags()},
		"tv":    {tf.GetEnabled(), tf.GetMaxRating(), tf.GetAllowUnrated(), tf.GetBlockedTags(), tf.GetAllowedTags()},
	} {
		if !got.enabled || got.max != "" || got.unrated || !reflect.DeepEqual(got.blocked, []string{"gore"}) || !reflect.DeepEqual(got.allowed, []string{"family", "kids"}) {
			t.Errorf("%s filter=%+v", name, got)
		}
	}
	if m.calls != 0 || tv.calls != 0 || tv.episodeCalls != 0 {
		t.Fatal("allowed-tag lists made item classification lookups")
	}
	for _, path := range []string{"/api/movies/m1", "/api/tv/series1", "/stream/movies/m1", "/stream/tv/e1"} {
		got := classificationRequest(h, kid, path)
		if got.panic != "" || got.status != http.StatusOK {
			t.Errorf("%s: allowed no-ceiling item=%+v", path, got)
		}
	}
	for _, path := range []string{"/api/movies/id1", "/api/tv/id1", "/stream/movies/id1"} {
		assertParentalError(t, path, classificationRequest(h, kid, path), http.StatusForbidden, parentalCodeBlocked)
	}
}

func TestParentalCatalogPerItemReadsBypassWarmPlaybackCache(t *testing.T) {
	h, m, tv, kid := classificationFixture(t)
	for _, path := range []string{"/stream/movies/m1", "/stream/tv/e1"} {
		got := classificationRequest(h, kid, path)
		if got.panic != "" || got.status != http.StatusOK {
			t.Fatalf("warm %s: %+v", path, got)
		}
	}
	movieCalls, seriesCalls, episodeCalls := m.calls, tv.calls, tv.episodeCalls
	upstreamHits := h.upHits.Load()
	m.items["m1"].ContentRating = "R"
	tv.series["series1"].ContentRating = "R"
	for _, path := range []string{"/api/movies/m1/tags", "/api/movies/m1/history", "/api/tv/series1/tags", "/api/tv/series1/history", "/api/episodes/e1/file"} {
		assertParentalError(t, path, classificationRequest(h, kid, path), http.StatusForbidden, parentalCodeBlocked)
	}
	if m.calls-movieCalls != 2 || tv.calls-seriesCalls != 3 || tv.episodeCalls-episodeCalls != 1 {
		t.Fatalf("fresh item reads: movies=%d series=%d episodes=%d", m.calls-movieCalls, tv.calls-seriesCalls, tv.episodeCalls-episodeCalls)
	}
	if h.upHits.Load() != upstreamHits {
		t.Fatal("denied item read reached downstream")
	}
	// No clock advance: the two playback entries are still warm. These checks
	// ensure the preceding denials came from fresh C-ITEM reads.
	for _, path := range []string{"/stream/movies/m1", "/stream/tv/e1"} {
		got := classificationRequest(h, kid, path)
		if got.panic != "" || got.status != http.StatusOK {
			t.Fatalf("warm playback %s: %+v", path, got)
		}
	}
	if m.calls != movieCalls+2 || tv.calls != seriesCalls+3 || tv.episodeCalls != episodeCalls+1 {
		t.Fatal("item reads invalidated or reused the playback cache")
	}
}

func TestParentalCatalogPopulatedUnrestrictedParity(t *testing.T) {
	h, m, tv, adult := classificationFixture(t)
	h.provider.doc(func(user string) string { return configuredDoc(user, "", 1, unrestrictedPolicyJSON) })
	m.items["m1"].ContentRating = "R"
	tv.series["series1"].ContentRating = "R"
	m.collections = map[int32][]*mgmntv1.MovieItem{1: {m.items["m1"], m.items["id1"]}, 2: {m.items["item1"]}}
	counts := func() [8]int {
		return [8]int{m.calls, len(m.requests), tv.calls, tv.episodeCalls, len(tv.requests), m.collectionLists, m.collectionReads, m.collectionPrefsReads}
	}
	delta := func(after, before [8]int) [8]int {
		for i := range after {
			after[i] -= before[i]
		}
		return after
	}
	for _, path := range []string{"/api/movies", "/api/tv", "/api/movies/m1", "/api/tv/series1", "/api/collections", "/api/collections/1", "/stream/movies/m1", "/stream/tv/e1"} {
		t.Run(path, func(t *testing.T) {
			before := counts()
			got := classificationRequest(h, adult, path)
			gatedCalls := delta(counts(), before)
			before = counts()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: "session", Value: adult})
			want := serve(h.baseline, req)
			baselineCalls := delta(counts(), before)
			if got.panic != "" || want.panic != "" || got.status != http.StatusOK || want.status != http.StatusOK {
				t.Fatalf("gated=%+v baseline=%+v", got, want)
			}
			if got.body != want.body {
				t.Errorf("gated body=%s baseline body=%s", got.body, want.body)
			}
			for _, key := range []string{"Content-Type", "Cache-Control"} {
				if got.header.Get(key) != want.header.Get(key) {
					t.Errorf("%s gated=%q baseline=%q", key, got.header.Get(key), want.header.Get(key))
				}
			}
			if gatedCalls != baselineCalls {
				t.Errorf("RPC deltas gated=%v baseline=%v", gatedCalls, baselineCalls)
			}
		})
	}
	for _, req := range m.requests {
		if req.ClassificationFilter != nil {
			t.Fatal("unrestricted movie list carried narrowing filter")
		}
	}
	for _, req := range tv.requests {
		if req.ClassificationFilter != nil {
			t.Fatal("unrestricted TV list carried narrowing filter")
		}
	}
}
