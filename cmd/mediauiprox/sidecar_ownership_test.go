package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
)

func ownedSidecars(t *testing.T, dir, video string) []string {
	t.Helper()
	var names []string
	for _, tr := range discoverSidecarSubtitles(filepath.Join(dir, video), true) {
		p, ok := decodeSidecarTrackID(tr.ID)
		if !ok {
			t.Fatalf("not a sidecar id: %s", tr.ID)
		}
		names = append(names, filepath.Base(p))
	}
	sort.Strings(names)
	return names
}

func touchAll(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		writeFixtureFile(t, filepath.Join(dir, n), "x")
	}
}

func TestSidecarOwnership(t *testing.T) {
	cases := []struct {
		name   string
		files  []string
		video  string
		want   []string
		unrest []string // what the unrestricted listing keeps offering
	}{
		{
			name:   "flat folder: a sequel's sidecars are not the original's",
			files:  []string{"Alien.mkv", "Alien.Resurrection.mkv", "Alien.en.srt", "Alien.Resurrection.en.srt", "Alien 2.srt", "Alien.srt"},
			video:  "Alien.mkv",
			want:   []string{"Alien.en.srt", "Alien.srt"},
			unrest: []string{"Alien 2.srt", "Alien.Resurrection.en.srt", "Alien.en.srt", "Alien.srt"},
		},
		{
			name:  "flat folder: the sequel owns its own",
			files: []string{"Alien.mkv", "Alien.Resurrection.mkv", "Alien.en.srt", "Alien.Resurrection.en.srt", "Alien 2.srt"},
			video: "Alien.Resurrection.mkv",
			want:  []string{"Alien.Resurrection.en.srt"},
		},
		{
			name:  "no competing video: an unrelated suffix is still not proof",
			files: []string{"Alien.mkv", "Alien.Resurrection.en.srt", "Alien 2.srt", "Alien-extras.srt", "Alien_en.srt", "Alien.one.srt"},
			video: "Alien.mkv",
			want:  nil,
		},
		{
			name: "language, region, forced, sdh and stacked tags",
			files: []string{"Movie.mkv", "Movie.en.srt", "Movie.eng.srt", "Movie.pt-BR.srt", "Movie.zh_Hans.srt", "Movie.en.forced.srt",
				"Movie.fr.sdh.ass", "Movie.EN.VTT", "Movie.srt", "Movie.forced.srt", "Movie.xx.srt", "Movie.en.extra.srt"},
			video: "Movie.mkv",
			want: []string{"Movie.EN.VTT", "Movie.en.forced.srt", "Movie.en.srt", "Movie.eng.srt", "Movie.forced.srt",
				"Movie.fr.sdh.ass", "Movie.pt-BR.srt", "Movie.srt", "Movie.zh_Hans.srt"},
		},
		{
			name:  "release names with dots and a year",
			files: []string{"Fight.Club.1999.1080p.mkv", "Fight.Club.1999.1080p.eng.srt", "Fight.Club.1999.eng.srt", "Fight.Club.1999.1080p.eng.forced.srt"},
			video: "Fight.Club.1999.1080p.mkv",
			want:  []string{"Fight.Club.1999.1080p.eng.forced.srt", "Fight.Club.1999.1080p.eng.srt"},
		},
		{
			name:  "tv episodes",
			files: []string{"Show - S01E01 - Pilot.mkv", "Show - S01E01 - Pilot.en.srt", "Show - S01E10 - Ten.mkv", "Show - S01E10 - Ten.en.srt", "Show - S01E1.en.srt"},
			video: "Show - S01E01 - Pilot.mkv",
			want:  []string{"Show - S01E01 - Pilot.en.srt"},
		},
		{
			name:  "tv: an episode number that is a prefix of another",
			files: []string{"Show S01E1.mkv", "Show S01E10.mkv", "Show S01E10.en.srt", "Show S01E1.en.srt"},
			video: "Show S01E1.mkv",
			want:  []string{"Show S01E1.en.srt"},
		},
		{
			name:  "the more specific stem wins a language-looking name",
			files: []string{"Show.mkv", "Show.en.mkv", "Show.en.srt", "Show.fr.srt"},
			video: "Show.mkv",
			want:  []string{"Show.fr.srt"},
		},
		{
			name:  "the more specific video owns it",
			files: []string{"Show.mkv", "Show.en.mkv", "Show.en.srt", "Show.fr.srt"},
			video: "Show.en.mkv",
			want:  []string{"Show.en.srt"},
		},
		{
			name:  "same stem under two containers: ambiguous, nobody is granted",
			files: []string{"Movie.mkv", "Movie.mp4", "Movie.en.srt"},
			video: "Movie.mkv",
			want:  nil,
		},
		{
			name:  "case-only difference is ambiguous",
			files: []string{"Movie.mkv", "MOVIE.mkv", "Movie.en.srt"},
			video: "Movie.mkv",
			want:  nil,
		},
		{
			name:  "a non-video neighbour is not a competitor",
			files: []string{"Movie.mkv", "Movie.en.nfo", "Movie.en.srt", "Movie.en.jpg"},
			video: "Movie.mkv",
			want:  []string{"Movie.en.srt"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			touchAll(t, dir, tc.files...)
			got := ownedSidecars(t, dir, tc.video)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("restricted: got %q want %q", got, tc.want)
			}
			// The unrestricted listing keeps today's lenient behaviour.
			if tc.unrest != nil {
				var un []string
				for _, tr := range discoverSidecarSubtitles(filepath.Join(dir, tc.video), false) {
					p, _ := decodeSidecarTrackID(tr.ID)
					un = append(un, filepath.Base(p))
				}
				sort.Strings(un)
				if strings.Join(un, "|") != strings.Join(tc.unrest, "|") {
					t.Errorf("unrestricted: got %q want %q", un, tc.unrest)
				}
			}
		})
	}
}

