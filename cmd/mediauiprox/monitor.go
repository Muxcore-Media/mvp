package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

func (s *server) syncWantedFromLibraryPatch(ctx context.Context, libraryID string, body libraryPatchBody) {
	if s.automation == nil || strings.TrimSpace(libraryID) == "" {
		return
	}
	req := &automationv1.UpdateQueueItemRequest{QueueId: libraryID, Monitored: body.Monitored}
	if body.QualityProfileID != nil {
		req.QualityProfileId = strings.TrimSpace(*body.QualityProfileID)
	}
	if req.Monitored == nil && req.QualityProfileId == "" {
		return
	}
	_, _ = s.automation.UpdateQueueItem(ctx, req)
}

type libraryPatchBody struct {
	Monitored        *bool   `json:"monitored"`
	QualityProfileID *string `json:"quality_profile_id"`
	RootFolderPath   *string `json:"root_folder_path"`
}

func (b libraryPatchBody) hasLibraryFields() bool {
	if b.Monitored != nil {
		return true
	}
	if b.QualityProfileID != nil && strings.TrimSpace(*b.QualityProfileID) != "" {
		return true
	}
	if b.RootFolderPath != nil && strings.TrimSpace(*b.RootFolderPath) != "" {
		return true
	}
	return false
}

func readLibraryPatch(r *http.Request) libraryPatchBody {
	var body libraryPatchBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	return body
}

func (s *server) handlePatchMovie(w http.ResponseWriter, r *http.Request) {
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
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies module is not connected", "code": "monitor.unavailable"})
		return
	}
	req := &mgmntv1.UpdateMovieRequest{MovieId: id, Monitored: body.Monitored}
	if body.QualityProfileID != nil {
		id := strings.TrimSpace(*body.QualityProfileID)
		req.QualityProfileId = &id
	}
	if body.RootFolderPath != nil {
		path := strings.TrimSpace(*body.RootFolderPath)
		req.RootFolderPath = &path
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.movies.UpdateMovie(ctx, req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	out := map[string]any{"movie": movieJSON(resp.GetMovie())}
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

func (s *server) handlePatchTV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "monitor.id_required"})
		return
	}
	body := readLibraryPatch(r)
	if !body.hasLibraryFields() {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "monitored, quality_profile_id, or root_folder_path is required", "code": "monitor.patch_required"})
		return
	}
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv module is not connected", "code": "monitor.unavailable"})
		return
	}
	req := &tvmgmtv1.UpdateTVShowRequest{SeriesId: id, Monitored: body.Monitored}
	if body.QualityProfileID != nil {
		pid := strings.TrimSpace(*body.QualityProfileID)
		req.QualityProfileId = &pid
	}
	if body.RootFolderPath != nil {
		path := strings.TrimSpace(*body.RootFolderPath)
		req.RootFolderPath = &path
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.tv.UpdateTVShow(ctx, req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	out := map[string]any{"show": tvJSON(resp.GetSeries())}
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

func (s *server) handlePatchTVSeason(w http.ResponseWriter, r *http.Request) {
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
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv module is not connected", "code": "monitor.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.tv.UpdateSeasonMonitored(ctx, &tvmgmtv1.UpdateSeasonMonitoredRequest{SeasonId: id, Monitored: *body.Monitored}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	writeJSON(w, map[string]any{"id": id, "monitored": *body.Monitored})
}

func (s *server) handlePatchEpisode(w http.ResponseWriter, r *http.Request) {
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
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv module is not connected", "code": "monitor.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.tv.UpdateEpisodeMonitored(ctx, &tvmgmtv1.UpdateEpisodeMonitoredRequest{EpisodeId: id, Monitored: *body.Monitored}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	writeJSON(w, map[string]any{"id": id, "monitored": *body.Monitored})
}
