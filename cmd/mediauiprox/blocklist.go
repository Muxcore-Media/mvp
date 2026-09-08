package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
)

func (s *server) handleListBlocklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSON(w, map[string]any{"items": []any{}, "total": 0, "available": false})
		return
	}
	page := int32(1)
	pageSize := int32(50)
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			page = int32(n) //nolint:gosec // page is a positive query bound
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			pageSize = int32(n) //nolint:gosec // page_size is a positive query bound
		}
	}
	resp, err := s.automation.ListBlocklist(r.Context(), &automationv1.ListBlocklistRequest{
		Page:         page,
		PageSize:     pageSize,
		WantedItemId: strings.TrimSpace(r.URL.Query().Get("wanted_item_id")),
	})
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
		})
		return
	}
	items := make([]map[string]any, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		items = append(items, map[string]any{
			"wanted_item_id": e.GetWantedItemId(),
			"guid":           e.GetGuid(),
			"title":          e.GetTitle(),
			"reason":         e.GetReason(),
			"loop":           e.GetLoop(),
			"created_at":     e.GetCreatedAt(),
		})
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
		"available": true,
	})
}

func (s *server) handleClearBlocklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "automation unavailable", "code": "blocklist.unavailable"})
		return
	}
	var body struct {
		ClearAll     bool   `json:"clear_all"`
		WantedItemID string `json:"wanted_item_id"`
		GUID         string `json:"guid"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if !body.ClearAll && strings.TrimSpace(body.WantedItemID) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "wanted_item_id or clear_all required", "code": "blocklist.clear_required"})
		return
	}
	resp, err := s.automation.ClearBlocklist(r.Context(), &automationv1.ClearBlocklistRequest{
		ClearAll:     body.ClearAll,
		WantedItemId: strings.TrimSpace(body.WantedItemID),
		Guid:         strings.TrimSpace(body.GUID),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "blocklist.clear_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": resp.GetRemoved()})
}
