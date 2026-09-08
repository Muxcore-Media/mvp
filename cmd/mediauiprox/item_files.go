package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func publicMovieFile(f *mgmntv1.MovieFile) map[string]any {
	if f == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         f.GetId(),
		"quality":    f.GetQuality(),
		"size_bytes": f.GetSizeBytes(),
		"container":  f.GetContainer(),
		"created_at": f.GetCreatedAt(),
		"filename":   filepath.Base(f.GetFilePath()),
	}
}

func (s *server) handleListMovieFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "files.id_required"})
		return
	}
	if s.movies == nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	listed, err := s.movies.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: id})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "error": err.Error()})
		return
	}
	items := make([]map[string]any, 0, len(listed.GetFiles()))
	for _, f := range listed.GetFiles() {
		if f == nil || strings.TrimSpace(f.GetId()) == "" {
			continue
		}
		items = append(items, publicMovieFile(f))
	}
	writeJSON(w, map[string]any{"available": true, "items": items})
}

func (s *server) handleDeleteMovieFileByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "files.forbidden"})
		return
	}
	movieID := strings.TrimSpace(r.PathValue("id"))
	fileID := strings.TrimSpace(r.PathValue("fileId"))
	if movieID == "" || fileID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "files.id_required"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies module is not connected", "code": "files.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	listed, err := s.movies.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: movieID})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "files.list_failed"})
		return
	}
	found := false
	for _, f := range listed.GetFiles() {
		if f != nil && strings.TrimSpace(f.GetId()) == fileID {
			found = true
			break
		}
	}
	if !found {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "file not found", "code": "files.not_found"})
		return
	}
	if _, err := s.movies.RemoveFile(ctx, &mgmntv1.RemoveFileRequest{FileId: fileID, DeleteFiles: deleteFiles}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "files.remove_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": fileID, "delete_files": deleteFiles})
}
