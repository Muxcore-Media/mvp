package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
)

func publicListSource(src *listsyncv1.ListSource) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                    src.GetId(),
		"name":                  src.GetName(),
		"type":                  src.GetType(),
		"enabled":               src.GetEnabled(),
		"username":              src.GetUsername(),
		"client_id":             src.GetClientId(),
		"list_url":              src.GetListUrl(),
		"sync_interval_minutes": src.GetSyncIntervalMinutes(),
		"last_synced":           src.GetLastSynced(),
		"base_url":              src.GetBaseUrl(),
		"quality_profile_id":    src.GetQualityProfileId(),
		"root_folder_path":      src.GetRootFolderPath(),
		"has_api_key":           strings.TrimSpace(src.GetApiKey()) != "",
	}
}

func normalizeListSourceType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "imdb":
		return "imdb"
	case "plex":
		return "plex"
	case "jellyfin", "emby":
		return "jellyfin"
	case "radarr":
		return "radarr"
	case "sonarr":
		return "sonarr"
	default:
		return "trakt"
	}
}

func (s *server) handleListSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	if s.listSync == nil {
		writeJSON(w, map[string]any{"available": false, "sources": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.listSync.ListSources(ctx, &listsyncv1.ListSourcesRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "sources": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetSources()))
	for _, src := range resp.GetSources() {
		if src == nil {
			continue
		}
		out = append(out, publicListSource(src))
	}
	writeJSON(w, map[string]any{"available": true, "sources": out})
}

func (s *server) handleCreateListSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	if s.listSync == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "list sync unavailable", "code": "lists.unavailable"})
		return
	}
	var body struct {
		Name                string `json:"name"`
		Type                string `json:"type"`
		Username            string `json:"username"`
		ClientID            string `json:"client_id"`
		ListURL             string `json:"list_url"`
		SyncIntervalMinutes int32  `json:"sync_interval_minutes"`
		BaseURL             string `json:"base_url"`
		APIKey              string `json:"api_key"`
		QualityProfileID    string `json:"quality_profile_id"`
		RootFolderPath      string `json:"root_folder_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "lists.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name required", "code": "lists.name_required"})
		return
	}
	interval := body.SyncIntervalMinutes
	if interval <= 0 {
		interval = 1440
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.listSync.AddSource(ctx, &listsyncv1.AddSourceRequest{
		Name:                name,
		Type:                normalizeListSourceType(body.Type),
		Username:            strings.TrimSpace(body.Username),
		ClientId:            strings.TrimSpace(body.ClientID),
		ListUrl:             strings.TrimSpace(body.ListURL),
		SyncIntervalMinutes: interval,
		BaseUrl:             strings.TrimSpace(body.BaseURL),
		ApiKey:              strings.TrimSpace(body.APIKey),
		QualityProfileId:    strings.TrimSpace(body.QualityProfileID),
		RootFolderPath:      strings.TrimSpace(body.RootFolderPath),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "lists.create_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "source": publicListSource(resp.GetSource())})
}

func (s *server) handleUpdateListSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "lists.id_required"})
		return
	}
	if s.listSync == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "list sync unavailable", "code": "lists.unavailable"})
		return
	}
	var body struct {
		Enabled             *bool  `json:"enabled"`
		Name                string `json:"name"`
		Username            string `json:"username"`
		ClientID            string `json:"client_id"`
		ListURL             string `json:"list_url"`
		SyncIntervalMinutes *int32 `json:"sync_interval_minutes"`
		BaseURL             string `json:"base_url"`
		APIKey              string `json:"api_key"`
		QualityProfileID    string `json:"quality_profile_id"`
		RootFolderPath      string `json:"root_folder_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "lists.invalid_json"})
		return
	}
	req := &listsyncv1.UpdateSourceRequest{Id: id}
	if body.Enabled != nil {
		req.Enabled = body.Enabled
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		req.Name = &name
	}
	if username := strings.TrimSpace(body.Username); username != "" {
		req.Username = &username
	}
	if clientID := strings.TrimSpace(body.ClientID); clientID != "" {
		req.ClientId = &clientID
	}
	if listURL := strings.TrimSpace(body.ListURL); listURL != "" {
		req.ListUrl = &listURL
	}
	if body.SyncIntervalMinutes != nil {
		req.SyncIntervalMinutes = body.SyncIntervalMinutes
	}
	if baseURL := strings.TrimSpace(body.BaseURL); baseURL != "" {
		req.BaseUrl = &baseURL
	}
	if apiKey := strings.TrimSpace(body.APIKey); apiKey != "" {
		req.ApiKey = &apiKey
	}
	if qualityID := strings.TrimSpace(body.QualityProfileID); qualityID != "" {
		req.QualityProfileId = &qualityID
	}
	if rootPath := strings.TrimSpace(body.RootFolderPath); rootPath != "" {
		req.RootFolderPath = &rootPath
	}
	if req.Enabled == nil && req.Name == nil && req.Username == nil && req.ClientId == nil && req.ListUrl == nil &&
		req.SyncIntervalMinutes == nil && req.BaseUrl == nil && req.ApiKey == nil && req.QualityProfileId == nil && req.RootFolderPath == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "no fields to update", "code": "lists.empty_update"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.listSync.UpdateSource(ctx, req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "lists.update_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "source": publicListSource(resp.GetSource())})
}

