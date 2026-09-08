package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
)

func publicWantedItem(it *subtv1.WantedItem) map[string]any {
	if it == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":            it.GetId(),
		"media_id":      it.GetMediaId(),
		"title":         it.GetTitle(),
		"language":      it.GetLanguage(),
		"media_type":    it.GetMediaType(),
		"season":        it.GetSeason(),
		"episode":       it.GetEpisode(),
		"imdb_id":       it.GetImdbId(),
		"tmdb_id":       it.GetTmdbId(),
		"media_file_id": it.GetMediaFileId(),
	}
}

func publicSubtitleProvider(p *subtv1.SubtitleProvider) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":          p.GetId(),
		"name":        p.GetName(),
		"enabled":     p.GetEnabled(),
		"implemented": p.GetImplemented(),
	}
}

func publicHistoryEntry(e *subtv1.HistoryEntry) map[string]any {
	if e == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         e.GetId(),
		"title":      e.GetTitle(),
		"language":   e.GetLanguage(),
		"provider":   e.GetProvider(),
		"action":     e.GetAction(),
		"score":      e.GetScore(),
		"created_at": e.GetCreatedAt(),
	}
}

func publicBlacklistEntry(e *subtv1.BlacklistEntry) map[string]any {
	if e == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         e.GetId(),
		"provider":   e.GetProvider(),
		"title":      e.GetTitle(),
		"language":   e.GetLanguage(),
		"reason":     e.GetReason(),
		"created_at": e.GetCreatedAt(),
		"file_id":    e.GetFileId(),
	}
}

func publicLanguageProfile(p *subtv1.LanguageProfile) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	langs := make([]map[string]any, 0, len(p.GetLanguages()))
	for _, lr := range p.GetLanguages() {
		if lr == nil {
			continue
		}
		langs = append(langs, map[string]any{
			"language":          lr.GetLanguage(),
			"hearing_impaired":  lr.GetHearingImpaired(),
			"forced":            lr.GetForced(),
		})
	}
	return map[string]any{
		"id":         p.GetId(),
		"name":       p.GetName(),
		"languages":  langs,
		"is_default": p.GetIsDefault(),
	}
}

func (s *server) handleListSubtitleWanted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "wanted": []any{}, "total": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.ListWanted(ctx, &subtv1.ListWantedRequest{Page: 1, PageSize: 100})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "wanted": []any{}, "total": 0, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		out = append(out, publicWantedItem(it))
	}
	writeJSON(w, map[string]any{"available": true, "wanted": out, "total": resp.GetTotal()})
}

func (s *server) handleCreateSubtitleWanted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	var body struct {
		Title       string `json:"title"`
		Language    string `json:"language"`
		MediaType   string `json:"media_type"`
		Season      int32  `json:"season"`
		Episode     int32  `json:"episode"`
		IMDBID      string `json:"imdb_id"`
		TMDBID      int32  `json:"tmdb_id"`
		MediaFileID string `json:"media_file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "subtitles.invalid_json"})
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "title required", "code": "subtitles.title_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.UpsertWanted(ctx, &subtv1.UpsertWantedRequest{Item: &subtv1.WantedItem{
		Title: strings.TrimSpace(body.Title), Language: strings.TrimSpace(body.Language),
		MediaType: strings.TrimSpace(body.MediaType), Season: body.Season, Episode: body.Episode,
		ImdbId: strings.TrimSpace(body.IMDBID), TmdbId: body.TMDBID, MediaFileId: strings.TrimSpace(body.MediaFileID),
	}})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.wanted_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "item": publicWantedItem(resp.GetItem())})
}

func (s *server) handleDeleteSubtitleWanted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.subtitles.DeleteWanted(ctx, &subtv1.DeleteWantedRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.wanted_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}

func (s *server) handleSearchSubtitleWanted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	var body struct {
		MediaIDs []string `json:"media_ids"`
		Limit    int32    `json:"limit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	resp, err := s.subtitles.SearchWanted(ctx, &subtv1.SearchWantedRequest{MediaIds: body.MediaIDs, Limit: body.Limit})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.search_wanted_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "searched": resp.GetSearched(), "downloaded": resp.GetDownloaded()})
}

