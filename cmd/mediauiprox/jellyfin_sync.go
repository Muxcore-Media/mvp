package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
)

func (s *server) handleJellyfinStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "jellyfin.forbidden"})
		return
	}
	if s.jellyfin == nil {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	st, err := s.jellyfin.Status(ctx, &jellyfinv1.StatusRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{
		"available":           true,
		"configured":          st.GetConfigured(),
		"baseUrl":             st.GetBaseUrl(),
		"conflictMode":        st.GetConflictMode(),
		"itemLinks":           st.GetItemLinks(),
		"sessionsPollEnabled": st.GetSessionsPollEnabled(),
		"userdataSync":        st.GetUserdataSync(),
		"sseConnected":        st.GetSseConnected(),
	})
}

func (s *server) handleJellyfinSync(w http.ResponseWriter, r *http.Request) {
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
		Direction string `json:"direction"`
		DryRun    bool   `json:"dry_run"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "jellyfin.invalid_json"})
			return
		}
	}
	dir := strings.ToLower(strings.TrimSpace(body.Direction))
	if dir == "" {
		dir = "both"
	}
	switch dir {
	case "both", "jellyfin", "muxcore":
	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "direction must be both, jellyfin, or muxcore", "code": "jellyfin.invalid_direction"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	resp, err := s.jellyfin.SyncLibrary(ctx, &jellyfinv1.SyncLibraryRequest{
		Direction: dir,
		DryRun:    body.DryRun,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "jellyfin.sync_failed"})
		return
	}
	errors := resp.GetErrors()
	if errors == nil {
		errors = []string{}
	}
	writeJSON(w, map[string]any{
		"available": true,
		"direction": dir,
		"dryRun":    body.DryRun,
		"scanned":   resp.GetScanned(),
		"matched":   resp.GetMatched(),
		"upserted":  resp.GetUpserted(),
		"removed":   resp.GetRemoved(),
		"errors":    errors,
	})
}

func (s *server) handleJellyfinRefresh(w http.ResponseWriter, r *http.Request) {
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
		ItemID string `json:"item_id"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "jellyfin.invalid_json"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.jellyfin.RefreshLibrary(ctx, &jellyfinv1.RefreshLibraryRequest{ItemId: strings.TrimSpace(body.ItemID)})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "jellyfin.refresh_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"ok":     resp.GetOk(),
		"itemId": strings.TrimSpace(body.ItemID),
	})
}
