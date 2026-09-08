package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
)

func (s *server) handlePatchMusicArtist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "monitor.id_required"})
		return
	}
	body := readLibraryPatch(r)
	if !body.hasLibraryFields() {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "monitored, quality_profile_id, or root_folder_path is required", "code": "monitor.patch_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "monitor.unavailable"})
		return
	}
	req := &musicv1.UpdateArtistRequest{Id: id, Monitored: body.Monitored}
	if body.QualityProfileID != nil {
		qp := strings.TrimSpace(*body.QualityProfileID)
		req.QualityProfileId = &qp
	}
	if body.RootFolderPath != nil {
		path := strings.TrimSpace(*body.RootFolderPath)
		req.RootFolderPath = &path
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.music.UpdateArtist(ctx, req); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	out := map[string]any{"id": id}
	if body.Monitored != nil {
		out["monitored"] = *body.Monitored
	}
	if body.QualityProfileID != nil {
		out["quality_profile_id"] = strings.TrimSpace(*body.QualityProfileID)
	}
	if body.RootFolderPath != nil {
		out["root_folder_path"] = strings.TrimSpace(*body.RootFolderPath)
	}
	writeJSON(w, out)
}

func (s *server) handlePatchMusicAlbum(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	body := readLibraryPatch(r)
	if id == "" || body.Monitored == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and monitored are required", "code": "monitor.invalid"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "monitor.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.music.UpdateAlbumMonitored(ctx, &musicv1.UpdateAlbumMonitoredRequest{
		AlbumId:   id,
		Monitored: *body.Monitored,
	}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	writeJSON(w, map[string]any{"id": id, "monitored": *body.Monitored})
}

func (s *server) handleAddMusicAlbum(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "library.forbidden"})
		return
	}
	artistID := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Title     string `json:"title"`
		Year      int32  `json:"year"`
		Monitored *bool  `json:"monitored"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "library.invalid_json"})
			return
		}
	}
	title := strings.TrimSpace(body.Title)
	if artistID == "" || title == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and title are required", "code": "library.title_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "library.unavailable"})
		return
	}
	monitored := true
	if body.Monitored != nil {
		monitored = *body.Monitored
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.music.AddAlbum(ctx, &musicv1.AddAlbumRequest{
		ArtistId:  artistID,
		Title:     title,
		Year:      body.Year,
		Monitored: monitored,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	al := resp.GetAlbum()
	writeJSON(w, map[string]any{
		"added": true,
		"album": map[string]any{
			"id":        al.GetId(),
			"artist_id": al.GetArtistId(),
			"title":     al.GetTitle(),
			"year":      al.GetYear(),
			"monitored": al.GetMonitored(),
		},
	})
}

func (s *server) handleAddMusicArtist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "library.forbidden"})
		return
	}
	var body struct {
		Name             string `json:"name"`
		Monitored        *bool  `json:"monitored"`
		QualityProfileID string `json:"quality_profile_id"`
		RootFolderPath   string `json:"root_folder_path"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "library.invalid_json"})
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name is required", "code": "library.name_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "library.unavailable"})
		return
	}
	monitored := true
	if body.Monitored != nil {
		monitored = *body.Monitored
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.music.AddArtist(ctx, &musicv1.AddArtistRequest{
		Name:             name,
		Monitored:        monitored,
		QualityProfileId: strings.TrimSpace(body.QualityProfileID),
		RootFolderPath:   strings.TrimSpace(body.RootFolderPath),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	ar := resp.GetArtist()
	writeJSON(w, map[string]any{
		"added": true,
		"artist": map[string]any{
			"id":                 ar.GetId(),
			"name":               ar.GetName(),
			"monitored":          ar.GetMonitored(),
			"quality_profile_id": ar.GetQualityProfileId(),
			"root_folder_path":   ar.GetRootFolderPath(),
		},
	})
}