func (s *server) handleListSubtitleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "providers": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.ListProviders(ctx, &subtv1.ListProvidersRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "providers": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetProviders()))
	for _, p := range resp.GetProviders() {
		if p == nil {
			continue
		}
		out = append(out, publicSubtitleProvider(p))
	}
	writeJSON(w, map[string]any{"available": true, "providers": out})
}

func (s *server) handleSetSubtitleProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "subtitles.invalid_json"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.SetProviderEnabled(ctx, &subtv1.SetProviderEnabledRequest{Id: id, Enabled: enabled})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.provider_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "provider": publicSubtitleProvider(resp.GetProvider())})
}

func (s *server) handleListSubtitleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "history": []any{}, "total": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.ListHistory(ctx, &subtv1.ListHistoryRequest{Page: 1, PageSize: 40})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "history": []any{}, "total": 0, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		if e == nil {
			continue
		}
		out = append(out, publicHistoryEntry(e))
	}
	writeJSON(w, map[string]any{"available": true, "history": out, "total": resp.GetTotal()})
}

func (s *server) handleClearSubtitleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.subtitles.ClearHistory(ctx, &subtv1.ClearHistoryRequest{}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.history_failed"})
		return
	}
	writeJSON(w, map[string]any{"cleared": true})
}

func (s *server) handleListSubtitleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "profiles": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.ListLanguageProfiles(ctx, &subtv1.ListLanguageProfilesRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "profiles": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetProfiles()))
	for _, p := range resp.GetProfiles() {
		if p == nil {
			continue
		}
		out = append(out, publicLanguageProfile(p))
	}
	writeJSON(w, map[string]any{"available": true, "profiles": out})
}

func publicSubtitleLanguage(l *subtv1.LanguageInfo) map[string]any {
	if l == nil {
		return map[string]any{}
	}
	return map[string]any{
		"code": l.GetCode(),
		"name": l.GetName(),
	}
}

func (s *server) handleListSubtitleLanguages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "languages": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.ListLanguages(ctx, &subtv1.ListLanguagesRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "languages": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetLanguages()))
	for _, l := range resp.GetLanguages() {
		if l == nil || strings.TrimSpace(l.GetCode()) == "" {
			continue
		}
		out = append(out, publicSubtitleLanguage(l))
	}
	writeJSON(w, map[string]any{"available": true, "languages": out})
}

