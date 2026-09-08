package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
)

func publicTrackFile(f *musicv1.TrackFile) map[string]any {
	if f == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         f.GetId(),
		"artist_id":  f.GetArtistId(),
		"album_id":   f.GetAlbumId(),
		"title":      f.GetTitle(),
		"quality":    f.GetQuality(),
		"size_bytes": f.GetSizeBytes(),
		"container":  f.GetContainer(),
		"created_at": f.GetCreatedAt(),
		"filename":   filepath.Base(f.GetFilePath()),
	}
}

func (s *server) handleListMusicTrackFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "files.id_required"})
		return
	}
	if s.music == nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	listed, err := s.music.ListTrackFiles(ctx, &musicv1.ListTrackFilesRequest{
		ArtistId: id,
		AlbumId:  strings.TrimSpace(r.URL.Query().Get("album_id")),
	})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "error": err.Error()})
		return
	}
	items := make([]map[string]any, 0, len(listed.GetFiles()))
	for _, f := range listed.GetFiles() {
		if f == nil || strings.TrimSpace(f.GetId()) == "" {
			continue
		}
		items = append(items, publicTrackFile(f))
	}
	writeJSON(w, map[string]any{"available": true, "items": items})
}

func (s *server) handleDeleteMusicTrackFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "files.forbidden"})
		return
	}
	artistID := strings.TrimSpace(r.PathValue("id"))
	fileID := strings.TrimSpace(r.PathValue("fileId"))
	if artistID == "" || fileID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "files.id_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "files.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	listed, err := s.music.ListTrackFiles(ctx, &musicv1.ListTrackFilesRequest{ArtistId: artistID})
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
	if _, err := s.music.RemoveTrackFile(ctx, &musicv1.RemoveTrackFileRequest{FileId: fileID, DeleteFiles: deleteFiles}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "files.remove_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": fileID, "delete_files": deleteFiles})
}

func (s *server) handleImportMusicAlbum(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "import.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Path string `json:"path"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	path := strings.TrimSpace(body.Path)
	if id == "" || path == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and path are required", "code": "import.path_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "import.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.music.AddTrackFile(ctx, &musicv1.AddTrackFileRequest{AlbumId: id, FilePath: path})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "import.failed"})
		return
	}
	fileID := strings.TrimSpace(resp.GetFileId())
	out := map[string]any{"imported": true, "id": fileID}
	if fileID != "" {
		out["stream_url"] = "/stream/music/" + fileID
	}
	writeJSON(w, out)
}
