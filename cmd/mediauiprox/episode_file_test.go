package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func episodeFileUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/episodes/e1/file" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"file_id": "ef1", "file_path": "/data/tv/Severance.S01E01.mkv", "quality": "WEBDL-1080p",
		})
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func TestHandleGetEpisodeFile(t *testing.T) {
	upstream := episodeFileUpstream(t)
	s := &server{tvHTTP: mustURL(upstream.URL)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/episodes/e1/file", nil)
	req.SetPathValue("id", "e1")
	s.handleGetEpisodeFile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool   `json:"available"`
		FileID    string `json:"file_id"`
		Filename  string `json:"filename"`
		Quality   string `json:"quality"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.FileID != "ef1" || body.Filename != "Severance.S01E01.mkv" || body.Quality != "WEBDL-1080p" {
		t.Fatalf("file %#v", body)
	}
}

func TestHandleGetEpisodeFileUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/episodes/e1/file", nil)
	req.SetPathValue("id", "e1")
	s.handleGetEpisodeFile(w, req)
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
		t.Fatal("expected available=false")
	}
}

func TestHandleTVByIDEnrichesEpisodeFile(t *testing.T) {
	upstream := episodeFileUpstream(t)
	s := &server{tv: dialTVAPIFixture(t), tvHTTP: mustURL(upstream.URL)}
	w := httptest.NewRecorder()
	s.handleTVByID(w, httptest.NewRequest(http.MethodGet, "/api/tv/s1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Show struct {
			Seasons []struct {
				Episodes []struct {
					Title    string `json:"title"`
					Quality  string `json:"quality"`
					Filename string `json:"filename"`
					FileID   string `json:"file_id"`
				} `json:"episodes"`
			} `json:"seasons"`
		} `json:"show"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Show.Seasons) == 0 || len(body.Show.Seasons[0].Episodes) == 0 {
		t.Fatalf("show %#v", body.Show)
	}
	ep := body.Show.Seasons[0].Episodes[0]
	if ep.Quality != "WEBDL-1080p" || ep.Filename != "Severance.S01E01.mkv" || ep.FileID != "ef1" {
		t.Fatalf("episode %#v", ep)
	}
}
