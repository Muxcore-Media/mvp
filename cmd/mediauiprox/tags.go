package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

func publicMovieTag(t *mgmntv1.Tag) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         t.GetId(),
		"label":      t.GetLabel(),
		"created_at": t.GetCreatedAt(),
		"media":      "movie",
	}
}

func publicTVTag(t *tvmgmtv1.Tag) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         t.GetId(),
		"label":      t.GetLabel(),
		"created_at": t.GetCreatedAt(),
		"media":      "tv",
	}
}

func publicMusicTag(t *musicv1.Tag) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         t.GetId(),
		"label":      t.GetLabel(),
		"created_at": t.GetCreatedAt(),
		"media":      "music",
	}
}

func normalizeTagMedia(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "tv", "show", "series":
		return "tv"
	case "movie", "movies":
		return "movie"
	case "music", "artist", "artists":
		return "music"
	default:
		return "all"
	}
}

func (s *server) handleListTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	media := normalizeTagMedia(r.URL.Query().Get("media"))
	out := make([]map[string]any, 0)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	movieOK, tvOK, musicOK := false, false, false
	if media != "tv" && media != "music" && s.movies != nil {
		resp, err := s.movies.ListTags(ctx, &mgmntv1.ListTagsRequest{})
		if err == nil {
			movieOK = true
			for _, t := range resp.GetTags() {
				if t == nil {
					continue
				}
				out = append(out, publicMovieTag(t))
			}
		}
	}
	if media != "movie" && media != "music" && s.tv != nil {
		resp, err := s.tv.ListTags(ctx, &tvmgmtv1.ListTagsRequest{})
		if err == nil {
			tvOK = true
			for _, t := range resp.GetTags() {
				if t == nil {
					continue
				}
				out = append(out, publicTVTag(t))
			}
		}
	}
	if media != "movie" && media != "tv" && s.music != nil {
		resp, err := s.music.ListTags(ctx, &musicv1.ListTagsRequest{})
		if err == nil {
			musicOK = true
			for _, t := range resp.GetTags() {
				if t == nil {
					continue
				}
				out = append(out, publicMusicTag(t))
			}
		}
	}
	available := (media == "movie" && movieOK) || (media == "tv" && tvOK) || (media == "music" && musicOK) || (media == "all" && (movieOK || tvOK || musicOK))
	if media == "movie" && s.movies == nil {
		available = false
	}
	if media == "tv" && s.tv == nil {
		available = false
	}
	if media == "music" && s.music == nil {
		available = false
	}
	writeJSON(w, map[string]any{"available": available, "tags": out})
}

