package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Independent-review fixes for T-M4-01 S5b (mvp PR 102): restricted
// collections from the real module contracts (F1), playback subtitle track
// binding (F2), fail-closed library tag pass (F3) and the owning-item mapping
// of item subtitles (F4).

// --- F1: collections --------------------------------------------------------

// The fake must keep the production shape: media-movies v0.1.23 ListMovies and
// GetMovie never fill collection_id/collection_name, only GetCollectionMovies
// does. A test double that fills them hid F1.
func TestS5bFakeMatchesProductionCollectionFields(t *testing.T) {
	h := newS5b(t)
	m := &fakeMovies{c: h.cat}
	resp, err := m.ListMovies(context.Background(), &mgmntv1.ListMoviesRequest{Page: 1, PageSize: 100})
	if err != nil || len(resp.GetMovies()) == 0 {
		t.Fatalf("ListMovies: %v %v", resp, err)
	}
	for _, mv := range resp.GetMovies() {
		if mv.GetCollectionId() != 0 || mv.GetCollectionName() != "" {
			t.Fatalf("ListMovies returned collection fields for %s", mv.GetId())
		}
	}
	one, _ := m.GetMovie(context.Background(), &mgmntv1.GetMovieRequest{MovieId: "m-pg"})
	if one.GetMovie().GetCollectionId() != 0 {
		t.Fatalf("GetMovie returned collection fields")
	}
	cm, _ := m.GetCollectionMovies(context.Background(), &mgmntv1.GetCollectionMoviesRequest{CollectionId: 7})
	if len(cm.GetMovies()) == 0 || cm.GetMovies()[0].GetCollectionId() != 7 {
		t.Fatalf("GetCollectionMovies lost the collection fields: %v", cm)
	}
}

type collectionsBody struct {
	Items []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Count     int    `json:"movie_count"`
		Monitored bool   `json:"monitored"`
	} `json:"items"`
	Total int `json:"total"`
}

