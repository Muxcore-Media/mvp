package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
)

func normalizeWatchMediaType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "movie", "movies":
		return "movie"
	case "tv":
		return "tv"
	case "music":
		return "music"
	default:
		return "both"
	}
}

func publicWatchDir(dir *scannerv1.WatchDir) map[string]any {
	if dir == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                dir.GetId(),
		"path":              dir.GetPath(),
		"media_type":        dir.GetMediaType(),
		"library_path":      dir.GetLibraryPath(),
		"tv_library_path":   dir.GetTvLibraryPath(),
		"music_library_path": dir.GetMusicLibraryPath(),
		"enabled":           dir.GetEnabled(),
		"created_at":        dir.GetCreatedAt(),
	}
}

func (s *server) handleListWatchDirs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watchdirs.forbidden"})
		return
	}
	if s.scanner == nil {
		writeJSON(w, map[string]any{"available": false, "dirs": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.scanner.ListWatchDirs(ctx, &scannerv1.ListWatchDirsRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "dirs": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetDirs()))
	for _, dir := range resp.GetDirs() {
		if dir == nil {
			continue
		}
		out = append(out, publicWatchDir(dir))
	}
	writeJSON(w, map[string]any{"available": true, "dirs": out})
}

func (s *server) handleCreateWatchDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watchdirs.forbidden"})
		return
	}
	if s.scanner == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-scanner is not connected", "code": "watchdirs.unavailable"})
		return
	}
	var body struct {
		Path             string `json:"path"`
		MediaType        string `json:"media_type"`
		LibraryPath      string `json:"library_path"`
		TVLibraryPath    string `json:"tv_library_path"`
		MusicLibraryPath string `json:"music_library_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "watchdirs.invalid_json"})
		return
	}
	path := strings.TrimSpace(body.Path)
	if path == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "path required", "code": "watchdirs.path_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.scanner.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{
		Path:             path,
		MediaType:        normalizeWatchMediaType(body.MediaType),
		LibraryPath:      strings.TrimSpace(body.LibraryPath),
		TvLibraryPath:    strings.TrimSpace(body.TVLibraryPath),
		MusicLibraryPath: strings.TrimSpace(body.MusicLibraryPath),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watchdirs.create_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"id":         resp.GetId(),
		"path":       path,
		"media_type": normalizeWatchMediaType(body.MediaType),
		"enabled":    true,
	})
}

func (s *server) handleDeleteWatchDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watchdirs.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "watchdirs.id_required"})
		return
	}
	if s.scanner == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-scanner is not connected", "code": "watchdirs.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.scanner.RemoveWatchDir(ctx, &scannerv1.RemoveWatchDirRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watchdirs.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}

func (s *server) handleUpdateWatchDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watchdirs.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "watchdirs.id_required"})
		return
	}
	if s.scanner == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-scanner is not connected", "code": "watchdirs.unavailable"})
		return
	}
	var body struct {
		Enabled          *bool  `json:"enabled"`
		Path             string `json:"path"`
		MediaType        string `json:"media_type"`
		LibraryPath      string `json:"library_path"`
		TVLibraryPath    string `json:"tv_library_path"`
		MusicLibraryPath string `json:"music_library_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "watchdirs.invalid_json"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if body.Enabled != nil {
		if _, err := s.scanner.SetWatchDirEnabled(ctx, &scannerv1.SetWatchDirEnabledRequest{
			Id: id, Enabled: *body.Enabled,
		}); err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watchdirs.update_failed"})
			return
		}
	}
	path := strings.TrimSpace(body.Path)
	mediaType := strings.TrimSpace(body.MediaType)
	libPath := strings.TrimSpace(body.LibraryPath)
	tvPath := strings.TrimSpace(body.TVLibraryPath)
	musicPath := strings.TrimSpace(body.MusicLibraryPath)
	if path != "" || mediaType != "" || libPath != "" || tvPath != "" || musicPath != "" {
		upd := &scannerv1.UpdateWatchDirRequest{Id: id}
		if path != "" {
			upd.Path = path
		}
		if mediaType != "" {
			upd.MediaType = normalizeWatchMediaType(mediaType)
		}
		if libPath != "" {
			upd.LibraryPath = libPath
		}
		if tvPath != "" {
			upd.TvLibraryPath = tvPath
		}
		if musicPath != "" {
			upd.MusicLibraryPath = musicPath
		}
		if _, err := s.scanner.UpdateWatchDir(ctx, upd); err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watchdirs.update_failed"})
			return
		}
	}
	if body.Enabled == nil && path == "" && mediaType == "" && libPath == "" && tvPath == "" && musicPath == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "no fields to update", "code": "watchdirs.empty_update"})
		return
	}
	resp, err := s.scanner.ListWatchDirs(ctx, &scannerv1.ListWatchDirsRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"id": id, "updated": true})
		return
	}
	for _, dir := range resp.GetDirs() {
		if dir != nil && dir.GetId() == id {
			out := publicWatchDir(dir)
			out["updated"] = true
			writeJSON(w, out)
			return
		}
	}
	writeJSON(w, map[string]any{"id": id, "updated": true})
}
