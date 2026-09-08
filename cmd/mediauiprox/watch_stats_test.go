package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleWatchStatsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleWatchStats(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Stats     []any `json:"stats"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Stats == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleWatchStatsLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/stats/home":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stats": []map[string]any{{"Key": "plays", "Label": "Plays", "Value": 12}},
			})
		case "/stats/top-content":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"movies": []map[string]any{{"Title": "Dune", "MediaType": "movie", "PlayCount": 4, "WatchMinutes": 90}},
				"shows":  []map[string]any{{"Title": "Severance", "MediaType": "episode", "PlayCount": 6}},
			})
		case "/stats/plays-by-date":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rows": []map[string]any{{"Date": "2026-09-01", "Count": 3}},
			})
		case "/stats/libraries":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"libraries": []map[string]any{{"LibraryName": "movies", "PlayCount": 8, "WatchMinutes": 200}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	u := mustURL(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handleWatchStats(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats?days=14", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Days      int  `json:"days"`
		Stats     []struct {
			Key   string  `json:"key"`
			Value float64 `json:"value"`
		} `json:"stats"`
		TopMovies []struct {
			Title     string  `json:"title"`
			PlayCount float64 `json:"playCount"`
		} `json:"topMovies"`
		Plays []struct {
			Date  string  `json:"date"`
			Count float64 `json:"count"`
		} `json:"plays"`
		Libraries []struct {
			Name string `json:"name"`
		} `json:"libraries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Days != 14 || len(body.Stats) != 1 || body.Stats[0].Key != "plays" {
		t.Fatalf("%#v", body)
	}
	if len(body.TopMovies) != 1 || body.TopMovies[0].Title != "Dune" || body.TopMovies[0].PlayCount != 4 {
		t.Fatalf("movies %#v", body.TopMovies)
	}
	if len(body.Plays) != 1 || body.Plays[0].Date != "2026-09-01" {
		t.Fatalf("plays %#v", body.Plays)
	}
	if len(body.Libraries) != 1 || body.Libraries[0].Name != "movies" {
		t.Fatalf("libraries %#v", body.Libraries)
	}
}

func TestHandleItemWatchStatsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleItemWatchStats(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/item?id=m1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool   `json:"available"`
		ItemID    string `json:"itemId"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.ItemID != "m1" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleItemWatchStatsRequiresID(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleItemWatchStats(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/item", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleItemWatchStatsLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stats/item" || r.URL.Query().Get("item_id") != "m1" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"item_id": "m1", "play_count": 4, "unique_users": 2, "watch_minutes": 90,
			"has_activity": true, "never_watched": false, "last_watched_at": "2026-09-01T12:00:00Z",
		})
	}))
	t.Cleanup(up.Close)
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handleItemWatchStats(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/item?id=m1&runtime=139", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Available   bool    `json:"available"`
		PlayCount   float64 `json:"playCount"`
		UniqueUsers float64 `json:"uniqueUsers"`
		LastWatched string  `json:"lastWatchedAt"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.PlayCount != 4 || body.UniqueUsers != 2 || body.LastWatched == "" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleWatchStatsStaleUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleWatchStatsStale(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/stale", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Items     []any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Items == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleWatchStatsStaleLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/stale" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"never_watched_count": 1,
			"stale_count":         1,
			"items": []map[string]any{
				{"Title": "Old Movie", "Category": "never_watched", "LibraryName": "movies", "DaysStale": 400},
			},
		})
	}))
	t.Cleanup(up.Close)
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handleWatchStatsStale(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/stale", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available    bool `json:"available"`
		NeverWatched int  `json:"neverWatched"`
		Items        []struct {
			Title    string `json:"title"`
			Category string `json:"category"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.NeverWatched != 1 || len(body.Items) != 1 || body.Items[0].Title != "Old Movie" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleImportTautulliForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleImportTautulli(w, httptest.NewRequest(http.MethodPost, "/api/watch-stats/import-tautulli", strings.NewReader(
		`{"tautulli_url":"http://tautulli:8181","api_key":"k"}`,
	)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleImportTautulliDryRun(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/import/tautulli" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"imported": 0, "skipped": 3, "failed": 0, "total_fetched": 3})
	}))
	t.Cleanup(up.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token", sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/watch-stats/import-tautulli", strings.NewReader(
		`{"tautulli_url":"http://tautulli:8181","api_key":"k","dry_run":true}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleImportTautulli(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Skipped float64 `json:"skipped"`
		DryRun  bool    `json:"dryRun"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.DryRun || body.Skipped != 3 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleImportJellystatForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleImportJellystat(w, httptest.NewRequest(http.MethodPost, "/api/watch-stats/import-jellystat", strings.NewReader(
		`{"backup_json":"[]"}`,
	)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleImportJellystatDryRun(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/import/jellystat" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"imported": 1, "skipped": 0, "failed": 0, "total_fetched": 1})
	}))
	t.Cleanup(up.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token", sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/watch-stats/import-jellystat", strings.NewReader(
		`{"backup_json":"[{\"id\":1}]","dry_run":true}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleImportJellystat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Imported float64 `json:"imported"`
		DryRun   bool    `json:"dryRun"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.DryRun || body.Imported != 1 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleWatchStatsDuplicatesLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/duplicates" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"groups": []map[string]any{
				{"Title": "Dune", "CopyCount": 2, "Copies": []map[string]any{
					{"Title": "Dune", "LibraryName": "movies", "FileSizeBytes": 1000},
				}},
			},
		})
	}))
	t.Cleanup(up.Close)
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handleWatchStatsDuplicates(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/duplicates", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Groups    []struct {
			Title     string `json:"title"`
			CopyCount int    `json:"copyCount"`
		} `json:"groups"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Groups) != 1 || body.Groups[0].Title != "Dune" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleWatchStatsStorageLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/storage" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_items":           12,
			"total_bytes":           1_000_000_000,
			"duplicate_waste_bytes": 500_000_000,
			"total_human":           "931.3 MB",
			"libraries": []map[string]any{
				{"LibraryName": "movies", "ItemCount": 8, "TotalBytes": 800_000_000},
			},
		})
	}))
	t.Cleanup(up.Close)
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handleWatchStatsStorage(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/storage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available  bool    `json:"available"`
		TotalItems float64 `json:"totalItems"`
		Libraries  []struct {
			Name string `json:"name"`
		} `json:"libraries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.TotalItems != 12 || len(body.Libraries) != 1 || body.Libraries[0].Name != "movies" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleWatchStatsStorageHistoryLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/storage/history" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"history": []map[string]any{
				{"Day": "2026-08-01", "TotalBytes": 800_000_000, "ItemCount": 10},
				{"Day": "2026-09-01", "TotalBytes": 1_000_000_000, "ItemCount": 12},
			},
			"prediction": map[string]any{
				"growth_bytes_per_day": 6_451_612,
				"projected_bytes":      1_580_645_000,
				"horizon_days":         90,
			},
		})
	}))
	t.Cleanup(up.Close)
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handleWatchStatsStorageHistory(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/storage-history?days=90", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		History   []struct {
			Day   string  `json:"day"`
			Bytes float64 `json:"bytes"`
		} `json:"history"`
		Prediction struct {
			ProjectedBytes float64 `json:"projectedBytes"`
			HorizonDays    float64 `json:"horizonDays"`
		} `json:"prediction"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.History) != 2 || body.History[1].Day != "2026-09-01" ||
		body.History[1].Bytes != 1_000_000_000 || body.Prediction.HorizonDays != 90 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleWatchStatsChartsLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stats/plays-by-hour":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "20", "Label": "20:00", "Count": 4}}})
		case "/stats/plays-by-top-users":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "pat", "Label": "pat", "Count": 6}}})
		case "/stats/plays-by-top-platforms":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "web", "Label": "Web", "Count": 5}}})
		case "/stats/plays-by-dow":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "sat", "Label": "Saturday", "Count": 7}}})
		case "/stats/plays-by-month":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "2026-09", "Label": "2026-09", "Count": 12}}})
		case "/stats/plays-by-stream-type":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "direct", "Label": "Direct play", "Count": 9}}})
		case "/stats/plays-by-stream-resolution":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "1080p", "Label": "1080p", "Count": 8}}})
		case "/stats/plays-by-source-resolution":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "2160p", "Label": "2160p", "Count": 3}}})
		case "/stats/plays-by-platform-resolution":
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"Key": "web|1080p", "Label": "Web · 1080p", "Count": 4}}})
		case "/stats/concurrent-streams":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"categories": []string{"2026-09-01"},
				"series":     []map[string]any{{"name": "direct", "data": []int{3}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	s := &server{playbackMonitorHTTP: mustURL(up.URL), playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handleWatchStatsCharts(w, httptest.NewRequest(http.MethodGet, "/api/watch-stats/charts", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Hours     []struct {
			Label string `json:"label"`
		} `json:"hours"`
		Users []struct {
			Label string `json:"label"`
		} `json:"users"`
		DaysOfWeek []struct {
			Label string `json:"label"`
		} `json:"daysOfWeek"`
		StreamTypes []struct {
			Label string `json:"label"`
		} `json:"streamTypes"`
		Concurrent struct {
			Peak float64 `json:"peak"`
		} `json:"concurrent"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Hours) != 1 || body.Hours[0].Label != "20:00" || body.Users[0].Label != "pat" ||
		body.DaysOfWeek[0].Label != "Saturday" || body.StreamTypes[0].Label != "Direct play" || body.Concurrent.Peak != 3 {
		t.Fatalf("%#v", body)
	}
}