func TestS5bRestrictedCollectionsUseOwnerContracts(t *testing.T) {
	h := newS5b(t)
	// The summary may name and size a collection after hidden movies; none of
	// that may reach a restricted principal.
	h.cat.collections[0].Name = "Family Pack + Hidden R Cut"
	h.cat.collections[0].MovieCount = 99
	kid := h.kid(policyJSON(true, "", false, nil, nil))
	h.cat.resetCalls()

	res := h.get("/api/collections", kid)
	if res.status != http.StatusOK {
		t.Fatalf("%d %s", res.status, res.body)
	}
	var b collectionsBody
	if err := json.Unmarshal([]byte(res.body), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 1 || b.Items[0].ID != "7" || b.Items[0].Name != "Family Pack" || b.Items[0].Count != 1 || !b.Items[0].Monitored || b.Total != 1 {
		t.Fatalf("visible collection missing or wrong: %s", res.body)
	}
	for _, leak := range []string{"Hidden", "Cut", "99", "m-r", "m-nc17"} {
		if strings.Contains(res.body, leak) {
			t.Fatalf("%q leaked: %s", leak, res.body)
		}
	}
	// Bounded: one ListCollections, one GetCollectionMovies per collection, and
	// no ListMovies pass (it cannot carry membership).
	if h.cat.count("ListCollections") != 1 || h.cat.count("GetCollectionMovies") != len(h.cat.collections) || h.cat.count("ListMovies") != 0 {
		t.Fatalf("calls=%v", h.cat.calls)
	}

	// A wider ceiling shows the second collection with its own filtered count.
	wide := h.kid(policyJSON(false, "NC-17", false, nil, nil))
	if err := json.Unmarshal([]byte(h.get("/api/collections", wide).body), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 2 || b.Items[0].Name != "Family Pack" || b.Items[0].Count != 2 || b.Items[1].Name != "Hidden Saga" || b.Items[1].Count != 1 {
		t.Fatalf("wide collections=%+v", b)
	}
	// A tag rule hides m-pg inside collection 7 and keeps the count honest.
	tagged := h.kid(policyJSON(false, "NC-17", false, []string{"family"}, nil))
	if err := json.Unmarshal([]byte(h.get("/api/collections", tagged).body), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 2 || b.Items[0].Count != 1 {
		t.Fatalf("tag-filtered collections=%+v", b)
	}
}

// Any failed or inconsistent required call fails the whole list: never a
// partial 200 with the collections that did answer.
func TestS5bRestrictedCollectionsFailClosed(t *testing.T) {
	big := make([]*mgmntv1.CollectionSummary, 0, maxRestrictedCollections+1)
	for i := 1; i <= maxRestrictedCollections+1; i++ {
		big = append(big, &mgmntv1.CollectionSummary{CollectionId: int32(1000 + i), Name: fmt.Sprintf("C%d", i)})
	}
	cases := map[string]func(c *fakeCatalog){
		"ListCollections fails":           func(c *fakeCatalog) { c.failCollections = status.Error(codes.Unavailable, "down") },
		"visible collection read fails":   func(c *fakeCatalog) { c.failCollectionID = 7 },
		"hidden collection read fails":    func(c *fakeCatalog) { c.failCollectionID = 9 },
		"module answers for another id":   func(c *fakeCatalog) { c.collectionAnswersID = 8 },
		"foreign movie in a collection":   func(c *fakeCatalog) { c.foreignMovie = movieOf("m-g", "G", ratingSourceOperator, 99) },
		"too many collections to list":    func(c *fakeCatalog) { c.collections = big },
		"collection reads fail entirely":  func(c *fakeCatalog) { c.failMovies = status.Error(codes.Unavailable, "down") },
		"deadline exceeded on collection": func(c *fakeCatalog) { c.failCollections = context.DeadlineExceeded },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newS5b(t)
			kid := h.kid(policyJSON(true, "", false, nil, nil))
			mutate(h.cat)
			res := h.get("/api/collections", kid)
			assertParentalError(t, "GET /api/collections", res, http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
			if strings.Contains(res.body, "Family") || strings.Contains(res.body, `"items"`) {
				t.Fatalf("partial content in a failed answer: %s", res.body)
			}
		})
	}
	// An unrestricted principal is not affected by the restricted path.
	h := newS5b(t)
	h.cat.failCollectionID = 7
	adult := h.session("adult", "", "bearer-adult")
	if res := h.get("/api/collections", adult); res.status != http.StatusOK {
		t.Fatalf("unrestricted: %d %s", res.status, res.body)
	}
}

// --- F3: library tag pass ---------------------------------------------------

func libraryHarness(t *testing.T) (*s5b, string) {
	t.Helper()
	h := newS5b(t)
	h.s.libraryPaths = newLibraryPathsStore("", t.TempDir())
	for _, m := range h.cat.movies {
		m.RootFolderPath = "/media/homevideos"
	}
	h.cat.tags = []*mgmntv1.Tag{{Id: "t-home", Label: "Home Videos"}}
	return h, h.kid(policyJSON(true, "", false, nil, nil))
}

func TestS5bLibraryTagPassFailureFailsClosed(t *testing.T) {
	const path = "/api/movies?library=homevideos&page=1&page_size=100"
	h, kid := libraryHarness(t)
	// The page pass would succeed on its own.
	if ids, _ := h.list(t, path, kid); len(ids) == 0 {
		t.Fatal("baseline library view is empty")
	}
	sawTagPass := false
	for _, r := range h.cat.movieLists {
		sawTagPass = sawTagPass || r.GetTagId() == "t-home"
	}
	if !sawTagPass {
		t.Fatal("the tag pass never ran, so this test would prove nothing")
	}

	h.cat.failTagPass = true
	res := h.get(path, kid)
	assertParentalError(t, "/api/movies", res, http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	if strings.Contains(res.body, "m-pg") {
		t.Fatalf("partial list on a failed tag pass: %s", res.body)
	}

	// The tag lookup is a required call as well.
	h.cat.failTagPass = false
	h.cat.failTags = status.Error(codes.Unavailable, "tags down")
	assertParentalError(t, "/api/movies", h.get(path, kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)

	// Unrestricted behavior is unchanged: the tag pass stays best-effort.
	adult := h.session("adult", "", "bearer-adult")
	h.cat.failTags = nil
	h.cat.failTagPass = true
	if res := h.get(path, adult); res.status != http.StatusOK || !strings.Contains(res.body, "m-nc17") {
		t.Fatalf("unrestricted with a failed tag pass: %d %s", res.status, res.body)
	}
	h.cat.failTagPass = false
	h.cat.failTags = status.Error(codes.Unavailable, "tags down")
	if res := h.get(path, adult); res.status != http.StatusOK {
		t.Fatalf("unrestricted with failed tags: %d %s", res.status, res.body)
	}
}

// --- F2 / F4: subtitles -----------------------------------------------------

type fakeSubs struct {
	subtv1.SubtitleServiceClient
	mu     sync.Mutex
	rows   map[string][]*subtv1.SubtitleFile      // media file id → rows
	media  map[string]*subtv1.SubtitleMediaItem   // media.id → item (GetMedia)
	series map[string][]*subtv1.SubtitleMediaItem // series id → items (ListMedia)
	listed []string                               // media file ids ListSubtitles was asked about
}

func (f *fakeSubs) ListSubtitles(_ context.Context, in *subtv1.ListSubtitlesRequest, _ ...grpc.CallOption) (*subtv1.ListSubtitlesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = append(f.listed, in.GetMediaFileId())
	return &subtv1.ListSubtitlesResponse{Subtitles: f.rows[in.GetMediaFileId()], Total: int32(len(f.rows[in.GetMediaFileId()]))}, nil
}

func (f *fakeSubs) GetMedia(_ context.Context, in *subtv1.GetMediaRequest, _ ...grpc.CallOption) (*subtv1.GetMediaResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it, ok := f.media[in.GetId()]; ok {
		return &subtv1.GetMediaResponse{Item: it}, nil
	}
	return nil, status.Error(codes.NotFound, "no such media")
}

func (f *fakeSubs) ListMedia(_ context.Context, in *subtv1.ListMediaRequest, _ ...grpc.CallOption) (*subtv1.ListMediaResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.series[in.GetSeriesId()]
	return &subtv1.ListMediaResponse{Items: items, Total: int32(len(items))}, nil
}

func (f *fakeSubs) askedAbout(file string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.listed {
		if id == file {
			return true
		}
	}
	return false
}

type subtitleFixture struct {
	*s5b
	subs *fakeSubs
	dir  string
	// files by video basename
	secret string
}

const (
	srtBody    = "1\n00:00:01,000 --> 00:00:02,000\n%s\n"
	secretBody = "TOPSECRET outside any library"
)

func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newSubtitleFixture gives m-pg (allowed for the kid) and m-r (denied) real
// video files with sidecars on disk and module-held subtitle rows, plus a file
// outside every library.
func newSubtitleFixture(t *testing.T) *subtitleFixture {
	t.Helper()
	f := &subtitleFixture{s5b: newS5b(t), dir: t.TempDir()}
	pg, r := filepath.Join(f.dir, "pg", "Movie PG.mkv"), filepath.Join(f.dir, "r", "Movie R.mkv")
	writeFixtureFile(t, pg, "video")
	writeFixtureFile(t, r, "video")
	writeFixtureFile(t, filepath.Join(f.dir, "pg", "Movie PG.en.srt"), fmt.Sprintf(srtBody, "Hello PG"))
	writeFixtureFile(t, filepath.Join(f.dir, "pg", "Movie PG.fr.vtt"), "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nBonjour PG\n")
	writeFixtureFile(t, filepath.Join(f.dir, "r", "Movie R.en.srt"), fmt.Sprintf(srtBody, "Hello R"))
	f.secret = filepath.Join(f.dir, "outside", "secret.srt")
	writeFixtureFile(t, f.secret, secretBody)
	f.cat.files = map[string]string{"m-pg": pg, "m-r": r}
	f.subs = &fakeSubs{
		rows: map[string][]*subtv1.SubtitleFile{
			"file-m-pg": {{Id: "sub-pg-1", MediaFileId: "file-m-pg", Language: "eng", Source: "opensubtitles"}},
			"file-m-r":  {{Id: "sub-r-1", MediaFileId: "file-m-r", Language: "eng", Source: "opensubtitles"}},
		},
		media:  map[string]*subtv1.SubtitleMediaItem{},
		series: map[string][]*subtv1.SubtitleMediaItem{},
	}
	f.s.subtitles = f.subs
	return f
}

type trackList struct {
	Tracks []playbackSubtitleTrack `json:"tracks"`
}

func (f *subtitleFixture) tracks(t *testing.T, src, tok string) []playbackSubtitleTrack {
	t.Helper()
	res := f.get("/api/playback/subtitles?src="+url.QueryEscape(src), tok)
	if res.status != http.StatusOK {
		t.Fatalf("list %s: %d %s", src, res.status, res.body)
	}
	var tl trackList
	if err := json.Unmarshal([]byte(res.body), &tl); err != nil {
		t.Fatal(err)
	}
	return tl.Tracks
}

func trackIDs(tracks []playbackSubtitleTrack) []string {
	var ids []string
	for _, tr := range tracks {
		ids = append(ids, tr.ID)
	}
	return ids
}

func assertBlocked(t *testing.T, what string, res serveResult) {
	t.Helper()
	assertParentalError(t, what, res, http.StatusForbidden, parentalCodeBlocked)
	if strings.Contains(res.body, "Hello") || strings.Contains(res.body, "TOPSECRET") || strings.Contains(res.body, "upstream") {
		t.Fatalf("%s: denied answer carries content: %s", what, res.body)
	}
}

func TestS5bSubtitleTracksAdvertisedToRestrictedAreFetchable(t *testing.T) {
	f := newSubtitleFixture(t)
	kid := f.kid(policyJSON(true, "", false, nil, nil))

	tracks := f.tracks(t, "/stream/movies/m-pg", kid)
	if len(tracks) != 3 {
		t.Fatalf("want 2 sidecar + 1 module track, got %+v", tracks)
	}
	var sidecars, module int
	for _, tr := range tracks {
		if strings.HasPrefix(tr.ID, "sc_") {
			sidecars++
		} else {
			module++
		}
		// Every advertised track fetches, in every format and path.
		res := f.get(tr.Src, kid)
		if res.status != http.StatusOK {
			t.Fatalf("advertised track %q is not fetchable: %d %s", tr.ID, res.status, res.body)
		}
	}
	if sidecars != 2 || module != 1 {
		t.Fatalf("sidecars=%d module=%d", sidecars, module)
	}
	byID := map[string]serveResult{}
	for _, tr := range tracks {
		byID[tr.ID] = f.get(tr.Src, kid)
	}
	if r := byID[sidecarTrackID(filepath.Join(f.dir, "pg", "Movie PG.en.srt"))]; !strings.Contains(r.body, "WEBVTT") || !strings.Contains(r.body, "00:00:01.000 --> 00:00:02.000") || !strings.Contains(r.body, "Hello PG") {
		t.Fatalf("srt sidecar not converted: %q", r.body)
	}
	if r := byID[sidecarTrackID(filepath.Join(f.dir, "pg", "Movie PG.fr.vtt"))]; !strings.Contains(r.body, "Bonjour PG") {
		t.Fatalf("vtt sidecar: %q", r.body)
	}
	if r := byID["sub-pg-1"]; !strings.Contains(r.body, "/api/subtitles/sub-pg-1/vtt") {
		t.Fatalf("module track did not reach media-subtitles: %q", r.body)
	}
}

func TestS5bSubtitleTrackFetchDeniedWithoutAuthorizedBinding(t *testing.T) {
	f := newSubtitleFixture(t)
	kid := f.kid(policyJSON(true, "", false, nil, nil))
	sibling := f.session("sibling", "", "bearer-sibling")
	f.setPolicy("sibling", policyJSON(true, "", false, nil, nil))
	otherDevice := f.session("kid", "", "bearer-kid") // same user, another BFF session
	adult := f.session("adult", "", "bearer-adult")

	granted := f.tracks(t, "/stream/movies/m-pg", kid)
	if len(granted) != 3 {
		t.Fatalf("tracks=%+v", granted)
	}
	pgSidecar := sidecarTrackID(filepath.Join(f.dir, "pg", "Movie PG.en.srt"))

	// A denied item lists nothing and its tracks cannot be fetched, whether
	// the id is a sidecar id built from a path, a module id or a guess.
	assertBlocked(t, "denied item list", f.get("/api/playback/subtitles?src="+url.QueryEscape("/stream/movies/m-r"), kid))
	for name, id := range map[string]string{
		"denied item sidecar":      sidecarTrackID(filepath.Join(f.dir, "r", "Movie R.en.srt")),
		"denied item module track": "sub-r-1",
		"never advertised":         "sub-nobody",
		"forged path":              sidecarTrackID(f.secret),
		"path of the allowed item": sidecarTrackID(filepath.Join(f.dir, "pg", "Movie PG.en.srt")) + "x",
	} {
		assertBlocked(t, name, f.get("/api/playback/subtitles/"+url.PathEscape(id), kid))
	}

	// Bindings belong to one BFF session: another user and another session of
	// the same user cannot use them.
	for name, tok := range map[string]string{"sibling": sibling, "same user, other session": otherDevice} {
		for _, tr := range granted {
			assertBlocked(t, name+" "+tr.ID, f.get(tr.Src, tok))
		}
	}
	// Unrestricted principals keep today's behavior (no binding needed).
	for _, tr := range granted {
		if res := f.get(tr.Src, adult); res.status != http.StatusOK {
			t.Fatalf("unrestricted fetch of %q: %d %s", tr.ID, res.status, res.body)
		}
	}
	if res := f.get("/api/playback/subtitles/sub-r-1", adult); res.status != http.StatusOK {
		t.Fatalf("unrestricted module track: %d %s", res.status, res.body)
	}
	if got := len(f.s.parental.subtitles.byID); got != 3 {
		t.Fatalf("only the restricted list may bind, got %d bindings", got)
	}
	// A client-supplied item cannot name the authority.
	for _, extra := range []string{"?src=" + url.QueryEscape("/stream/movies/m-pg"), "?media_id=m-pg", "?item=m-pg"} {
		assertBlocked(t, "supplied item "+extra, f.get("/api/playback/subtitles/"+url.PathEscape(sidecarTrackID(f.secret))+extra, kid))
	}
	_ = pgSidecar
}

func TestS5bSubtitleTrackRechecksPolicyClassificationAndFailuresEachFetch(t *testing.T) {
	f := newSubtitleFixture(t)
	kid := f.kid(policyJSON(true, "", false, nil, nil))
	tracks := f.tracks(t, "/stream/movies/m-pg", kid)
	fetchAll := func() []int {
		var out []int
		for _, tr := range tracks {
			out = append(out, f.get(tr.Src, kid).status)
		}
		return out
	}
	allStatus := func(want int) bool {
		for _, s := range fetchAll() {
			if s != want {
				return false
			}
		}
		return true
	}
	if !allStatus(200) {
		t.Fatalf("baseline %v", fetchAll())
	}

	// The policy tightens: the next fetch after the policy TTL is refused.
	f.setPolicy("kid", policyJSON(false, "G", false, nil, nil))
	if !allStatus(200) {
		t.Fatal("inside the 30 s policy window the cached policy still applies")
	}
	f.clock.Advance(parentalPolicyTTL + time.Second)
	for _, tr := range tracks {
		assertBlocked(t, "tightened policy "+tr.ID, f.get(tr.Src, kid))
	}
	// The binding survives; loosening again restores access.
	f.setPolicy("kid", policyJSON(true, "", false, nil, nil))
	f.clock.Advance(parentalPolicyTTL + time.Second)
	if !allStatus(200) {
		t.Fatalf("restored policy %v", fetchAll())
	}

	// The item is reclassified: refused once the classification cache expires.
	for _, m := range f.cat.movies {
		if m.GetId() == "m-pg" {
			m.ContentRating = "R"
		}
	}
	f.clock.Advance(playbackClassificationTTL + time.Second)
	f.clock.Advance(parentalPolicyTTL) // keep the policy fresh, it did not change
	for _, tr := range tracks {
		assertBlocked(t, "reclassified "+tr.ID, f.get(tr.Src, kid))
	}
	for _, m := range f.cat.movies {
		if m.GetId() == "m-pg" {
			m.ContentRating = "PG"
		}
	}
	f.clock.Advance(playbackClassificationTTL + time.Second)
	if !allStatus(200) {
		t.Fatalf("after restoring the rating %v", fetchAll())
	}

	// A failed classification lookup is 503, never an allowed fetch.
	f.cat.failMovies = status.Error(codes.Unavailable, "movies down")
	f.clock.Advance(playbackClassificationTTL + time.Second)
	for _, tr := range tracks {
		assertParentalError(t, "classification down "+tr.ID, f.get(tr.Src, kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	}
	f.cat.failMovies = nil
	f.clock.Advance(playbackClassificationTTL + time.Second)
	if !allStatus(200) {
		t.Fatalf("after recovery %v", fetchAll())
	}

	// A failed policy lookup is 503 and a 401 evicts: neither fetches.
	f.provider.set(http.StatusInternalServerError, "boom")
	f.clock.Advance(parentalPolicyTTL + time.Second)
	for _, tr := range tracks {
		assertParentalError(t, "policy down "+tr.ID, f.get(tr.Src, kid), http.StatusServiceUnavailable, parentalCodeUnavailable)
	}
}

func TestS5bSubtitleBindingExpiryEvictionAndRestartFailClosed(t *testing.T) {
	f := newSubtitleFixture(t)
	kid := f.kid(policyJSON(true, "", false, nil, nil))
	tracks := f.tracks(t, "/stream/movies/m-pg", kid)
	keep := func(d time.Duration) {
		f.clock.Advance(d)
		f.setPolicy("kid", policyJSON(true, "", false, nil, nil))
	}
	// Sliding window: use refreshes the binding.
	keep(subtitleBindingTTL - time.Minute)
	if res := f.get(tracks[0].Src, kid); res.status != http.StatusOK {
		t.Fatalf("inside the window: %d %s", res.status, res.body)
	}
	keep(subtitleBindingTTL - time.Minute)
	if res := f.get(tracks[0].Src, kid); res.status != http.StatusOK {
		t.Fatalf("window must slide with use: %d %s", res.status, res.body)
	}
	// An unused binding expires.
	keep(subtitleBindingTTL + time.Second)
	for _, tr := range tracks {
		assertBlocked(t, "expired "+tr.ID, f.get(tr.Src, kid))
	}
	if len(f.s.parental.subtitles.byID) != 0 || len(f.s.parental.subtitles.bySession) != 0 {
		t.Fatalf("expired bindings were not dropped: %d", len(f.s.parental.subtitles.byID))
	}
	// A fresh authorized list restores access; a restart (empty table) denies.
	tracks = f.tracks(t, "/stream/movies/m-pg", kid)
	if res := f.get(tracks[0].Src, kid); res.status != http.StatusOK {
		t.Fatalf("re-listed: %d", res.status)
	}
	f.s.parental.subtitles = newSubtitleTrackBindings()
	for _, tr := range tracks {
		assertBlocked(t, "after restart "+tr.ID, f.get(tr.Src, kid))
	}
}

// Two videos in one directory can both claim a sidecar. The id is then bound
// to both, and a fetch needs both authorized: it is only as visible as the more
// restricted item.
func TestS5bSubtitleIDCollisionIsConservative(t *testing.T) {
	f := newSubtitleFixture(t)
	g, pg := filepath.Join(f.dir, "shared", "Show.mkv"), filepath.Join(f.dir, "shared", "Show.en.mkv")
	writeFixtureFile(t, g, "video")
	writeFixtureFile(t, pg, "video")
	shared := filepath.Join(f.dir, "shared", "Show.en.srt")
	writeFixtureFile(t, shared, fmt.Sprintf(srtBody, "Hello shared"))
	f.cat.files["m-g"], f.cat.files["m-pg"] = g, pg
	kid := f.kid(policyJSON(true, "", false, nil, nil))

	forG, forPG := f.tracks(t, "/stream/movies/m-g", kid), f.tracks(t, "/stream/movies/m-pg", kid)
	id := sidecarTrackID(shared)
	has := func(ts []playbackSubtitleTrack) bool {
		for _, tr := range ts {
			if tr.ID == id {
				return true
			}
		}
		return false
	}
	if !has(forG) || !has(forPG) {
		t.Fatalf("fixture must make both items claim the sidecar: %v %v", trackIDs(forG), trackIDs(forPG))
	}
	if res := f.get("/api/playback/subtitles/"+id, kid); res.status != http.StatusOK {
		t.Fatalf("both items allowed: %d %s", res.status, res.body)
	}
	// Only G stays allowed: the PG claim now blocks the shared id.
	f.setPolicy("kid", policyJSON(false, "G", false, nil, nil))
	f.clock.Advance(parentalPolicyTTL + time.Second)
	assertBlocked(t, "shared id with one denied claimant", f.get("/api/playback/subtitles/"+id, kid))
	// m-g's own listing still works, so the denial is the binding rule, not the item.
	if len(f.tracks(t, "/stream/movies/m-g", kid)) == 0 {
		t.Fatal("m-g should still list its tracks")
	}
}

func TestSubtitleTrackBindingsTable(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	movie := func(id string) parentalItem { return parentalItem{Kind: kindMovie, ID: id} }

	var nilB *subtitleTrackBindings
	if nilB.bind("s", "t", movie("a"), now) {
		t.Fatal("nil table must refuse")
	}
	if _, ok := nilB.lookup("s", "t", now); ok {
		t.Fatal("nil table must not look up")
	}
	b := newSubtitleTrackBindings()
	for name, args := range map[string][3]string{
		"empty session":  {"", "t", "a"},
		"empty track":    {"s", "", "a"},
		"padded track":   {"s", " t", "a"},
		"oversize track": {"s", strings.Repeat("x", subtitleTrackIDMax+1), "a"},
		"empty item":     {"s", "t", ""},
	} {
		if b.bind(args[0], args[1], movie(args[2]), now) {
			t.Errorf("%s: bound", name)
		}
	}
	if b.bind("s", "t", parentalItem{ID: "a"}, now) {
		t.Error("an item with no kind bound")
	}
	if !b.bind("s", "t", movie("a"), now) || !b.bind("s", "t", movie("a"), now) {
		t.Fatal("bind failed")
	}
	if items, ok := b.lookup("s", "t", now); !ok || len(items) != 1 {
		t.Fatalf("duplicate claim must not grow the binding: %v", items)
	}
	if _, ok := b.lookup("other", "t", now); ok {
		t.Fatal("another session sees the binding")
	}
	// The items of one id are capped; the over-cap claimant is refused so the
	// caller does not advertise the track.
	for i := 1; i < subtitleBindingItems; i++ {
		if !b.bind("s", "t", movie(fmt.Sprint("x", i)), now) {
			t.Fatalf("claim %d refused", i)
		}
	}
	if b.bind("s", "t", movie("one-too-many"), now) {
		t.Fatal("claim over the cap bound")
	}
	// An expired binding is replaced, not extended with the old claimants.
	later := now.Add(subtitleBindingTTL + time.Second)
	if !b.bind("s", "t", movie("fresh"), later) {
		t.Fatal("rebind after expiry")
	}
	if items, _ := b.lookup("s", "t", later); len(items) != 1 || items[0].ID != "fresh" {
		t.Fatalf("expired claimants survived: %v", items)
	}

	// One session cannot push another session's bindings out.
	b = newSubtitleTrackBindings()
	b.perSession, b.max = 4, 8
	b.bind("victim", "v", movie("a"), now)
	for i := range 40 {
		b.bind("attacker", fmt.Sprint("t", i), movie("a"), now.Add(time.Duration(i)*time.Second))
	}
	if _, ok := b.lookup("victim", "v", now.Add(time.Minute)); !ok {
		t.Fatal("the attacker evicted another session's binding")
	}
	if got := len(b.bySession["attacker"]); got != 4 {
		t.Fatalf("attacker holds %d bindings, cap 4", got)
	}
	// The global cap evicts the least recently used binding.
	b = newSubtitleTrackBindings()
	b.perSession, b.max = 100, 3
	for i := range 3 {
		b.bind(fmt.Sprint("s", i), "t", movie("a"), now.Add(time.Duration(i)*time.Second))
	}
	b.bind("s9", "t", movie("a"), now.Add(10*time.Second))
	if _, ok := b.lookup("s0", "t", now.Add(11*time.Second)); ok {
		t.Fatal("the oldest binding must be evicted at the global cap")
	}
	if len(b.byID) != 3 {
		t.Fatalf("table=%d", len(b.byID))
	}
	n := 0
	for _, own := range b.bySession {
		n += len(own)
	}
	if n != len(b.byID) {
		t.Fatalf("session index %d != table %d", n, len(b.byID))
	}
}

// The gate names the item from src; any src form it cannot read as exactly one
// item is refused before the handler runs.
func TestS5bSubtitleListRefusesAmbiguousSrc(t *testing.T) {
	f := newSubtitleFixture(t)
	kid := f.kid(policyJSON(true, "", false, nil, nil))
	for _, src := range []string{"/stream/movies/m-pg?x=1", "/stream/movies/m-pg#frag", "/stream/movies/m-pg/", "/stream/movies/m-pg/../m-r", ""} {
		assertBlocked(t, "src "+src, f.get("/api/playback/subtitles?src="+url.QueryEscape(src), kid))
	}
	if len(f.s.parental.subtitles.byID) != 0 {
		t.Fatal("a refused list bound tracks")
	}
}

// Defense in depth: the handler resolves and binds tracks for the item the
// gate authorized (the grant), never for whatever its own parse of src yields.
func TestS5bSubtitleListBindsTheGrantedItemNotSrc(t *testing.T) {
	f := newSubtitleFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/playback/subtitles?src="+url.QueryEscape("/stream/movies/m-r"), nil)
	grant := parentalGrant{sessionID: "sess-1", item: parentalItem{Kind: kindMovie, ID: "m-pg"}}
	req = req.WithContext(context.WithValue(req.Context(), parentalGrantKey{}, grant))
	rec := httptest.NewRecorder()
	f.s.handlePlaybackSubtitlesList(rec, req)
	var tl trackList
	if err := json.Unmarshal(rec.Body.Bytes(), &tl); err != nil || len(tl.Tracks) != 3 {
		t.Fatalf("tracks for the granted item: %v %s", err, rec.Body.String())
	}
	for _, tr := range tl.Tracks {
		if tr.ID == "sub-r-1" || strings.Contains(tr.Label, "Movie R") {
			t.Fatalf("a track of the src item was listed: %+v", tr)
		}
		items, ok := f.s.parental.subtitles.lookup("sess-1", tr.ID, f.clock.Now())
		if !ok || len(items) != 1 || items[0].ID != "m-pg" {
			t.Fatalf("track %q bound to %v", tr.ID, items)
		}
	}
	// Without a grant (unrestricted) nothing is bound and src rules.
	req = httptest.NewRequest(http.MethodGet, "/api/playback/subtitles?src="+url.QueryEscape("/stream/movies/m-r"), nil)
	rec = httptest.NewRecorder()
	f.s.handlePlaybackSubtitlesList(rec, req)
	if !strings.Contains(rec.Body.String(), "sub-r-1") || len(f.s.parental.subtitles.byID) != 3 {
		t.Fatalf("unrestricted list: %s bindings=%d", rec.Body.String(), len(f.s.parental.subtitles.byID))
	}
}

func TestS5bSubtitleEpisodeTracksAreBoundToTheSeries(t *testing.T) {
	f := newSubtitleFixture(t)
	ok, bad := filepath.Join(f.dir, "tv", "Pilot.mkv"), filepath.Join(f.dir, "tv-bad", "Finale.mkv")
	writeFixtureFile(t, ok, "video")
	writeFixtureFile(t, bad, "video")
	writeFixtureFile(t, filepath.Join(f.dir, "tv", "Pilot.en.srt"), fmt.Sprintf(srtBody, "Hello episode"))
	writeFixtureFile(t, filepath.Join(f.dir, "tv-bad", "Finale.en.srt"), fmt.Sprintf(srtBody, "Hello bad episode"))
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths := map[string]string{"/api/episodes/e-ok/file": ok, "/api/episodes/e-bad/file": bad}
		p, found := paths[r.URL.Path]
		if !found {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"file_id": "ef", "file_path": p})
	}))
	t.Cleanup(tv.Close)
	f.s.tvHTTP = mustURL(tv.URL)
	kid := f.kid(policyJSON(true, "", false, nil, nil))

	tracks := f.tracks(t, "/stream/tv/e-ok", kid)
	if len(tracks) != 1 || !strings.HasPrefix(tracks[0].ID, "sc_") {
		t.Fatalf("episode tracks=%+v", tracks)
	}
	if res := f.get(tracks[0].Src, kid); res.status != http.StatusOK || !strings.Contains(res.body, "Hello episode") {
		t.Fatalf("episode track: %d %s", res.status, res.body)
	}
	assertBlocked(t, "episode of a blocked series", f.get("/api/playback/subtitles?src="+url.QueryEscape("/stream/tv/e-bad"), kid))
	assertBlocked(t, "blocked episode sidecar", f.get("/api/playback/subtitles/"+sidecarTrackID(filepath.Join(f.dir, "tv-bad", "Finale.en.srt")), kid))
}

// The download route hands out a track_url for the id it just fetched. It is
// an operator route that stays C-DENY for restricted principals, so a
// restricted session is never given an unbound track URL.
func TestS5bSubtitleDownloadTrackURLIsNotReachableWhenRestricted(t *testing.T) {
	f := newSubtitleFixture(t)
	kid := f.kid(policyJSON(true, "", false, nil, nil))
	op := f.tokFor("POST /api/subtitles/download", kid)
	req := httptest.NewRequest(http.MethodPost, "/api/subtitles/download", strings.NewReader(`{"id":"cand-1"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: op})
	assertParentalError(t, "POST /api/subtitles/download", serve(f.gated, req), http.StatusForbidden, parentalCodeRestrictedRoute)
}

// --- F4: owning-item mapping of item subtitles ------------------------------

func TestS5bMovieSubtitlesDoNotMapMovieIDToSubtitleMediaID(t *testing.T) {
	f := newSubtitleFixture(t)
	// m-g has no file in media-movies, which is what makes the handler fall
	// back to media-subtitles GetMedia(movie id). media-subtitles ids are its
	// own namespace; here its media row "m-g" happens to belong to another
	// item's file.
	f.subs.media["m-g"] = &subtv1.SubtitleMediaItem{Id: "m-g", Title: "Another title", MediaFileId: "file-m-r", MediaType: "movie"}
	kid := f.kid(policyJSON(true, "", false, nil, nil))
	adult := f.session("adult", "", "bearer-adult")

	// Unrestricted compatibility is kept (the fallback still runs).
	res := f.get("/api/movies/m-g/subtitles", adult)
	if res.status != http.StatusOK || !strings.Contains(res.body, "sub-r-1") {
		t.Fatalf("unrestricted fallback changed: %d %s", res.status, res.body)
	}
	f.subs.listed = nil

	res = f.get("/api/movies/m-g/subtitles", kid)
	if res.status != http.StatusOK {
		t.Fatalf("%d %s", res.status, res.body)
	}
	if strings.Contains(res.body, "sub-r-1") || strings.Contains(res.body, "file-m-r") || f.subs.askedAbout("file-m-r") {
		t.Fatalf("a restricted caller was handed another item's subtitle files: %s", res.body)
	}
	var out struct {
		Items []any `json:"items"`
		Files []any `json:"files"`
	}
	_ = json.Unmarshal([]byte(res.body), &out)
	if len(out.Items) != 0 || len(out.Files) != 0 {
		t.Fatalf("restricted fallback must be empty: %s", res.body)
	}
	// The owning module's own file mapping still works for a restricted caller.
	res = f.get("/api/movies/m-pg/subtitles", kid)
	if !strings.Contains(res.body, "sub-pg-1") || strings.Contains(res.body, "sub-r-1") {
		t.Fatalf("m-pg subtitles: %s", res.body)
	}
}

func TestS5bTVSubtitlesKeepToTheAuthorizedSeries(t *testing.T) {
	f := newSubtitleFixture(t)
	f.subs.series["s-ok"] = []*subtv1.SubtitleMediaItem{
		{Id: "med-1", SeriesId: "s-ok", MediaFileId: "ef-ok", Title: "Pilot", Season: 1, Episode: 1},
		{Id: "med-2", SeriesId: "s-bad", MediaFileId: "ef-bad", Title: "Stray"}, // not this series
	}
	f.subs.rows["ef-ok"] = []*subtv1.SubtitleFile{
		{Id: "sub-ok", MediaFileId: "ef-ok"},
		{Id: "sub-stray-row", MediaFileId: "ef-bad"}, // row of another file
	}
	f.subs.rows["ef-bad"] = []*subtv1.SubtitleFile{{Id: "sub-bad", MediaFileId: "ef-bad"}}
	kid := f.kid(policyJSON(true, "", false, nil, nil))
	adult := f.session("adult", "", "bearer-adult")

	res := f.get("/api/tv/s-ok/subtitles", kid)
	if res.status != http.StatusOK || !strings.Contains(res.body, "sub-ok") {
		t.Fatalf("%d %s", res.status, res.body)
	}
	for _, leak := range []string{"sub-bad", "sub-stray-row", "ef-bad", "Stray"} {
		if strings.Contains(res.body, leak) {
			t.Fatalf("%q leaked to a restricted caller: %s", leak, res.body)
		}
	}
	// Unrestricted behavior is unchanged: the same module answer, unfiltered.
	res = f.get("/api/tv/s-ok/subtitles", adult)
	if !strings.Contains(res.body, "sub-bad") || !strings.Contains(res.body, "ef-bad") {
		t.Fatalf("unrestricted answer changed: %s", res.body)
	}

	// The GetMedia fallback treats the series id as a media-subtitles media.id.
	f.subs.series["s-ok"] = nil
	f.subs.media["s-ok"] = &subtv1.SubtitleMediaItem{Id: "s-ok", MediaFileId: "ef-bad", Title: "Elsewhere"}
	if res = f.get("/api/tv/s-ok/subtitles", adult); !strings.Contains(res.body, "ef-bad") {
		t.Fatalf("unrestricted fallback changed: %s", res.body)
	}
	f.subs.listed = nil
	res = f.get("/api/tv/s-ok/subtitles", kid)
	if res.status != http.StatusOK || strings.Contains(res.body, "ef-bad") || strings.Contains(res.body, "sub-bad") || f.subs.askedAbout("ef-bad") {
		t.Fatalf("restricted fallback used a foreign media row: %d %s", res.status, res.body)
	}
}
