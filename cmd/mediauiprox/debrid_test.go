package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHandleDebridUnavailableWhenUnset(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleDebridAdd(w, httptest.NewRequest(http.MethodPost, "/api/debrid/add", strings.NewReader(`{"url":"magnet:foo"}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleDebridAddProxiesUpstream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/add" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "magnet:test") {
			t.Fatalf("body=%s", string(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"rd-99"}`))
	}))
	defer up.Close()

	u, _ := url.Parse(up.URL)
	s := &server{debridHTTP: u}
	w := httptest.NewRecorder()
	s.handleDebridAdd(w, httptest.NewRequest(http.MethodPost, "/api/debrid/add", strings.NewReader(`{"url":"magnet:test"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "rd-99" {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleDebridVFSProxiesQuery(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vfs" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("path") != "/Movies" {
			t.Fatalf("path=%s", r.URL.Query().Get("path"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer up.Close()

	u, _ := url.Parse(up.URL)
	s := &server{debridHTTP: u}
	w := httptest.NewRecorder()
	s.handleDebridVFS(w, httptest.NewRequest(http.MethodGet, "/api/debrid/vfs?path=/Movies", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleDebridStreamForwardsRange(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vfs/stream" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Range") != "bytes=0-1023" {
			t.Fatalf("range=%s", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("chunk"))
	}))
	defer up.Close()

	u, _ := url.Parse(up.URL)
	s := &server{debridHTTP: u}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/debrid/stream?id=rd-1", nil)
	req.Header.Set("Range", "bytes=0-1023")
	s.handleDebridStream(w, req)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("status %d", w.Code)
	}
	if w.Body.String() != "chunk" {
		t.Fatalf("body=%s", w.Body.String())
	}
}