// The MF1 reproduction through the HTTP routes: two items share a flat folder.
func TestSidecarOwnershipFlatFolderOverHTTP(t *testing.T) {
	f := newSubtitleFixture(t)
	flat := filepath.Join(f.dir, "flat")
	writeFixtureFile(t, filepath.Join(flat, "Alien.mkv"), "video")
	writeFixtureFile(t, filepath.Join(flat, "Alien.Resurrection.mkv"), "video")
	writeFixtureFile(t, filepath.Join(flat, "Alien.en.srt"), fmt.Sprintf(srtBody, "Hello Alien"))
	writeFixtureFile(t, filepath.Join(flat, "Alien.Resurrection.en.srt"), fmt.Sprintf(srtBody, "Hello Resurrection"))
	writeFixtureFile(t, filepath.Join(flat, "Alien 2.srt"), fmt.Sprintf(srtBody, "Hello Alien 2"))
	// m-g is allowed for the kid; m-r is denied.
	files := f.s.movies.(*subtitleMovies).files
	files["m-g"] = filepath.Join(flat, "Alien.mkv")
	files["m-r"] = filepath.Join(flat, "Alien.Resurrection.mkv")
	kid := f.kid

	tracks := f.tracks(t, "/stream/movies/m-g", kid)
	var sidecars []string
	for _, tr := range tracks {
		if p, ok := decodeSidecarTrackID(tr.ID); ok {
			sidecars = append(sidecars, filepath.Base(p))
		}
	}
	if len(sidecars) != 1 || sidecars[0] != "Alien.en.srt" {
		t.Fatalf("allowed item's sidecars = %v", sidecars)
	}
	if res := f.get("/api/playback/subtitles/"+sidecarTrackID(filepath.Join(flat, "Alien.en.srt")), kid); res.status != http.StatusOK || !strings.Contains(res.body, "Hello Alien") {
		t.Fatalf("own sidecar: %d %s", res.status, res.body)
	}
	for _, other := range []string{"Alien.Resurrection.en.srt", "Alien 2.srt"} {
		assertBlocked(t, "foreign sidecar "+other, f.get("/api/playback/subtitles/"+url.PathEscape(sidecarTrackID(filepath.Join(flat, other))), kid))
	}
	assertBlocked(t, "denied item's listing", f.get("/api/playback/subtitles?src="+url.QueryEscape("/stream/movies/m-r"), kid))

	// An unrestricted principal still gets the lenient listing.
	req, rec := newSubtitleListRequest("/stream/movies/m-g")
	f.s.handlePlaybackSubtitlesList(rec, req)
	for _, want := range []string{"Alien.en.srt", "Alien.Resurrection.en.srt", "Alien 2.srt"} {
		if !strings.Contains(decodeAllSidecarNames(t, rec.Body.Bytes()), want) {
			t.Errorf("unrestricted listing lost %s: %s", want, rec.Body.String())
		}
	}
}

