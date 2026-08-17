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
