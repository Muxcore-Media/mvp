package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
)

func TestMapTVSeasonsSkipsSpecials(t *testing.T) {
	got := mapTVSeasons([]*metadatav1.Season{
		{SeasonNumber: 0, Name: "Specials", EpisodeCount: 2},
		{SeasonNumber: 1, Name: "Season 1", EpisodeCount: 13, AirDate: "2008-01-20"},
		{SeasonNumber: 2, EpisodeCount: 13},
		nil,
	})
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].SeasonNumber != 1 || got[0].EpisodeCount != 13 || got[0].AirDate != "2008-01-20" {
		t.Fatalf("%+v", got[0])
	}
	if got[1].Name != "Season 2" {
		t.Fatalf("unnamed season: %+v", got[1])
	}
}

func TestHandleMediaIssuesCreateAndList(t *testing.T) {
	s := &server{issues: newMediaIssueStore(t.TempDir())}
	req := httptest.NewRequest(http.MethodPost, "/api/media-issues", strings.NewReader(
		`{"kind":"subtitles","mediaType":"movie","tmdbId":550,"title":"Fight Club","message":"no English subs"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleMediaIssues(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var created mediaIssue
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Kind != "subtitles" || created.Title != "Fight Club" || created.ID == "" {
		t.Fatalf("%+v", created)
	}

	listW := httptest.NewRecorder()
	s.handleMediaIssues(listW, httptest.NewRequest(http.MethodGet, "/api/media-issues", nil))
	if listW.Code != http.StatusOK {
		t.Fatalf("list %d", listW.Code)
	}
	var body struct {
		Items []mediaIssue `json:"items"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Message != "no English subs" {
		t.Fatalf("%+v", body.Items)
	}

	dir := t.TempDir()
	persist := &server{issues: newMediaIssueStore(dir)}
	persist.handleMediaIssues(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/media-issues", strings.NewReader(
		`{"kind":"video","title":"Dune","message":"stutter"}`,
	)))
	reopen := newMediaIssueStore(dir)
	if n := len(reopen.list()); n != 1 {
		t.Fatalf("reopen list %d", n)
	}
}
