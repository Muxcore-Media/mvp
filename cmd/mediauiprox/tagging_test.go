package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleGetTaggingUnavailable(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/tagging", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleGetTagging(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Rules     []any `json:"rules"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Rules == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleGetTaggingForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleGetTagging(w, httptest.NewRequest(http.MethodGet, "/api/tagging", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleGetTaggingLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "t1", "name": "Kids", "category": "audience", "color": "#0f0"}})
		case "/api/rules":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "r1", "tag_id": "t1", "field": "title", "match": "contains", "pattern": "Paw Patrol", "enabled": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	s := &server{sessions: newSessionStore(time.Hour), taggingHTTP: mustURL(up.URL)}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/tagging", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleGetTagging(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Tags      []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Rules []struct {
			Pattern string `json:"pattern"`
			TagID   string `json:"tagId"`
		} `json:"rules"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Tags) != 1 || body.Tags[0].Name != "Kids" || body.Rules[0].Pattern != "Paw Patrol" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleUpsertTaggingRule(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rules" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "r1", "tag_id": "t1", "field": "title", "match": "contains", "pattern": "Paw Patrol", "enabled": true})
	}))
	t.Cleanup(up.Close)
	s := &server{sessions: newSessionStore(time.Hour), taggingHTTP: mustURL(up.URL)}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tagging/rules", strings.NewReader(
		`{"tag_id":"t1","field":"title","match":"contains","pattern":"Paw Patrol","enabled":true}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleUpsertTaggingRule(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Pattern string `json:"pattern"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Pattern != "Paw Patrol" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleClassifyTagging(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/classify" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"media_id":         "m1",
			"tags":             []map[string]any{{"id": "t1", "name": "Kids", "category": "audience"}},
			"matched_rule_ids": []string{"r1"},
		})
	}))
	t.Cleanup(up.Close)
	s := &server{sessions: newSessionStore(time.Hour), taggingHTTP: mustURL(up.URL)}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tagging/classify", strings.NewReader(
		`{"mediaId":"m1","title":"Paw Patrol","mediaType":"tv","merge":true}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleClassifyTagging(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		MediaID        string   `json:"mediaId"`
		MatchedRuleIds []string `json:"matchedRuleIds"`
		Tags           []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.MediaID != "m1" || len(body.Tags) != 1 || body.Tags[0].Name != "Kids" || body.MatchedRuleIds[0] != "r1" {
		t.Fatalf("%#v", body)
	}
}