func (s *server) handleDeleteListSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "lists.id_required"})
		return
	}
	if s.listSync == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "list sync unavailable", "code": "lists.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.listSync.RemoveSource(ctx, &listsyncv1.RemoveSourceRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "lists.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}

func (s *server) handleSyncListSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	if s.listSync == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "list sync unavailable", "code": "lists.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	resp, err := s.listSync.SyncNow(ctx, &listsyncv1.SyncNowRequest{})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "lists.sync_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"started":     true,
		"items_found": resp.GetItemsFound(),
		"items_new":   resp.GetItemsNew(),
	})
}

func (s *server) handleSyncListSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.EqualFold(id, "sync") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "lists.id_required"})
		return
	}
	if s.listSync == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "list sync unavailable", "code": "lists.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	resp, err := s.listSync.SyncNow(ctx, &listsyncv1.SyncNowRequest{SourceId: id})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "lists.sync_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"started":     true,
		"id":          id,
		"items_found": resp.GetItemsFound(),
		"items_new":   resp.GetItemsNew(),
	})
}

func (s *server) handleTestListSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "lists.id_required"})
		return
	}
	if s.listSync == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "list sync unavailable", "code": "lists.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.listSync.TestSource(ctx, &listsyncv1.TestSourceRequest{SourceId: id})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "lists.test_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"ok":          resp.GetOk(),
		"id":          id,
		"message":     resp.GetMessage(),
		"items_found": resp.GetItemsFound(),
	})
}

func publicSyncLog(entry *listsyncv1.SyncLogEntry) map[string]any {
	if entry == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":           entry.GetId(),
		"source_id":    entry.GetSourceId(),
		"source_name":  entry.GetSourceName(),
		"status":       entry.GetStatus(),
		"items_found":  entry.GetItemsFound(),
		"items_new":    entry.GetItemsNew(),
		"error":        entry.GetError(),
		"started_at":   entry.GetStartedAt(),
		"completed_at": entry.GetCompletedAt(),
	}
}

func publicSyncItem(item *listsyncv1.SyncItem) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":              item.GetId(),
		"source_id":       item.GetSourceId(),
		"external_id":     item.GetExternalId(),
		"tmdb_id":         item.GetTmdbId(),
		"imdb_id":         item.GetImdbId(),
		"media_type":      item.GetMediaType(),
		"title":           item.GetTitle(),
		"year":            item.GetYear(),
		"season_number":   item.GetSeasonNumber(),
		"episode_number":  item.GetEpisodeNumber(),
		"action":          item.GetAction(),
		"status":          item.GetStatus(),
		"matched_item_id": item.GetMatchedItemId(),
		"created_at":      item.GetCreatedAt(),
		"updated_at":      item.GetUpdatedAt(),
	}
}

func (s *server) handleListSyncHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	if s.listSync == nil {
		writeJSON(w, map[string]any{"available": false, "entries": []any{}, "total": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.listSync.GetHistory(ctx, &listsyncv1.GetHistoryRequest{Page: 1, PageSize: 50})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "entries": []any{}, "total": 0, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetEntries()))
	for _, entry := range resp.GetEntries() {
		if entry == nil {
			continue
		}
		out = append(out, publicSyncLog(entry))
	}
	writeJSON(w, map[string]any{"available": true, "entries": out, "total": resp.GetTotal()})
}

func (s *server) handleListSyncItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "lists.forbidden"})
		return
	}
	if s.listSync == nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "total": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.listSync.GetItems(ctx, &listsyncv1.GetItemsRequest{
		Page:        1,
		PageSize:    50,
		SourceId:    strings.TrimSpace(r.URL.Query().Get("source_id")),
		MediaType:   strings.TrimSpace(r.URL.Query().Get("media_type")),
		MatchedOnly: r.URL.Query().Get("matched_only") == "true",
	})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "total": 0, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		if item == nil {
			continue
		}
		out = append(out, publicSyncItem(item))
	}
	writeJSON(w, map[string]any{
		"available": true,
		"items":     out,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
	})
}
