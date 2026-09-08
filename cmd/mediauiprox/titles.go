package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

func publicAlternateTitle(id, title, clean, source string) map[string]any {
	return map[string]any{
		"id":          id,
		"title":       title,
		"clean_title": clean,
		"source":      source,
		"user":        source == "user",
	}
}

func (s *server) handleListMovieTitles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "titles.id_required"})
		return
	}
	if s.movies == nil {
		writeJSON(w, map[string]any{"available": false, "titles": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.movies.ListAlternateTitles(ctx, &mgmntv1.ListAlternateTitlesRequest{MovieId: id})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "titles": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetTitles()))
	for _, t := range resp.GetTitles() {
		if t == nil {
			continue
		}
		out = append(out, publicAlternateTitle(t.GetId(), t.GetTitle(), t.GetCleanTitle(), t.GetSource()))
	}
	writeJSON(w, map[string]any{"available": true, "titles": out})
}

func (s *server) handleAddMovieTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "titles.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "titles.id_required"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies unavailable", "code": "titles.unavailable"})
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "titles.invalid_json"})
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "title required", "code": "titles.title_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.movies.AddAlternateTitle(ctx, &mgmntv1.AddAlternateTitleRequest{MovieId: id, Title: title})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "titles.add_failed"})
		return
	}
	t := resp.GetTitle()
	writeJSON(w, map[string]any{"available": true, "title": publicAlternateTitle(t.GetId(), t.GetTitle(), t.GetCleanTitle(), t.GetSource())})
}

func (s *server) handleDeleteMovieTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "titles.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	titleID := strings.TrimSpace(r.PathValue("titleId"))
	if id == "" || titleID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "titles.id_required"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies unavailable", "code": "titles.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.movies.RemoveAlternateTitle(ctx, &mgmntv1.RemoveAlternateTitleRequest{MovieId: id, TitleId: titleID}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "titles.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": titleID})
}

func (s *server) handleListTVTitles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "titles.id_required"})
		return
	}
	if s.tv == nil {
		writeJSON(w, map[string]any{"available": false, "titles": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.tv.ListAlternateTitles(ctx, &tvmgmtv1.ListAlternateTitlesRequest{SeriesId: id})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "titles": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetTitles()))
	for _, t := range resp.GetTitles() {
		if t == nil {
			continue
		}
		out = append(out, publicAlternateTitle(t.GetId(), t.GetTitle(), t.GetCleanTitle(), t.GetSource()))
	}
	writeJSON(w, map[string]any{"available": true, "titles": out})
}

func (s *server) handleAddTVTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "titles.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "titles.id_required"})
		return
	}
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv unavailable", "code": "titles.unavailable"})
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "titles.invalid_json"})
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "title required", "code": "titles.title_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.tv.AddAlternateTitle(ctx, &tvmgmtv1.AddAlternateTitleRequest{SeriesId: id, Title: title})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "titles.add_failed"})
		return
	}
	t := resp.GetTitle()
	writeJSON(w, map[string]any{"available": true, "title": publicAlternateTitle(t.GetId(), t.GetTitle(), t.GetCleanTitle(), t.GetSource())})
}

func (s *server) handleDeleteTVTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "titles.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	titleID := strings.TrimSpace(r.PathValue("titleId"))
	if id == "" || titleID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "titles.id_required"})
		return
	}
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv unavailable", "code": "titles.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.tv.RemoveAlternateTitle(ctx, &tvmgmtv1.RemoveAlternateTitleRequest{SeriesId: id, TitleId: titleID}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "titles.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": titleID})
}
