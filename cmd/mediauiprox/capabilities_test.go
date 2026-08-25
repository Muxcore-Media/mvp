package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

func TestCapabilitiesOptionalLibrariesDown(t *testing.T) {
	s := &server{
		movies:       stubMoviesClient{},
		tv:           stubTVClient{},
		requestHTTP:  mustURL("http://127.0.0.1:1"),
		musicHTTP:    mustURL("http://127.0.0.1:1"),
		booksHTTP:    mustURL("http://127.0.0.1:1"),
		livetv:       newLiveTVStore("", t.TempDir()),
		libraryPaths: newLibraryPathsStore("", t.TempDir()),
		quickconnect: newQuickConnectStore(t.TempDir()),
		userdata:     newServerUserdata(t.TempDir()),
	}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Libraries map[string]bool `json:"libraries"`
		Features  map[string]bool `json:"features"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Libraries["movies"] || !body.Libraries["tv"] {
		t.Fatalf("expected core libraries true, got %#v", body.Libraries)
	}
	for _, k := range []string{"music", "books", "comics", "audiobooks"} {
		if body.Libraries[k] {
			t.Fatalf("expected %s false when upstream down", k)
		}
	}
	if body.Libraries["homevideos"] || body.Libraries["musicvideos"] {
		t.Fatalf("expected companion libraries false without paths, got %#v", body.Libraries)
	}
	if body.Features["livetv"] != true || body.Features["quickconnect"] != true {
		t.Fatalf("expected livetv/quickconnect true, got %#v", body.Features)
	}
}

func TestCapabilitiesOptionalLibrariesUp(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/artists", "/api/authors", "/api/series", "/api/audiobooks":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)

	reqMod := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(reqMod.Close)

	u, _ := url.Parse(up.URL)
	reqURL, _ := url.Parse(reqMod.URL)
	s := &server{
		movies:         stubMoviesClient{},
		tv:             stubTVClient{},
		requestHTTP:    reqURL,
		musicHTTP:      u,
		booksHTTP:      u,
		comicsHTTP:     u,
		audiobooksHTTP: u,
		livetv:         newLiveTVStore("", t.TempDir()),
		libraryPaths:   newLibraryPathsStore("", t.TempDir()),
		quickconnect:   newQuickConnectStore(t.TempDir()),
		userdata:       newServerUserdata(t.TempDir()),
	}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var body struct {
		Libraries map[string]bool `json:"libraries"`
		Features  map[string]bool `json:"features"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"music", "books", "comics", "audiobooks"} {
		if !body.Libraries[k] {
			t.Fatalf("expected %s true when upstream live", k)
		}
	}
	if !body.Features["search"] || !body.Features["mixed"] {
		t.Fatalf("expected search/mixed true, got %#v", body.Features)
	}
}

func TestCapabilitiesRequestModuleSearchFallback(t *testing.T) {
	reqMod := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			http.NotFound(w, r)
		case "/api/search":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(reqMod.Close)

	reqURL, _ := url.Parse(reqMod.URL)
	s := &server{
		movies:       stubMoviesClient{},
		tv:           stubTVClient{},
		requestHTTP:  reqURL,
		livetv:       newLiveTVStore("", t.TempDir()),
		libraryPaths: newLibraryPathsStore("", t.TempDir()),
		quickconnect: newQuickConnectStore(t.TempDir()),
		userdata:     newServerUserdata(t.TempDir()),
	}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var body struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Features["search"] || !body.Features["request"] {
		t.Fatalf("expected search/request true via /api/search fallback, got %#v", body.Features)
	}
}

func TestCapabilitiesCompanionLibrariesFromPathsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library-paths.json")
	if err := os.WriteFile(path, []byte(`{"homevideos":["/data/home"],"musicvideos":["/data/mv"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &server{
		movies:       stubMoviesClient{},
		tv:           stubTVClient{},
		libraryPaths: newLibraryPathsStore(path, ""),
	}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var body struct {
		Libraries map[string]bool `json:"libraries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Libraries["homevideos"] || !body.Libraries["musicvideos"] {
		t.Fatalf("expected companion libraries true, got %#v", body.Libraries)
	}
}

func TestCapabilitiesWatchlistWhenListSyncLive(t *testing.T) {
	s := &server{
		movies:       stubMoviesClient{},
		tv:           stubTVClient{},
		listSync:     dialListSyncFixture(t),
		libraryPaths: newLibraryPathsStore("", t.TempDir()),
	}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var body struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Features["watchlist"] {
		t.Fatalf("expected watchlist true when list-sync live, got %#v", body.Features)
	}
}

// stubMoviesClient / stubTVClient satisfy non-nil checks in handleCapabilities.
type stubMoviesClient struct {
	mgmntv1.MovieManagementServiceClient
}
type stubTVClient struct {
	tvmgmtv1.TvManagementServiceClient
}
