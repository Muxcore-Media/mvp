package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

func (s *server) handleGetEpisodeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "episodes.id_required"})
		return
	}
	if s.tvHTTP == nil {
		writeJSON(w, map[string]any{"available": false, "id": id})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	writeJSON(w, s.publicEpisodeFile(ctx, id))
}

func (s *server) publicEpisodeFile(ctx context.Context, episodeID string) map[string]any {
	path, fileID, quality := s.lookupEpisodeRenameFile(ctx, episodeID)
	if path == "" && fileID == "" {
		return map[string]any{"available": false, "id": episodeID}
	}
	out := map[string]any{
		"available": true,
		"id":        episodeID,
		"file_id":   fileID,
		"file_path": path,
		"filename":  filepath.Base(path),
		"quality":   quality,
	}
	return out
}

func applyEpisodeFile(ep map[string]any, file map[string]any) {
	if ep == nil || file == nil || file["available"] != true {
		return
	}
	if q, ok := file["quality"].(string); ok && q != "" {
		ep["quality"] = q
	}
	if id, ok := file["file_id"].(string); ok && id != "" {
		ep["file_id"] = id
	}
	if path, ok := file["file_path"].(string); ok && path != "" {
		ep["file_path"] = path
	}
	if name, ok := file["filename"].(string); ok && name != "" {
		ep["filename"] = name
	}
}

func (s *server) enrichTVEpisodeFiles(ctx context.Context, show map[string]any) {
	if s == nil || s.tvHTTP == nil || show == nil {
		return
	}
	seasons, ok := show["seasons"].([]map[string]any)
	if !ok {
		return
	}
	lookups := 0
	for _, season := range seasons {
		eps, ok := season["episodes"].([]map[string]any)
		if !ok {
			continue
		}
		for _, ep := range eps {
			if lookups >= 40 {
				return
			}
			hasFile, _ := ep["has_file"].(bool)
			id, _ := ep["id"].(string)
			if !hasFile || strings.TrimSpace(id) == "" {
				continue
			}
			applyEpisodeFile(ep, s.publicEpisodeFile(ctx, id))
			lookups++
		}
	}
}
