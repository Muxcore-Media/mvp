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

// Subtitle ownership for a restricted principal (ADR-0031 §1.2, C-PLAY
// GET /api/playback/subtitles/{id} and C-ITEM item subtitles). The upstream
// classification wiring (parental_classification_test.go) decides which item a
// request may see; these tests cover what hangs off that item: which sidecar
// files and module rows belong to it, and which tracks a session may fetch.

const (
	srtBody    = "1\n00:00:01,000 --> 00:00:02,000\n%s\n"
	secretBody = "TOPSECRET outside any library"

	tightPolicyJSON = `{"version":1,"mode":"restricted","rules":{"kids_mode":false,"max_rating":"G","blocked_tags":[],"allowed_tags":[],"allow_unrated":false}}`
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

// subtitleMovies adds the file listing the playback subtitle handler needs.
type subtitleMovies struct {
	*classificationMovies
	files map[string]string // movie id → file path
}

func (f *subtitleMovies) ListFiles(_ context.Context, in *mgmntv1.ListFilesRequest, _ ...grpc.CallOption) (*mgmntv1.ListFilesResponse, error) {
	path, ok := f.files[in.GetMovieId()]
	if !ok {
		return &mgmntv1.ListFilesResponse{}, nil
	}
	return &mgmntv1.ListFilesResponse{Files: []*mgmntv1.MovieFile{{Id: "file-" + in.GetMovieId(), MovieId: in.GetMovieId(), FilePath: path}}}, nil
}

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
	*parentalHarness
	movies *classificationMovies
	subs   *fakeSubs
	dir    string
	secret string // a file outside every library
	kid    string
	adult  string
}

// newSubtitleFixture gives m-pg (allowed for the kid) and m-r (denied) real
// video files with sidecars on disk and module-held subtitle rows.
func newSubtitleFixture(t *testing.T) *subtitleFixture {
	t.Helper()
	h, movies, _, kid := classificationFixture(t)
	f := &subtitleFixture{parentalHarness: h, movies: movies, dir: t.TempDir(), kid: kid}
	h.provider.doc(func(u string) string {
		if u == "adult" {
			return configuredDoc(u, "", 1, unrestrictedPolicyJSON)
		}
		return configuredDoc(u, "", 1, restrictedPolicyJSON)
	})
	f.adult = h.session("adult", "", "bearer-adult")
	movies.items["m-pg"] = &mgmntv1.MovieItem{Id: "m-pg", Title: "Movie PG", ContentRating: "PG", ContentRatingSource: "operator"}
	movies.items["m-r"] = &mgmntv1.MovieItem{Id: "m-r", Title: "Movie R", ContentRating: "R", ContentRatingSource: "operator"}
	movies.items["m-g"] = &mgmntv1.MovieItem{Id: "m-g", Title: "Movie G", ContentRating: "G", ContentRatingSource: "operator"}

	pg, r := filepath.Join(f.dir, "pg", "Movie PG.mkv"), filepath.Join(f.dir, "r", "Movie R.mkv")
	writeFixtureFile(t, pg, "video")
	writeFixtureFile(t, r, "video")
	writeFixtureFile(t, filepath.Join(f.dir, "pg", "Movie PG.en.srt"), fmt.Sprintf(srtBody, "Hello PG"))
	writeFixtureFile(t, filepath.Join(f.dir, "pg", "Movie PG.fr.vtt"), "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nBonjour PG\n")
	writeFixtureFile(t, filepath.Join(f.dir, "r", "Movie R.en.srt"), fmt.Sprintf(srtBody, "Hello R"))
	f.secret = filepath.Join(f.dir, "outside", "secret.srt")
	writeFixtureFile(t, f.secret, secretBody)
	h.s.movies = &subtitleMovies{classificationMovies: movies, files: map[string]string{"m-pg": pg, "m-r": r}}

	f.subs = &fakeSubs{
		rows: map[string][]*subtv1.SubtitleFile{
			"file-m-pg": {{Id: "sub-pg-1", MediaFileId: "file-m-pg", Language: "eng", Source: "opensubtitles"}},
			"file-m-r":  {{Id: "sub-r-1", MediaFileId: "file-m-r", Language: "eng", Source: "opensubtitles"}},
		},
		media:  map[string]*subtv1.SubtitleMediaItem{},
		series: map[string][]*subtv1.SubtitleMediaItem{},
	}
	h.s.subtitles = f.subs
	return f
}

