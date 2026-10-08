package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

// The gate names the item from src; any src form it cannot read as exactly one
// item is refused before the handler runs.
func TestSubtitleListRefusesAmbiguousSrc(t *testing.T) {
	f := newSubtitleFixture(t)
	for _, src := range []string{"/stream/movies/m-pg?x=1", "/stream/movies/m-pg#frag", "/stream/movies/m-pg/", "/stream/movies/m-pg/../m-r", ""} {
		assertBlocked(t, "src "+src, f.get("/api/playback/subtitles?src="+url.QueryEscape(src), f.kid))
	}
}

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
