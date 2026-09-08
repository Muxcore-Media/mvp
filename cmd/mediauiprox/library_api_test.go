package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLibraryListSoftWhenUpstreamDown(t *testing.T) {
	s := &server{
		musicHTTP: mustURL("http://127.0.0.1:1"),
	}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/music", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["available"] != false || body["coming_soon"] != true {
		t.Fatalf("expected soft coming_soon payload, got %#v", body)
	}
	if body["library"] != "music" {
		t.Fatalf("library=%v", body["library"])
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "Coming soon") {
		t.Fatalf("message=%q", msg)
	}
}

func TestLibraryListAvailableFromUpstream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/artists" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "ar1", "name": "Fixture Artist", "path": "/lib/Fixture"},
		})
	}))
	t.Cleanup(up.Close)

	u, _ := url.Parse(up.URL)
	s := &server{musicHTTP: u}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/music", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool             `json:"available"`
		Items     []map[string]any `json:"items"`
		Total     int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Total != 1 || body.Items[0]["name"] != "Fixture Artist" {
		t.Fatalf("unexpected %#v", body)
	}
}

func TestAudiobookListRewritesStreamThroughBFF(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/audiobooks" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":       "ab1",
				"title":    "Project Hail Mary",
				"narrator": "Ray Porter",
				"files": []map[string]any{
					{"id": "f1", "title": "Part 1", "stream_url": "/api/files/f1/stream"},
					{"id": "f2", "title": "Part 2", "stream_url": "/api/files/f2/stream"},
				},
			},
		})
	}))
	t.Cleanup(up.Close)

	u, _ := url.Parse(up.URL)
	s := &server{audiobooksHTTP: u}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/audiobooks", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool             `json:"available"`
		Items     []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 {
		t.Fatalf("unexpected %#v", body)
	}
	if body.Items[0]["stream_url"] != "/stream/audiobooks/f1" {
		t.Fatalf("list stream_url=%v", body.Items[0]["stream_url"])
	}
	files, _ := body.Items[0]["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files=%v", files)
	}
	f0, _ := files[0].(map[string]any)
	if f0["stream_url"] != "/stream/audiobooks/f1" {
		t.Fatalf("file stream_url=%v", f0["stream_url"])
	}
}

func TestAudiobookDetailAndStreamProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/audiobooks/ab1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"author": map[string]any{"id": "au1", "name": "Andy Weir"},
				"audiobook": map[string]any{
					"id":    "ab1",
					"title": "Project Hail Mary",
					"files": []map[string]any{
						{"id": "f1", "title": "Part 1", "stream_url": "/api/files/f1/stream"},
					},
				},
			})
		case "/api/files/f1/stream":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3fake"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)

	u, _ := url.Parse(up.URL)
	s := &server{audiobooksHTTP: u}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/audiobooks/ab1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status %d body %s", w.Code, w.Body.String())
	}
	var detail struct {
		Audiobook struct {
			StreamURL string `json:"stream_url"`
			Files     []struct {
				StreamURL string `json:"stream_url"`
			} `json:"files"`
		} `json:"audiobook"`
	}
	if err := json.NewDecoder(w.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Audiobook.StreamURL != "/stream/audiobooks/f1" {
		t.Fatalf("detail stream_url=%q", detail.Audiobook.StreamURL)
	}
	if len(detail.Audiobook.Files) != 1 || detail.Audiobook.Files[0].StreamURL != "/stream/audiobooks/f1" {
		t.Fatalf("detail files=%#v", detail.Audiobook.Files)
	}

	sreq := httptest.NewRequest(http.MethodGet, "/stream/audiobooks/f1", nil)
	sw := httptest.NewRecorder()
	mux.ServeHTTP(sw, sreq)
	if sw.Code != http.StatusOK {
		t.Fatalf("stream status %d", sw.Code)
	}
	if !strings.Contains(sw.Body.String(), "ID3fake") {
		t.Fatalf("stream body=%q", sw.Body.String())
	}
}

func TestComicSeriesDetailAndStreamProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/series/s1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"series": map[string]any{"id": "s1", "title": "Saga", "publisher": "Image", "monitored": true},
				"issues": []map[string]any{
					{"id": "i1", "series_id": "s1", "title": "Chapter One", "number": "1", "has_file": true, "path": "/comics/saga-1.cbz"},
					{"id": "i2", "series_id": "s1", "title": "Chapter Two", "number": "2", "has_file": false, "path": ""},
				},
			})
		case "/api/issues/i1/stream":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PKFAKE"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)

	u, _ := url.Parse(up.URL)
	s := &server{comicsHTTP: u}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/comics/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status %d body %s", w.Code, w.Body.String())
	}
	var detail struct {
		Series struct {
			Title string `json:"title"`
		} `json:"series"`
		Issues []struct {
			ID        string `json:"id"`
			StreamURL string `json:"stream_url"`
		} `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Series.Title != "Saga" {
		t.Fatalf("series %#v", detail.Series)
	}
	if len(detail.Issues) != 2 || detail.Issues[0].StreamURL != "/stream/comics/i1" {
		t.Fatalf("issues %#v", detail.Issues)
	}
	if detail.Issues[1].StreamURL != "" {
		t.Fatalf("missing issue should not stream %#v", detail.Issues[1])
	}

	sreq := httptest.NewRequest(http.MethodGet, "/stream/comics/i1", nil)
	sw := httptest.NewRecorder()
	mux.ServeHTTP(sw, sreq)
	if sw.Code != http.StatusOK {
		t.Fatalf("stream status %d", sw.Code)
	}
	if !strings.Contains(sw.Body.String(), "PKFAKE") {
		t.Fatalf("stream body=%q", sw.Body.String())
	}
}
