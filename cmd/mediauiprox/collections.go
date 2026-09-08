package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func collectionIDFromRequest(r *http.Request) (int32, string, bool) {
	idStr := strings.TrimSpace(r.PathValue("id"))
	if idStr == "" {
		idStr = strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/collections/"), "/")
	}
	if i := strings.Index(idStr, "/"); i >= 0 {
		idStr = idStr[:i]
	}
	if idStr == "" {
		return 0, "", false
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return 0, idStr, false
	}
	return int32(id), idStr, true
}

func publicCollectionPrefs(prefs *mgmntv1.CollectionPrefs) map[string]any {
	if prefs == nil {
		return map[string]any{
			"monitored": false, "search_on_add": true, "quality_profile_id": "", "root_folder_path": "",
		}
	}
	return map[string]any{
		"monitored":          prefs.GetMonitored(),
		"search_on_add":      prefs.GetSearchOnAdd(),
		"quality_profile_id": prefs.GetQualityProfileId(),
		"root_folder_path":   prefs.GetRootFolderPath(),
	}
}

func (s *server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	if s.movies == nil {
		writeJSON(w, map[string]any{"items": []any{}, "total": 0, "available": false, "source": "media-movies"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.movies.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "collections.gateway_error")
		return
	}
	items := make([]map[string]any, 0, len(resp.GetCollections()))
	for _, c := range resp.GetCollections() {
		items = append(items, map[string]any{
			"id":          strconv.Itoa(int(c.GetCollectionId())),
			"name":        c.GetName(),
			"movie_count": c.GetMovieCount(),
			"monitored":   c.GetMonitored(),
		})
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     len(items),
		"available": true,
		"source":    "media-movies",
	})
}

func (s *server) handleCollectionByID(w http.ResponseWriter, r *http.Request) {
	id, idStr, ok := collectionIDFromRequest(r)
	if !ok {
		if idStr != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid collection id", "collections.invalid_id")
			return
		}
		http.NotFound(w, r)
		return
	}
	if s.movies == nil {
		writeJSON(w, map[string]any{"available": false, "id": idStr, "movies": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.movies.GetCollectionMovies(ctx, &mgmntv1.GetCollectionMoviesRequest{CollectionId: id})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "collections.gateway_error")
		return
	}
	movies := make([]map[string]any, 0, len(resp.GetMovies()))
	for _, m := range resp.GetMovies() {
		movies = append(movies, movieJSON(m))
	}
	out := map[string]any{
		"id":        idStr,
		"name":      resp.GetName(),
		"movies":    movies,
		"total":     len(movies),
		"available": true,
	}
	if prefs, err := s.movies.GetCollectionPrefs(ctx, &mgmntv1.GetCollectionPrefsRequest{CollectionId: id}); err == nil {
		for k, v := range publicCollectionPrefs(prefs.GetPrefs()) {
			out[k] = v
		}
	}
	writeJSON(w, out)
}

func (s *server) handleSetCollectionMonitored(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "collections.forbidden"})
		return
	}
	id, _, ok := collectionIDFromRequest(r)
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid collection id", "code": "collections.invalid_id"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies unavailable", "code": "collections.unavailable"})
		return
	}
	var body struct {
		Monitored        *bool  `json:"monitored"`
		SearchOnAdd      *bool  `json:"search_on_add"`
		QualityProfileID string `json:"quality_profile_id"`
		RootFolderPath   string `json:"root_folder_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "collections.invalid_json"})
		return
	}
	req := &mgmntv1.SetCollectionMonitoredRequest{
		CollectionId:     id,
		QualityProfileId: strings.TrimSpace(body.QualityProfileID),
		RootFolderPath:   strings.TrimSpace(body.RootFolderPath),
		SearchOnAdd:      body.SearchOnAdd,
	}
	if body.Monitored != nil {
		req.Monitored = *body.Monitored
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.movies.SetCollectionMonitored(ctx, req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "collections.monitor_failed"})
		return
	}
	out := map[string]any{"ok": true, "id": strconv.Itoa(int(id))}
	for k, v := range publicCollectionPrefs(resp.GetPrefs()) {
		out[k] = v
	}
	writeJSON(w, out)
}

func (s *server) handleSyncCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "collections.forbidden"})
		return
	}
	id, idStr, ok := collectionIDFromRequest(r)
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid collection id", "code": "collections.invalid_id"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies unavailable", "code": "collections.unavailable"})
		return
	}
	var body struct {
		AddMissing *bool `json:"add_missing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "collections.invalid_json"})
		return
	}
	addMissing := true
	if body.AddMissing != nil {
		addMissing = *body.AddMissing
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.movies.SyncCollection(ctx, &mgmntv1.SyncCollectionRequest{CollectionId: id, AddMissing: addMissing})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "collections.sync_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"ok":               true,
		"id":               idStr,
		"added":            resp.GetAdded(),
		"already_present":  resp.GetAlreadyPresent(),
		"missing_on_source": resp.GetMissingOnSource(),
	})
}
