package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestNormalizeLibraryKey(t *testing.T) {
	cases := map[string]string{
		"musicvideos":  "musicvideos",
		"Music Videos": "musicvideos",
		"music-video":  "musicvideos",
		"homevideos":   "homevideos",
		"home_video":   "homevideos",
		"movies":       "",
	}
	for in, want := range cases {
		if got := normalizeLibraryKey(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestMovieMatchesLibraryPathPrefix(t *testing.T) {
	m := &mgmntv1.MovieItem{
		Id: "1", Title: "Random Clip", RootFolderPath: "/data/library/Music Videos/Artist",
	}
	prefixes := []string{"/data/library/Music Videos"}
	if !movieMatchesLibrary(m, "musicvideos", prefixes, false) {
		t.Fatal("expected path prefix match")
	}
	if movieMatchesLibrary(m, "homevideos", nil, false) {
		t.Fatal("should not match homevideos with music path")
	}
	if movieMatchesLibrary(m, "homevideos", []string{"/data/library/Home Videos"}, false) {
		t.Fatal("should not match homevideos prefixes against music path")
	}
}

func TestMovieMatchesLibraryHeuristicOnlyWhenConfigEmpty(t *testing.T) {
	m := &mgmntv1.MovieItem{
		Id: "2", Title: "Artist - Official Music Video", Genres: []string{"Music"},
	}
	if !movieMatchesLibrary(m, "musicvideos", nil, true) {
		t.Fatal("heuristic should match when config empty")
	}
	if movieMatchesLibrary(m, "musicvideos", []string{"/data/musicvideos"}, false) {
		t.Fatal("heuristic must not apply when config prefixes present")
	}
}

func TestMovieMatchesLibraryRootFolderName(t *testing.T) {
	m := &mgmntv1.MovieItem{
		Id: "3", Title: "Vacation 2019", RootFolderPath: "/mnt/media/Home Videos",
	}
	if !movieMatchesLibrary(m, "homevideos", nil, false) {
		t.Fatal("root folder name should match without config")
	}
}

func TestLibraryPathsStoreLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library-paths.json")
	raw := `{"musicvideos":["/a/Music Videos","/a/Music Videos"],"homevideos":["/b/homevideos"]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newLibraryPathsStore(path, "")
	mv := st.prefixes("musicvideos")
	if len(mv) != 1 || mv[0] != "/a/Music Videos" {
		t.Fatalf("musicvideos prefixes=%v", mv)
	}
	hv := st.prefixes("homevideos")
	if len(hv) != 1 || hv[0] != "/b/homevideos" {
		t.Fatalf("homevideos prefixes=%v", hv)
	}
}

func TestLiveTVStorePersistAndGuide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "livetv.json")
	st := newLiveTVStore(path, "")
	now := time.Now().UTC()
	f := liveTVFile{
		Channels: []liveTVChannel{
			{ID: "ch1", Name: "News", Number: "1", Category: "Info", URL: "http://example/stream"},
		},
		Guide: []liveTVGuideRow{
			{
				ChannelID: "ch1", Title: "Evening News",
				Start: now.Add(-15 * time.Minute).Format(time.RFC3339),
				End:   now.Add(45 * time.Minute).Format(time.RFC3339),
			},
		},
		Recordings: []liveTVRecording{
			{ID: "r1", ChannelID: "ch1", Title: "Past show", Start: now.Add(-3 * time.Hour).Format(time.RFC3339), End: now.Add(-2 * time.Hour).Format(time.RFC3339), Status: "completed", Path: "/rec/past.ts"},
		},
		Timers: []liveTVTimer{},
	}
	if err := st.save(f); err != nil {
		t.Fatal(err)
	}
	loaded := st.load()
	if len(loaded.Channels) != 1 || loaded.Channels[0].Name != "News" {
		t.Fatalf("channels=%#v", loaded.Channels)
	}
	if len(loaded.Guide) != 1 || loaded.Guide[0].Title != "Evening News" {
		t.Fatalf("guide=%#v", loaded.Guide)
	}
	np := guideNowPlaying(loaded.Guide, "ch1", now)
	if np == nil || np.Title != "Evening News" {
		t.Fatalf("now_playing=%#v", np)
	}

	s := &server{livetv: st}
	req := httptest.NewRequest(http.MethodGet, "/api/livetv", nil)
	w := httptest.NewRecorder()
	s.handleLiveTV(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["available"] != true {
		t.Fatalf("available=%v", body["available"])
	}
	guide, _ := body["guide"].([]any)
	if len(guide) != 1 {
		t.Fatalf("guide=%v", body["guide"])
	}
	channels, _ := body["channels"].([]any)
	if len(channels) != 1 {
		t.Fatalf("channels=%v", body["channels"])
	}
	ch0, _ := channels[0].(map[string]any)
	npMap, _ := ch0["now_playing"].(map[string]any)
	if npMap["title"] != "Evening News" {
		t.Fatalf("channel now_playing=%v", ch0["now_playing"])
	}
	recs, _ := body["recordings"].([]any)
	if len(recs) != 1 {
		t.Fatalf("recordings=%v", body["recordings"])
	}
}

func TestLiveTVTimerPersists(t *testing.T) {
	dir := t.TempDir()
	st := newLiveTVStore(filepath.Join(dir, "livetv.json"), "")
	_ = st.save(liveTVFile{
		Channels: []liveTVChannel{{ID: "ch1", Name: "A", Number: "1"}},
	})
	s := &server{livetv: st}
	req := httptest.NewRequest(http.MethodPost, "/api/livetv/timers", strings.NewReader(
		`{"channel_id":"ch1","title":"Fixture timer","series":true}`,
	))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleLiveTVTimer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	loaded := st.load()
	if len(loaded.Timers) != 1 || loaded.Timers[0].Title != "Fixture timer" || !loaded.Timers[0].Series {
		t.Fatalf("timers=%#v", loaded.Timers)
	}
}

func TestLiveTVDefaultsUnderUserdataDir(t *testing.T) {
	dir := t.TempDir()
	st := newLiveTVStore("", dir)
	want := filepath.Join(dir, "livetv.json")
	if st.path != want {
		t.Fatalf("path=%q want %q", st.path, want)
	}
}
