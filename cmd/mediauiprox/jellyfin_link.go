package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
)

func (s *server) handleJellyfinLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	muxID := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("mux_id"), r.URL.Query().Get("muxId")))
	if muxID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "mux_id required", "code": "jellyfin.mux_id_required"})
		return
	}
	if s.jellyfin == nil {
		writeJSON(w, map[string]any{"available": false, "linked": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	list, err := s.jellyfin.ListItemLinks(ctx, &jellyfinv1.ListItemLinksRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "linked": false, "error": err.Error()})
		return
	}
	for _, link := range list.GetLinks() {
		if link == nil || link.GetMuxcoreId() != muxID {
			continue
		}
		writeJSON(w, jellyfinLinkJSON(link, true))
		return
	}
	writeJSON(w, map[string]any{"available": true, "linked": false, "muxId": muxID})
}

func (s *server) handleDeleteJellyfinLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "jellyfin.forbidden"})
		return
	}
	if s.jellyfin == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "jellyfin bridge is not connected", "code": "jellyfin.unavailable"})
		return
	}
	muxID := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("mux_id"), r.URL.Query().Get("muxId")))
	jfID := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("jellyfin_id"), r.URL.Query().Get("jellyfinId")))
	if muxID == "" && jfID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "mux_id or jellyfin_id required", "code": "jellyfin.link_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.jellyfin.DeleteItemLink(ctx, &jellyfinv1.DeleteItemLinkRequest{
		MuxcoreId:  muxID,
		JellyfinId: jfID,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "jellyfin.unlink_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": resp.GetOk(), "muxId": muxID, "jellyfinId": jfID})
}

func (s *server) handleJellyfinMatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "jellyfin.forbidden"})
		return
	}
	if s.jellyfin == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "jellyfin bridge is not connected", "code": "jellyfin.unavailable"})
		return
	}
	var body struct {
		MuxID       string            `json:"mux_id"`
		Path        string            `json:"path"`
		Title       string            `json:"title"`
		MediaKind   string            `json:"media_kind"`
		TmdbID      int               `json:"tmdb_id"`
		ProviderIDs map[string]string `json:"provider_ids"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "jellyfin.invalid_json"})
			return
		}
	}
	muxID := strings.TrimSpace(body.MuxID)
	if muxID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "mux_id required", "code": "jellyfin.mux_id_required"})
		return
	}
	providers := body.ProviderIDs
	if providers == nil {
		providers = map[string]string{}
	}
	if body.TmdbID > 0 {
		if _, ok := providers["Tmdb"]; !ok {
			providers["Tmdb"] = strconv.Itoa(body.TmdbID)
		}
	}
	kind := strings.ToLower(strings.TrimSpace(body.MediaKind))
	if kind == "" {
		kind = "movie"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resp, err := s.jellyfin.MatchItem(ctx, &jellyfinv1.MatchItemRequest{
		MuxcoreId:   muxID,
		Path:        strings.TrimSpace(body.Path),
		ProviderIds: providers,
		MediaKind:   kind,
		Title:       strings.TrimSpace(body.Title),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "jellyfin.match_failed"})
		return
	}
	out := jellyfinLinkJSON(resp.GetLink(), resp.GetMatched())
	out["matchReason"] = resp.GetMatchReason()
	out["matched"] = resp.GetMatched()
	writeJSON(w, out)
}

func jellyfinLinkJSON(link *jellyfinv1.ItemLink, linked bool) map[string]any {
	if link == nil {
		return map[string]any{"available": true, "linked": false, "matched": false}
	}
	jfID := strings.TrimSpace(link.GetJellyfinId())
	return map[string]any{
		"available":  true,
		"linked":     linked && jfID != "",
		"matched":    linked && jfID != "",
		"muxId":      link.GetMuxcoreId(),
		"jellyfinId": jfID,
		"path":       link.GetPath(),
		"title":      link.GetTitle(),
		"mediaKind":  link.GetMediaKind(),
	}
}
