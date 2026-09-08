package main

import (
	"encoding/json"
	"net/http"
	"strings"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
)

func seriesOverrideJSON(o *automationv1.SeriesOverride) map[string]any {
	if o == nil {
		return map[string]any{
			"series_id":        "",
			"delay_minutes":    0,
			"preferred_groups": []string{},
			"ignored_groups":   []string{},
		}
	}
	pref := o.GetPreferredGroups()
	if pref == nil {
		pref = []string{}
	}
	ign := o.GetIgnoredGroups()
	if ign == nil {
		ign = []string{}
	}
	return map[string]any{
		"series_id":        o.GetSeriesId(),
		"delay_minutes":    o.GetDelayMinutes(),
		"preferred_groups": pref,
		"ignored_groups":   ign,
	}
}

func (s *server) handleGetSeriesOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "override.id_required"})
		return
	}
	if s.automation == nil {
		writeJSON(w, map[string]any{"available": false, "found": false, "override": seriesOverrideJSON(nil)})
		return
	}
	resp, err := s.automation.ListSeriesOverrides(r.Context(), &automationv1.ListSeriesOverridesRequest{})
	if err != nil {
		writeJSON(w, map[string]any{
			"available": false, "found": false, "override": seriesOverrideJSON(nil), "error": err.Error(),
		})
		return
	}
	for _, o := range resp.GetOverrides() {
		if strings.EqualFold(strings.TrimSpace(o.GetSeriesId()), id) {
			writeJSON(w, map[string]any{"available": true, "found": true, "override": seriesOverrideJSON(o)})
			return
		}
	}
	writeJSON(w, map[string]any{"available": true, "found": false, "override": seriesOverrideJSON(&automationv1.SeriesOverride{SeriesId: id})})
}

func (s *server) handlePutSeriesOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "override.id_required"})
		return
	}
	if s.automation == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "automation unavailable", "code": "override.unavailable"})
		return
	}
	var body struct {
		DelayMinutes    *int32   `json:"delay_minutes"`
		PreferredGroups []string `json:"preferred_groups"`
		IgnoredGroups   []string `json:"ignored_groups"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "override.invalid_body"})
			return
		}
	}
	mins := int32(0)
	if body.DelayMinutes != nil {
		mins = *body.DelayMinutes
	}
	if mins < 0 || mins > 10080 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "delay_minutes must be 0–10080", "code": "override.delay_invalid"})
		return
	}
	resp, err := s.automation.UpsertSeriesOverride(r.Context(), &automationv1.UpsertSeriesOverrideRequest{
		Override: &automationv1.SeriesOverride{
			SeriesId:        id,
			DelayMinutes:    mins,
			PreferredGroups: body.PreferredGroups,
			IgnoredGroups:   body.IgnoredGroups,
		},
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "override.upsert_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "found": true, "override": seriesOverrideJSON(resp.GetOverride())})
}

func (s *server) handleDeleteSeriesOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "override.id_required"})
		return
	}
	if s.automation == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "automation unavailable", "code": "override.unavailable"})
		return
	}
	if _, err := s.automation.DeleteSeriesOverride(r.Context(), &automationv1.DeleteSeriesOverrideRequest{SeriesId: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "override.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true})
}