func (s *server) handleUpsertSubtitleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	var body struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Languages string `json:"languages"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "subtitles.invalid_json"})
		return
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Languages) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name and languages required", "code": "subtitles.profile_required"})
		return
	}
	var reqs []*subtv1.LanguageRequirement
	for _, part := range strings.Split(body.Languages, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		lr := &subtv1.LanguageRequirement{}
		bits := strings.Split(part, "+")
		lr.Language = bits[0]
		for _, b := range bits[1:] {
			switch b {
			case "hi", "hearing_impaired", "sdh":
				lr.HearingImpaired = true
			case "forced", "force":
				lr.Forced = true
			}
		}
		reqs = append(reqs, lr)
	}
	if len(reqs) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "no valid languages", "code": "subtitles.profile_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.UpsertLanguageProfile(ctx, &subtv1.UpsertLanguageProfileRequest{
		Profile: &subtv1.LanguageProfile{Id: strings.TrimSpace(body.ID), Name: strings.TrimSpace(body.Name), Languages: reqs, IsDefault: body.IsDefault},
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.profile_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "profile": publicLanguageProfile(resp.GetProfile())})
}

func (s *server) handleListSubtitleBlacklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "entries": []any{}, "total": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.ListBlacklist(ctx, &subtv1.ListBlacklistRequest{Page: 1, PageSize: 100})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "entries": []any{}, "total": 0, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		if e == nil {
			continue
		}
		out = append(out, publicBlacklistEntry(e))
	}
	writeJSON(w, map[string]any{"available": true, "entries": out, "total": resp.GetTotal()})
}

func publicSubtitleMedia(it *subtv1.SubtitleMediaItem) map[string]any {
	if it == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                  it.GetId(),
		"title":               it.GetTitle(),
		"media_type":          it.GetMediaType(),
		"monitored":           it.GetMonitored(),
		"language_profile_id": it.GetLanguageProfileId(),
		"season":              it.GetSeason(),
		"episode":             it.GetEpisode(),
		"series_id":           it.GetSeriesId(),
		"series_name":         it.GetSeriesName(),
		"imdb_id":             it.GetImdbId(),
		"tmdb_id":             it.GetTmdbId(),
		"year":                it.GetYear(),
		"has_file":            it.GetHasFile(),
	}
}

func (s *server) handleListSubtitleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "total": 0})
		return
	}
	page := int32(1)
	if n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page"))); err == nil && n > 0 {
		page = int32(n)
	}
	pageSize := int32(200)
	if n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page_size"))); err == nil && n > 0 && n <= 500 {
		pageSize = int32(n)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.ListMedia(ctx, &subtv1.ListMediaRequest{
		Page:     page,
		PageSize: pageSize,
		SeriesId: strings.TrimSpace(r.URL.Query().Get("series_id")),
	})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "total": 0, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil || (it.GetId() == "" && it.GetTitle() == "") {
			continue
		}
		out = append(out, publicSubtitleMedia(it))
	}
	writeJSON(w, map[string]any{"available": true, "items": out, "total": resp.GetTotal()})
}

func (s *server) handlePatchSubtitleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	var body struct {
		LanguageProfileID *string `json:"language_profile_id"`
		Monitored         *bool   `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "subtitles.invalid_json"})
		return
	}
	if body.LanguageProfileID == nil && body.Monitored == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "language_profile_id or monitored required", "code": "subtitles.media_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if body.LanguageProfileID != nil {
		if _, err := s.subtitles.SetMediaLanguageProfile(ctx, &subtv1.SetMediaLanguageProfileRequest{
			MediaId:           id,
			LanguageProfileId: strings.TrimSpace(*body.LanguageProfileID),
		}); err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.media_failed"})
			return
		}
	}
	if body.Monitored != nil {
		if _, err := s.subtitles.MassEditMedia(ctx, &subtv1.MassEditMediaRequest{
			MediaIds:     []string{id},
			SetMonitored: true,
			Monitored:    *body.Monitored,
		}); err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.media_failed"})
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *server) handleMassEditSubtitleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	var body struct {
		MediaIDs          []string `json:"media_ids"`
		LanguageProfileID string   `json:"language_profile_id"`
		SetMonitored      bool     `json:"set_monitored"`
		Monitored         bool     `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "subtitles.invalid_json"})
		return
	}
	ids := make([]string, 0, len(body.MediaIDs))
	for _, id := range body.MediaIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "media_ids required", "code": "subtitles.media_required"})
		return
	}
	if strings.TrimSpace(body.LanguageProfileID) == "" && !body.SetMonitored {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "language_profile_id or set_monitored required", "code": "subtitles.media_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.subtitles.MassEditMedia(ctx, &subtv1.MassEditMediaRequest{
		MediaIds:          ids,
		SetMonitored:      body.SetMonitored,
		Monitored:         body.Monitored,
		LanguageProfileId: strings.TrimSpace(body.LanguageProfileID),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.media_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "updated": resp.GetUpdated()})
}

func (s *server) handleRemoveSubtitleBlacklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.subtitles.RemoveBlacklist(ctx, &subtv1.RemoveBlacklistRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.blacklist_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}