func (f *subtitleFixture) get(path, tok string) serveResult {
	return classificationRequest(f.parentalHarness, tok, path)
}

// setPolicy replaces the restricted policy every non-adult user gets.
func (f *subtitleFixture) setPolicy(policy string) {
	f.provider.doc(func(u string) string {
		if u == "adult" {
			return configuredDoc(u, "", 1, unrestrictedPolicyJSON)
		}
		return configuredDoc(u, "", 2, policy)
	})
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

func TestSubtitleTracksAdvertisedToRestrictedAreFetchable(t *testing.T) {
	f := newSubtitleFixture(t)
	tracks := f.tracks(t, "/stream/movies/m-pg", f.kid)
	if len(tracks) != 3 {
		t.Fatalf("want 2 sidecar + 1 module track, got %+v", tracks)
	}
	byID := map[string]serveResult{}
	for _, tr := range tracks {
		res := f.get(tr.Src, f.kid)
		if res.status != http.StatusOK {
			t.Fatalf("advertised track %q is not fetchable: %d %s", tr.ID, res.status, res.body)
		}
		byID[tr.ID] = res
	}
	if r := byID[sidecarTrackID(filepath.Join(f.dir, "pg", "Movie PG.en.srt"))]; !strings.Contains(r.body, "WEBVTT") || !strings.Contains(r.body, "Hello PG") {
		t.Fatalf("srt sidecar not converted: %q", r.body)
	}
	if r := byID[sidecarTrackID(filepath.Join(f.dir, "pg", "Movie PG.fr.vtt"))]; !strings.Contains(r.body, "Bonjour PG") {
		t.Fatalf("vtt sidecar: %q", r.body)
	}
	if r := byID["sub-pg-1"]; !strings.Contains(r.body, "/api/subtitles/sub-pg-1/vtt") {
		t.Fatalf("module track did not reach media-subtitles: %q", r.body)
	}
}

func TestSubtitleTrackFetchDeniedWithoutAuthorizedBinding(t *testing.T) {
	f := newSubtitleFixture(t)
	sibling := f.session("sibling", "", "bearer-sibling")
	otherDevice := f.session("kid", "", "kid-bearer") // same user, another BFF session

	granted := f.tracks(t, "/stream/movies/m-pg", f.kid)
	assertBlocked(t, "denied item list", f.get("/api/playback/subtitles?src="+url.QueryEscape("/stream/movies/m-r"), f.kid))
	for name, id := range map[string]string{
		"denied item sidecar":      sidecarTrackID(filepath.Join(f.dir, "r", "Movie R.en.srt")),
		"denied item module track": "sub-r-1",
		"never advertised":         "sub-nobody",
		"forged path":              sidecarTrackID(f.secret),
		"altered advertised path":  sidecarTrackID(filepath.Join(f.dir, "pg", "Movie PG.en.srt")) + "x",
	} {
		assertBlocked(t, name, f.get("/api/playback/subtitles/"+url.PathEscape(id), f.kid))
	}
	// Bindings belong to one BFF session.
	for name, tok := range map[string]string{"sibling": sibling, "same user, other session": otherDevice} {
		for _, tr := range granted {
			assertBlocked(t, name+" "+tr.ID, f.get(tr.Src, tok))
		}
	}
	// Unrestricted principals need no binding and bind nothing.
	for _, tr := range granted {
		if res := f.get(tr.Src, f.adult); res.status != http.StatusOK {
			t.Fatalf("unrestricted fetch of %q: %d %s", tr.ID, res.status, res.body)
		}
	}
	if got := len(f.s.parental.subtitles.byID); got != 3 {
		t.Fatalf("only the restricted list may bind, got %d bindings", got)
	}
	// A client-supplied item cannot name the authority.
	for _, extra := range []string{"?src=" + url.QueryEscape("/stream/movies/m-pg"), "?media_id=m-pg", "?item=m-pg"} {
		assertBlocked(t, "supplied item "+extra, f.get("/api/playback/subtitles/"+url.PathEscape(sidecarTrackID(f.secret))+extra, f.kid))
	}
}

func TestSubtitleTrackRechecksPolicyAndClassificationEachFetch(t *testing.T) {
	f := newSubtitleFixture(t)
	tracks := f.tracks(t, "/stream/movies/m-pg", f.kid)
	allStatus := func(want int) bool {
		for _, tr := range tracks {
			if f.get(tr.Src, f.kid).status != want {
				return false
			}
		}
		return true
	}
	if !allStatus(http.StatusOK) {
		t.Fatal("baseline fetch failed")
	}

	// The policy tightens: refused once the cached policy expires.
	f.setPolicy(tightPolicyJSON)
	f.clock.Advance(parentalPolicyTTL + time.Second)
	for _, tr := range tracks {
		assertBlocked(t, "tightened policy "+tr.ID, f.get(tr.Src, f.kid))
	}
	// The binding survives; loosening again restores access.
	f.setPolicy(restrictedPolicyJSON)
	f.clock.Advance(parentalPolicyTTL + time.Second)
	if !allStatus(http.StatusOK) {
		t.Fatal("restored policy did not restore access")
	}

	// The item is reclassified: refused once the classification cache expires.
	f.movies.items["m-pg"].ContentRating = "R"
	f.clock.Advance(parentalPolicyTTL + time.Second)
	for _, tr := range tracks {
		assertBlocked(t, "reclassified "+tr.ID, f.get(tr.Src, f.kid))
	}
	f.movies.items["m-pg"].ContentRating = "PG"
	f.clock.Advance(parentalPolicyTTL + time.Second)

	// A failed classification lookup is 503, never an allowed fetch.
	f.movies.getErr = status.Error(codes.Unavailable, "movies down")
	f.clock.Advance(parentalPolicyTTL + time.Second)
	for _, tr := range tracks {
		assertParentalError(t, "classification down "+tr.ID, f.get(tr.Src, f.kid), http.StatusServiceUnavailable, parentalCodeClassificationUnavailable)
	}
	f.movies.getErr = nil
	f.clock.Advance(parentalPolicyTTL + time.Second)
	if !allStatus(http.StatusOK) {
		t.Fatal("recovery did not restore access")
	}

	// A failed policy lookup is 503.
	f.provider.set(http.StatusInternalServerError, "boom")
	f.clock.Advance(parentalPolicyTTL + time.Second)
	for _, tr := range tracks {
		assertParentalError(t, "policy down "+tr.ID, f.get(tr.Src, f.kid), http.StatusServiceUnavailable, parentalCodeUnavailable)
	}
}

func TestSubtitleBindingExpiryAndRestartFailClosed(t *testing.T) {
	f := newSubtitleFixture(t)
	tracks := f.tracks(t, "/stream/movies/m-pg", f.kid)
	keep := func(d time.Duration) {
		f.clock.Advance(d)
		f.setPolicy(restrictedPolicyJSON)
	}
	// Sliding window: use refreshes the binding.
	keep(subtitleBindingTTL - time.Minute)
	if res := f.get(tracks[0].Src, f.kid); res.status != http.StatusOK {
		t.Fatalf("inside the window: %d %s", res.status, res.body)
	}
	keep(subtitleBindingTTL - time.Minute)
	if res := f.get(tracks[0].Src, f.kid); res.status != http.StatusOK {
		t.Fatalf("window must slide with use: %d %s", res.status, res.body)
	}
	// An unused binding expires.
	keep(subtitleBindingTTL + time.Second)
	for _, tr := range tracks {
		assertBlocked(t, "expired "+tr.ID, f.get(tr.Src, f.kid))
	}
	if len(f.s.parental.subtitles.byID) != 0 || len(f.s.parental.subtitles.bySession) != 0 {
		t.Fatalf("expired bindings were not dropped: %d", len(f.s.parental.subtitles.byID))
	}
	// A fresh authorized list restores access; a restart (empty table) denies.
	tracks = f.tracks(t, "/stream/movies/m-pg", f.kid)
	if res := f.get(tracks[0].Src, f.kid); res.status != http.StatusOK {
		t.Fatalf("re-listed: %d", res.status)
	}
	f.s.parental.subtitles = newSubtitleTrackBindings()
	for _, tr := range tracks {
		assertBlocked(t, "after restart "+tr.ID, f.get(tr.Src, f.kid))
	}
}

// A track ID bound for two items is only as visible as the more restricted
// one. Sidecar ownership stops two videos of one folder claiming a file at
// listing time, so the claims are made through the binder directly: the rule
// is defense in depth.
func TestSubtitleIDCollisionIsConservative(t *testing.T) {
	f := newSubtitleFixture(t)
	shared := filepath.Join(f.dir, "shared", "Show.en.srt")
	writeFixtureFile(t, shared, fmt.Sprintf(srtBody, "Hello shared"))
	id := sidecarTrackID(shared)
	track := []playbackSubtitleTrack{{ID: id}}
	sid := sessionID(f.kid)
	g := parentalGrant{sessionID: sid, item: parentalItem{Kind: "movie", ID: "m-g"}}
	pg := parentalGrant{sessionID: sid, item: parentalItem{Kind: "movie", ID: "m-pg"}}
	if got := f.s.parental.bindSubtitleTracks(g, true, track); len(got) != 1 {
		t.Fatalf("first claim: %v", got)
	}
	if got := f.s.parental.bindSubtitleTracks(pg, true, track); len(got) != 1 {
		t.Fatalf("second claim: %v", got)
	}
	if items, ok := f.s.parental.subtitles.lookup(sid, id, f.clock.Now()); !ok || len(items) != 2 {
		t.Fatalf("both claimants must be bound: %v", items)
	}
	if res := f.get("/api/playback/subtitles/"+id, f.kid); res.status != http.StatusOK {
		t.Fatalf("both items allowed: %d %s", res.status, res.body)
	}
	f.setPolicy(tightPolicyJSON) // only G stays allowed: the PG claim now blocks the shared id
	f.clock.Advance(parentalPolicyTTL + time.Second)
	assertBlocked(t, "shared id with one denied claimant", f.get("/api/playback/subtitles/"+id, f.kid))
}

func TestSubtitleTrackBindingsTable(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	movie := func(id string) parentalItem { return parentalItem{Kind: "movie", ID: id} }

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
	for i := 0; i < 2; i++ { // the second bind is a duplicate claim of the same item
		if !b.bind("s", "t", movie("a"), now) {
			t.Fatalf("bind %d failed", i+1)
		}
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
	n := 0
	for _, own := range b.bySession {
		n += len(own)
	}
	if len(b.byID) != 3 || n != len(b.byID) {
		t.Fatalf("table=%d session index=%d", len(b.byID), n)
	}
}

// The gate names the item from src; any src form it cannot read as exactly one
// item is refused before the handler runs and binds nothing.
func TestSubtitleListRefusesAmbiguousSrc(t *testing.T) {
	f := newSubtitleFixture(t)
	for _, src := range []string{"/stream/movies/m-pg?x=1", "/stream/movies/m-pg#frag", "/stream/movies/m-pg/", "/stream/movies/m-pg/../m-r", ""} {
		assertBlocked(t, "src "+src, f.get("/api/playback/subtitles?src="+url.QueryEscape(src), f.kid))
	}
	if len(f.s.parental.subtitles.byID) != 0 {
		t.Fatal("a refused list bound tracks")
	}
}

// Defense in depth: the handler resolves and binds tracks for the item the
// gate authorized (the grant), never for whatever its own parse of src yields.
func TestSubtitleListBindsTheGrantedItemNotSrc(t *testing.T) {
	f := newSubtitleFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/playback/subtitles?src="+url.QueryEscape("/stream/movies/m-r"), nil)
	grant := parentalGrant{sessionID: "sess-1", item: parentalItem{Kind: "movie", ID: "m-pg"}}
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

// --- owning-item mapping of item subtitles ----------------------------------

func TestMovieSubtitlesDoNotMapMovieIDToSubtitleMediaID(t *testing.T) {
	f := newSubtitleFixture(t)
	// m-g has no file in media-movies, which is what makes the handler fall
	// back to media-subtitles GetMedia(movie id). media-subtitles ids are its
	// own namespace; here its media row "m-g" belongs to another item's file.
	f.subs.media["m-g"] = &subtv1.SubtitleMediaItem{Id: "m-g", Title: "Another title", MediaFileId: "file-m-r", MediaType: "movie"}

	// Unrestricted compatibility is kept (the fallback still runs).
	res := f.get("/api/movies/m-g/subtitles", f.adult)
	if res.status != http.StatusOK || !strings.Contains(res.body, "sub-r-1") {
		t.Fatalf("unrestricted fallback changed: %d %s", res.status, res.body)
	}
	f.subs.listed = nil

	res = f.get("/api/movies/m-g/subtitles", f.kid)
	if res.status != http.StatusOK {
		t.Fatalf("%d %s", res.status, res.body)
	}
	if strings.Contains(res.body, "sub-r-1") || strings.Contains(res.body, "file-m-r") || f.subs.askedAbout("file-m-r") {
		t.Fatalf("a restricted caller was handed another item's subtitle files: %s", res.body)
	}
	// The owning module's own file mapping still works for a restricted caller.
	res = f.get("/api/movies/m-pg/subtitles", f.kid)
	if !strings.Contains(res.body, "sub-pg-1") || strings.Contains(res.body, "sub-r-1") {
		t.Fatalf("m-pg subtitles: %s", res.body)
	}
}

func TestMovieSubtitleRowsOfAnotherFileAreDropped(t *testing.T) {
	f := newSubtitleFixture(t)
	f.subs.rows["file-m-pg"] = append(f.subs.rows["file-m-pg"], &subtv1.SubtitleFile{Id: "sub-stray", MediaFileId: "file-m-r"})
	for tok, wantStray := range map[string]bool{f.kid: false, f.adult: true} {
		res := f.get("/api/movies/m-pg/subtitles", tok)
		if res.status != http.StatusOK || !strings.Contains(res.body, "sub-pg-1") {
			t.Fatalf("%d %s", res.status, res.body)
		}
		if strings.Contains(res.body, "sub-stray") != wantStray {
			t.Fatalf("stray row visible=%v, want %v: %s", !wantStray, wantStray, res.body)
		}
	}
}

func TestTVSubtitlesKeepToTheAuthorizedSeries(t *testing.T) {
	f := newSubtitleFixture(t)
	f.subs.series["series1"] = []*subtv1.SubtitleMediaItem{
		{Id: "med-1", SeriesId: "series1", MediaFileId: "ef-ok", Title: "Pilot", Season: 1, Episode: 1},
		{Id: "med-2", SeriesId: "s-bad", MediaFileId: "ef-bad", Title: "Stray"}, // not this series
	}
	f.subs.rows["ef-ok"] = []*subtv1.SubtitleFile{
		{Id: "sub-ok", MediaFileId: "ef-ok"},
		{Id: "sub-stray-row", MediaFileId: "ef-bad"}, // row of another file
	}
	f.subs.rows["ef-bad"] = []*subtv1.SubtitleFile{{Id: "sub-bad", MediaFileId: "ef-bad"}}

	res := f.get("/api/tv/series1/subtitles", f.kid)
	if res.status != http.StatusOK || !strings.Contains(res.body, "sub-ok") {
		t.Fatalf("%d %s", res.status, res.body)
	}
	for _, leak := range []string{"sub-bad", "sub-stray-row", "ef-bad", "Stray"} {
		if strings.Contains(res.body, leak) {
			t.Fatalf("%q leaked to a restricted caller: %s", leak, res.body)
		}
	}
	// Unrestricted behaviour is unchanged: the same module answer, unfiltered.
	if res = f.get("/api/tv/series1/subtitles", f.adult); !strings.Contains(res.body, "sub-bad") || !strings.Contains(res.body, "ef-bad") {
		t.Fatalf("unrestricted answer changed: %s", res.body)
	}

	// The GetMedia fallback treats the series id as a media-subtitles media.id.
	f.subs.series["series1"] = nil
	f.subs.media["series1"] = &subtv1.SubtitleMediaItem{Id: "series1", MediaFileId: "ef-bad", Title: "Elsewhere"}
	if res = f.get("/api/tv/series1/subtitles", f.adult); !strings.Contains(res.body, "ef-bad") {
		t.Fatalf("unrestricted fallback changed: %s", res.body)
	}
	f.subs.listed = nil
	res = f.get("/api/tv/series1/subtitles", f.kid)
	if res.status != http.StatusOK || strings.Contains(res.body, "ef-bad") || f.subs.askedAbout("ef-bad") {
		t.Fatalf("restricted fallback must not map the series id to a media row: %d %s", res.status, res.body)
	}
}
