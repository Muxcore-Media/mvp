package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
)

func publicSubtitleCandidate(c *subtv1.SubtitleCandidate) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	title := strings.TrimSpace(c.GetReleaseName())
	if title == "" {
		title = strings.TrimSpace(c.GetFileId())
	}
	return map[string]any{
		"id":        c.GetFileId(),
		"provider":  c.GetSource(),
		"title":     title,
		"language":  c.GetLanguage(),
		"format":    c.GetFormat(),
		"release":   c.GetReleaseName(),
		"downloads": c.GetDownloads(),
	}
}

func (s *server) handleSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "results": []any{}})
		return
	}
	q := r.URL.Query()
	title := strings.TrimSpace(q.Get("title"))
	query := title
	if year := strings.TrimSpace(q.Get("year")); year != "" && title != "" {
		query = title + " " + year
	}
	if query == "" && strings.TrimSpace(q.Get("imdb_id")) == "" && strings.TrimSpace(q.Get("tmdb_id")) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "title or imdb_id required", "code": "subtitles.query_required"})
		return
	}
	tmdbID, _ := strconv.Atoi(strings.TrimSpace(q.Get("tmdb_id")))
	season, _ := strconv.Atoi(strings.TrimSpace(q.Get("season")))
	episode, _ := strconv.Atoi(strings.TrimSpace(q.Get("episode")))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resp, err := s.subtitles.Search(ctx, &subtv1.SearchRequest{
		Query:    query,
		ImdbId:   strings.TrimSpace(q.Get("imdb_id")),
		TmdbId:   int32(tmdbID),
		Language: strings.TrimSpace(q.Get("language")),
		Season:   int32(season),
		Episode:  int32(episode),
	})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "results": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetResults()))
	for _, c := range resp.GetResults() {
		if c == nil {
			continue
		}
		out = append(out, publicSubtitleCandidate(c))
	}
	writeJSON(w, map[string]any{"available": true, "results": out})
}

func (s *server) handleSubtitleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Subtitle search is unavailable — start media-subtitles.",
			"code":  "subtitles.unavailable",
		})
		return
	}
	var body struct {
		ID          string `json:"id"`
		FileID      string `json:"file_id"`
		Provider    string `json:"provider"`
		Language    string `json:"language"`
		MediaFileID string `json:"media_file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "subtitles.invalid_json"})
		return
	}
	fileID := strings.TrimSpace(body.FileID)
	if fileID == "" {
		fileID = strings.TrimSpace(body.ID)
	}
	if fileID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.subtitles.DownloadSubtitle(ctx, &subtv1.DownloadSubtitleRequest{
		FileId:      fileID,
		MediaFileId: strings.TrimSpace(body.MediaFileID),
		Language:    strings.TrimSpace(body.Language),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.download_failed"})
		return
	}
	sub := resp.GetSubtitle()
	if sub == nil || strings.TrimSpace(sub.GetId()) == "" {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "download returned no subtitle", "code": "subtitles.download_empty"})
		return
	}
	lang := strings.TrimSpace(sub.GetLanguage())
	if lang == "" {
		lang = strings.TrimSpace(body.Language)
	}
	label := lang
	if label == "" {
		label = "Subtitle"
	}
	if p := strings.TrimSpace(body.Provider); p != "" && p != sub.GetSource() {
		label = label + " · " + p
	}
	writeJSON(w, map[string]any{
		"track_url": "/api/playback/subtitles/" + sub.GetId(),
		"language":  lang,
		"label":     label,
		"id":        sub.GetId(),
	})
}
