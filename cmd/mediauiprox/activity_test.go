package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActivityStuckStatuses(t *testing.T) {
	if !activityStuck("failed") || !activityStuck("stalled") || !activityStuck("import_failed") {
		t.Fatal("failed/stalled/import_failed are stuck")
	}
	if activityStuck("completed") || activityStuck("downloading") {
		t.Fatal("completed/downloading are not stuck")
	}
}

func TestActivityUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleActivity(w, httptest.NewRequest(http.MethodGet, "/api/activity", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available {
		t.Fatal("expected unavailable")
	}
}

func TestActivityMarksStuckImports(t *testing.T) {
	s := &server{automation: dialAutomationFixture(t, &fixtureAutomation{})}
	w := httptest.NewRecorder()
	s.handleActivity(w, httptest.NewRequest(http.MethodGet, "/api/activity", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool
		Items     []activityRecordJSON
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 2 {
		t.Fatalf("%#v", body)
	}
	if !body.Items[0].Stuck || !body.Items[0].Warning || body.Items[0].Status != "import_failed" {
		t.Fatalf("want stuck import first, got %#v", body.Items[0])
	}
	if body.Items[1].Stuck || body.Items[1].Warning {
		t.Fatalf("completed should not be stuck, got %#v", body.Items[1])
	}
}

func TestActivityRetryImport(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	w := httptest.NewRecorder()
	s.handleActivityRetry(w, httptest.NewRequest(http.MethodPost, "/api/activity/retry", bytes.NewBufferString(`{"history_id":"h-fail"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fix.retriedHistory != "h-fail" {
		t.Fatalf("history_id=%q", fix.retriedHistory)
	}
}

func TestWantedMissingOnly(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	w := httptest.NewRecorder()
	s.handleWanted(w, httptest.NewRequest(http.MethodGet, "/api/wanted?missing=1&monitored=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !fix.wantedMissing {
		t.Fatal("expected missing filter")
	}
	var body struct {
		Available bool
		Items     []wantedItemJSON
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 || body.Items[0].ItemID != "m-miss" {
		t.Fatalf("%#v", body)
	}
}

func TestWantedRemove(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	w := httptest.NewRecorder()
	s.handleWantedRemove(w, httptest.NewRequest(http.MethodPost, "/api/wanted/remove", bytes.NewBufferString(`{"queue_id":"q-miss"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fix.removedQueue != "q-miss" {
		t.Fatalf("queue_id=%q", fix.removedQueue)
	}
}

func TestWantedAdd(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	w := httptest.NewRecorder()
	s.handleWantedAdd(w, httptest.NewRequest(http.MethodPost, "/api/wanted", bytes.NewBufferString(
		`{"item_type":"movie","item_id":"m1","title":"Dune","year":2021,"tmdb_id":438631}`,
	)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fix.addedWanted == nil || fix.addedWanted.GetItemId() != "m1" || fix.addedWanted.GetTitle() != "Dune" || fix.addedWanted.GetTmdbId() != 438631 {
		t.Fatalf("added %#v", fix.addedWanted)
	}
	var body struct {
		Added   bool   `json:"added"`
		QueueID string `json:"queue_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Added || body.QueueID != "w_movie_m1" {
		t.Fatalf("%#v", body)
	}
}

func TestWantedAddRequiresIDs(t *testing.T) {
	s := &server{automation: dialAutomationFixture(t, &fixtureAutomation{})}
	w := httptest.NewRecorder()
	s.handleWantedAdd(w, httptest.NewRequest(http.MethodPost, "/api/wanted", bytes.NewBufferString(`{"title":"Dune"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestWantedAddUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleWantedAdd(w, httptest.NewRequest(http.MethodPost, "/api/wanted", bytes.NewBufferString(
		`{"item_type":"movie","item_id":"m1","title":"Dune"}`,
	)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}