// R1: the subtitle service names the media file of every row. A restricted
// listing offers and binds only rows that name the file it asked about.
func TestModuleSubtitleRowOfAnotherFileIsNotOffered(t *testing.T) {
	f := newSubtitleFixture(t)
	f.subs.rows["file-m-pg"] = []*subtv1.SubtitleFile{
		{Id: "sub-pg-1", MediaFileId: "file-m-pg", Language: "eng"},
		{Id: "sub-wrong", MediaFileId: "file-m-r", Language: "eng"},
		{Id: "sub-nofile", Language: "eng"},
	}
	kid := f.kid

	ids := trackIDs(f.tracks(t, "/stream/movies/m-pg", kid))
	has := func(id string) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	if !has("sub-pg-1") || has("sub-wrong") || has("sub-nofile") {
		t.Fatalf("restricted tracks = %v", ids)
	}
	sid := sessionID(kid)
	for _, id := range []string{"sub-wrong", "sub-nofile"} {
		if _, ok := f.s.parental.subtitles.lookup(sid, id, f.clock.Now()); ok {
			t.Errorf("%s was bound", id)
		}
		assertBlocked(t, "row of another file "+id, f.get("/api/playback/subtitles/"+id, kid))
	}
	if _, ok := f.s.parental.subtitles.lookup(sid, "sub-pg-1", f.clock.Now()); !ok {
		t.Error("the correct row was not bound")
	}

	// Unrestricted behaviour is unchanged: the row is still offered.
	req, rec := newSubtitleListRequest("/stream/movies/m-pg")
	f.s.handlePlaybackSubtitlesList(rec, req)
	if !strings.Contains(rec.Body.String(), "sub-wrong") {
		t.Fatalf("unrestricted listing changed: %s", rec.Body.String())
	}
}

func newSubtitleListRequest(src string) (*http.Request, *httptest.ResponseRecorder) {
	return httptest.NewRequest(http.MethodGet, "/api/playback/subtitles?src="+url.QueryEscape(src), nil), httptest.NewRecorder()
}

// decodeAllSidecarNames joins the file names behind every sidecar track of a
// list response.
func decodeAllSidecarNames(t *testing.T, body []byte) string {
	t.Helper()
	var tl trackList
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tr := range tl.Tracks {
		if p, ok := decodeSidecarTrackID(tr.ID); ok {
			names = append(names, filepath.Base(p))
		}
	}
	return strings.Join(names, "|")
}

// The tag grammar is its own boundary: the lenient prefix filter of the
// unrestricted listing is not what keeps these out.
func TestSidecarExtraTags(t *testing.T) {
	for stem, want := range map[string]bool{
		"Alien": true, "Alien.en": true, "Alien.en.forced": true, "Alien.pt-BR": true,
		"Alien_en": false, "Alien-en": false, "Alien en": false, "Alien 2": false, "Alien.2": false,
		"Alien.": false, "Alien..en": false, "Alien.en.": false, "Alien.Resurrection": false,
		"Alien.pt-": false, "Alien.pt-BRAZIL": false, "Aliens": false, "Alie": false,
	} {
		if _, ok := sidecarExtraTags(stem, "Alien", false); ok != want {
			t.Errorf("%q: owned=%v want %v", stem, ok, want)
		}
	}
	if _, ok := sidecarExtraTags("anything", "", false); ok {
		t.Error("an empty video stem owns nothing")
	}
}