func (s *server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tags.forbidden"})
		return
	}
	var body struct {
		Label string `json:"label"`
		Media string `json:"media"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "tags.invalid_json"})
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "label required", "code": "tags.label_required"})
		return
	}
	media := normalizeTagMedia(body.Media)
	if media == "all" {
		media = "movie"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if media == "tv" {
		if s.tv == nil {
			writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "TV module unavailable", "code": "tags.unavailable"})
			return
		}
		resp, err := s.tv.CreateTag(ctx, &tvmgmtv1.CreateTagRequest{Label: label})
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tags.create_failed"})
			return
		}
		writeJSON(w, map[string]any{"tag": map[string]any{"id": resp.GetTagId(), "label": label, "media": "tv"}})
		return
	}
	if media == "music" {
		if s.music == nil {
			writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "Music module unavailable", "code": "tags.unavailable"})
			return
		}
		resp, err := s.music.CreateTag(ctx, &musicv1.CreateTagRequest{Label: label})
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tags.create_failed"})
			return
		}
		writeJSON(w, map[string]any{"tag": map[string]any{"id": resp.GetTagId(), "label": label, "media": "music"}})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "Movies module unavailable", "code": "tags.unavailable"})
		return
	}
	resp, err := s.movies.CreateTag(ctx, &mgmntv1.CreateTagRequest{Label: label})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tags.create_failed"})
		return
	}
	writeJSON(w, map[string]any{"tag": map[string]any{"id": resp.GetTagId(), "label": label, "media": "movie"}})
}

func (s *server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tags.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tags.id_required"})
		return
	}
	media := normalizeTagMedia(r.URL.Query().Get("media"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var err error
	if media != "tv" && media != "music" && s.movies != nil {
		_, err = s.movies.DeleteTag(ctx, &mgmntv1.DeleteTagRequest{TagId: id})
		if err == nil {
			writeJSON(w, map[string]any{"removed": true, "id": id, "media": "movie"})
			return
		}
	}
	if media != "movie" && media != "music" && s.tv != nil {
		_, err = s.tv.DeleteTag(ctx, &tvmgmtv1.DeleteTagRequest{TagId: id})
		if err == nil {
			writeJSON(w, map[string]any{"removed": true, "id": id, "media": "tv"})
			return
		}
	}
	if media != "movie" && media != "tv" && s.music != nil {
		_, err = s.music.DeleteTag(ctx, &musicv1.DeleteTagRequest{TagId: id})
		if err == nil {
			writeJSON(w, map[string]any{"removed": true, "id": id, "media": "music"})
			return
		}
	}
	if s.movies == nil && s.tv == nil && s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "Library modules unavailable", "code": "tags.unavailable"})
		return
	}
	msg := "tag not found"
	if err != nil {
		msg = err.Error()
	}
	writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "tags.delete_failed"})
}

func (s *server) handleGetMovieTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tags.id_required"})
		return
	}
	if s.movies == nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.movies.GetItemTags(ctx, &mgmntv1.GetItemTagsRequest{ItemId: id})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetTags()))
	for _, t := range resp.GetTags() {
		if t == nil {
			continue
		}
		out = append(out, publicMovieTag(t))
	}
	writeJSON(w, map[string]any{"available": true, "tags": out})
}

func (s *server) handleSetMovieTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tags.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tags.id_required"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "Movies module unavailable", "code": "tags.unavailable"})
		return
	}
	var body struct {
		TagIDs []string `json:"tag_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "tags.invalid_json"})
		return
	}
	ids := make([]string, 0, len(body.TagIDs))
	for _, raw := range body.TagIDs {
		if id := strings.TrimSpace(raw); id != "" {
			ids = append(ids, id)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.movies.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: id, TagIds: ids}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tags.set_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id, "tag_ids": ids})
}

func (s *server) handleGetTVTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tags.id_required"})
		return
	}
	if s.tv == nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.tv.GetItemTags(ctx, &tvmgmtv1.GetItemTagsRequest{ItemId: id})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetTags()))
	for _, t := range resp.GetTags() {
		if t == nil {
			continue
		}
		out = append(out, publicTVTag(t))
	}
	writeJSON(w, map[string]any{"available": true, "tags": out})
}

func (s *server) handleSetTVTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tags.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tags.id_required"})
		return
	}
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "TV module unavailable", "code": "tags.unavailable"})
		return
	}
	var body struct {
		TagIDs []string `json:"tag_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "tags.invalid_json"})
		return
	}
	ids := make([]string, 0, len(body.TagIDs))
	for _, raw := range body.TagIDs {
		if id := strings.TrimSpace(raw); id != "" {
			ids = append(ids, id)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.tv.SetItemTags(ctx, &tvmgmtv1.SetItemTagsRequest{ItemId: id, TagIds: ids}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tags.set_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id, "tag_ids": ids})
}

func (s *server) handleGetMusicTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tags.id_required"})
		return
	}
	if s.music == nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.music.GetItemTags(ctx, &musicv1.GetItemTagsRequest{ItemId: id})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetTags()))
	for _, t := range resp.GetTags() {
		if t == nil {
			continue
		}
		out = append(out, publicMusicTag(t))
	}
	writeJSON(w, map[string]any{"available": true, "tags": out})
}

func (s *server) handleSetMusicTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tags.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tags.id_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "Music module unavailable", "code": "tags.unavailable"})
		return
	}
	var body struct {
		TagIDs []string `json:"tag_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "tags.invalid_json"})
		return
	}
	ids := make([]string, 0, len(body.TagIDs))
	for _, raw := range body.TagIDs {
		if id := strings.TrimSpace(raw); id != "" {
			ids = append(ids, id)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.music.SetItemTags(ctx, &musicv1.SetItemTagsRequest{ItemId: id, TagIds: ids}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tags.set_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id, "tag_ids": ids})
}
