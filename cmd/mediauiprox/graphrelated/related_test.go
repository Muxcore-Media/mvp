package graphrelated

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRelatedUnavailableWhenUnset(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/related?id=tmdb:movie:550", nil)
	Handle(nil, "")(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got relatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Available || len(got.Items) != 0 {
		t.Fatalf("want unavailable empty, got %+v", got)
	}
}

func TestRelatedUnavailableWhenUnreachable(t *testing.T) {
	u, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/related?id=tmdb:movie:550", nil)
	Handle(u, "")(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got relatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Fatalf("want available=false on unreachable graph")
	}
}

func TestRelatedUnavailableOnUpstreamError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/related?id=tmdb:tv:1396", nil)
	Handle(u, "")(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got relatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Available || len(got.Items) != 0 {
		t.Fatalf("want unavailable empty, got %+v", got)
	}
}

func TestRelatedRejectsInvalidID(t *testing.T) {
	for _, id := range []string{"", "550", "tmdb:movie:", "tmdb:book:1", "tmdb:movie:0"} {
		rr := httptest.NewRecorder()
		q := "/api/graph/related"
		if id != "" {
			q += "?id=" + url.QueryEscape(id)
		}
		req := httptest.NewRequest(http.MethodGet, q, nil)
		Handle(nil, "")(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("id %q: status %d", id, rr.Code)
		}
	}
}

func TestRelatedHappyPathMapsGraphNodes(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"related": [
				{
					"Node": {
						"ID": "gn_1",
						"Kind": "movie",
						"Title": "Se7en",
						"ExternalID": "tmdb:movie:807",
						"Attrs": {
							"year": "1995",
							"tmdb_id": "807",
							"overview": "Two detectives.",
							"poster": "/se7en.jpg",
							"vote_avg": "8.3",
							"content_rating": "R"
						}
					},
					"Rel": "similar",
					"Weight": 0.9
				},
				{
					"node": {
						"id": "gn_2",
						"kind": "series",
						"title": "Breaking Bad",
						"external_id": "tmdb:tv:1396",
						"attrs": {"year": "2008"}
					},
					"rel": "similar"
				}
			]
		}`)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/related?id=tmdb:movie:550&limit=12", nil)
	Handle(u, "secret-token")(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if gotPath != "/api/graph/related" {
		t.Fatalf("upstream path %q", gotPath)
	}
	q, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("external_id") != "tmdb:movie:550" {
		t.Fatalf("external_id %q", q.Get("external_id"))
	}
	if q.Get("limit") != "12" {
		t.Fatalf("limit %q", q.Get("limit"))
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("auth %q", gotAuth)
	}

	var got relatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available {
		t.Fatal("want available")
	}
	if len(got.Items) != 2 {
		t.Fatalf("items %+v", got.Items)
	}
	m := got.Items[0]
	if m.ID != 807 || m.Title != "Se7en" || m.Year != 1995 || m.MediaType != "movie" {
		t.Fatalf("movie item %+v", m)
	}
	if m.Overview != "Two detectives." || m.Poster != "/se7en.jpg" || m.VoteAvg != 8.3 {
		t.Fatalf("movie extras %+v", m)
	}
	if m.Relation != "similar" || m.ContentRating != "R" {
		t.Fatalf("movie relation/rating %+v", m)
	}
	tv := got.Items[1]
	if tv.ID != 1396 || tv.MediaType != "tv" || tv.Title != "Breaking Bad" || tv.Year != 2008 {
		t.Fatalf("tv item %+v", tv)
	}
}

func TestRelatedAcceptsExternalIDQuery(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("external_id") != "tmdb:tv:1396" {
			t.Errorf("external_id %q", r.URL.Query().Get("external_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"related":[]}`)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/related?external_id=tmdb:tv:1396", nil)
	Handle(u, "")(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got relatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available {
		t.Fatal("empty related from a live graph should stay available")
	}
}

func TestRelatedGraph404IsAvailableEmpty(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/related?id=tmdb:movie:550", nil)
	Handle(u, "")(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got relatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Items) != 0 {
		t.Fatalf("404 from graph should be available empty, got %+v", got)
	}
}

func TestRelatedRejectsNonGET(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/graph/related?id=tmdb:movie:550", strings.NewReader("{}"))
	Handle(nil, "")(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", rr.Code)
	}
}
