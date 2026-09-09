package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestHandleAcquisitionReady(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{
		acquisitionPeers: []acquisitionPeerDef{
			{ID: "indexer-piratebay", Kind: "indexer", Label: "Pirate Bay indexer", URL: u},
			{ID: "downloader-native-torrent", Kind: "downloader", Label: "Native torrent", URL: u},
		},
	}
	w := httptest.NewRecorder()
	s.handleAcquisition(w, httptest.NewRequest(http.MethodGet, "/api/acquisition", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Ready         bool `json:"ready"`
		HasIndexer    bool `json:"hasIndexer"`
		HasDownloader bool `json:"hasDownloader"`
		Peers         []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Live bool   `json:"live"`
		} `json:"peers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Ready || !body.HasIndexer || !body.HasDownloader {
		t.Fatalf("expected ready with both peers, got %#v", body)
	}
	if len(body.Peers) != 2 || !body.Peers[0].Live || !body.Peers[1].Live {
		t.Fatalf("expected both peers live, got %#v", body.Peers)
	}
}

func TestHandleAcquisitionIndexerOnly(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)
	live, _ := url.Parse(up.URL)
	down, _ := url.Parse("http://127.0.0.1:1")
	s := &server{
		acquisitionPeers: []acquisitionPeerDef{
			{ID: "indexer-piratebay", Kind: "indexer", Label: "Pirate Bay indexer", URL: live},
			{ID: "downloader-qbittorrent", Kind: "downloader", Label: "qBittorrent", URL: down},
		},
	}
	w := httptest.NewRecorder()
	s.handleAcquisition(w, httptest.NewRequest(http.MethodGet, "/api/acquisition", nil))
	var body struct {
		Ready         bool   `json:"ready"`
		HasIndexer    bool   `json:"hasIndexer"`
		HasDownloader bool   `json:"hasDownloader"`
		Message       string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || !body.HasIndexer || body.HasDownloader {
		t.Fatalf("expected indexer-only, got %#v", body)
	}
	if body.Message == "" {
		t.Fatal("expected a household message")
	}
}

func TestAcquisitionReportsLiveGrabPolicy(t *testing.T) {
	t.Setenv("DOWNLOADER_ENGINE", "")
	t.Setenv("WG_CONF", "")
	s := &server{}
	w := httptest.NewRecorder()
	s.handleAcquisition(w, httptest.NewRequest(http.MethodGet, "/api/acquisition", nil))
	var body struct {
		LiveGrabAllowed bool   `json:"live_grab_allowed"`
		DownloaderMode  string `json:"downloader_mode"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.LiveGrabAllowed || body.DownloaderMode != "fixture" {
		t.Fatalf("%#v", body)
	}

	t.Setenv("DOWNLOADER_ENGINE", "live")
	w = httptest.NewRecorder()
	s.handleAcquisition(w, httptest.NewRequest(http.MethodGet, "/api/acquisition", nil))
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.LiveGrabAllowed || body.DownloaderMode != "live" {
		t.Fatalf("live without WG should block grab: %#v", body)
	}
}

func TestHandleAcquisitionMethodNotAllowed(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleAcquisition(w, httptest.NewRequest(http.MethodPost, "/api/acquisition", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", w.Code)
	}
}

func TestCapabilitiesAcquisitionReady(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{
		movies: stubMoviesClient{},
		tv:     stubTVClient{},
		acquisitionPeers: []acquisitionPeerDef{
			{ID: "idx", Kind: "indexer", Label: "Indexer", URL: u},
			{ID: "dl", Kind: "downloader", Label: "Downloader", URL: u},
		},
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
	if !body.Features["acquisition"] {
		t.Fatalf("expected acquisition true when indexer+downloader live, got %#v", body.Features)
	}

	down := &server{
		movies:       stubMoviesClient{},
		tv:           stubTVClient{},
		libraryPaths: newLibraryPathsStore("", t.TempDir()),
	}
	downW := httptest.NewRecorder()
	down.handleCapabilities(downW, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var downBody struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.NewDecoder(downW.Body).Decode(&downBody); err != nil {
		t.Fatal(err)
	}
	if downBody.Features["acquisition"] {
		t.Fatalf("expected acquisition false without peers, got %#v", downBody.Features)
	}
}
