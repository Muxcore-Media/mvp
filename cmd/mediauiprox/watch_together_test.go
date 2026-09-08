package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleWatchTogetherCreateSyncAndGuestForbidden(t *testing.T) {
	s := &server{together: newWatchTogetherStore(t.TempDir())}
	w := httptest.NewRecorder()
	s.handleWatchTogether(w, httptest.NewRequest(http.MethodPost, "/api/watch-together", strings.NewReader(
		`{"mediaId":"m1","src":"/stream/movies/m1","title":"Dune","positionSeconds":12.5,"playing":true}`,
	)))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var created watchTogetherRoom
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.HostToken == "" || created.Src != "/stream/movies/m1" || !created.YouAreHost {
		t.Fatalf("%+v", created)
	}

	guest := httptest.NewRecorder()
	s.handleWatchTogether(guest, httptest.NewRequest(http.MethodGet, "/api/watch-together/"+created.ID, nil))
	if guest.Code != http.StatusOK {
		t.Fatalf("guest get %d", guest.Code)
	}
	var public watchTogetherRoom
	if err := json.Unmarshal(guest.Body.Bytes(), &public); err != nil {
		t.Fatal(err)
	}
	if public.HostToken != "" || public.YouAreHost || public.Title != "Dune" {
		t.Fatalf("guest view %+v", public)
	}

	denied := httptest.NewRecorder()
	s.handleWatchTogether(denied, httptest.NewRequest(http.MethodPost, "/api/watch-together/"+created.ID, strings.NewReader(
		`{"positionSeconds":40,"playing":false}`,
	)))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("guest sync %d", denied.Code)
	}

	hostReq := httptest.NewRequest(http.MethodPost, "/api/watch-together/"+created.ID, strings.NewReader(
		`{"positionSeconds":40,"playing":false}`,
	))
	hostReq.Header.Set("X-Watch-Together-Host", created.HostToken)
	hostW := httptest.NewRecorder()
	s.handleWatchTogether(hostW, hostReq)
	if hostW.Code != http.StatusOK {
		t.Fatalf("host sync %d body %s", hostW.Code, hostW.Body.String())
	}
	var synced watchTogetherRoom
	if err := json.Unmarshal(hostW.Body.Bytes(), &synced); err != nil {
		t.Fatal(err)
	}
	if synced.PositionSeconds != 40 || synced.Playing || synced.HostToken != "" {
		t.Fatalf("synced %+v", synced)
	}
}

func TestHandleWatchTogetherRequiresSrc(t *testing.T) {
	s := &server{together: newWatchTogetherStore("")}
	w := httptest.NewRecorder()
	s.handleWatchTogether(w, httptest.NewRequest(http.MethodPost, "/api/watch-together", strings.NewReader(`{"title":"x"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestWatchTogetherPersists(t *testing.T) {
	dir := t.TempDir()
	s := &server{together: newWatchTogetherStore(dir)}
	w := httptest.NewRecorder()
	s.handleWatchTogether(w, httptest.NewRequest(http.MethodPost, "/api/watch-together", strings.NewReader(
		`{"src":"/stream/tv/e1","title":"S01E01"}`,
	)))
	var created watchTogetherRoom
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	reopen := newWatchTogetherStore(dir)
	got, ok := reopen.get(created.ID)
	if !ok || got.Title != "S01E01" || got.HostToken == "" {
		t.Fatalf("reopen %+v ok=%v", got, ok)
	}
}
