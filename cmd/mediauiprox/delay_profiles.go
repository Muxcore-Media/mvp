package main

import (
	"encoding/json"
	"net/http"
	"strings"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
)

const maxDelayWaitMinutes int32 = 10080 // one week

func delayProfileJSON(p *automationv1.DelayProfile) map[string]any {
	if p == nil {
		return map[string]any{"protocol": "", "wait_minutes": 0}
	}
	return map[string]any{
		"protocol":     p.GetProtocol(),
		"wait_minutes": p.GetWaitMinutes(),
	}
}

func (s *server) handleListDelayProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSON(w, map[string]any{"profiles": []any{}, "available": false})
		return
	}
	resp, err := s.automation.ListDelayProfiles(r.Context(), &automationv1.ListDelayProfilesRequest{})
	if err != nil {
		writeJSON(w, map[string]any{
			"profiles":  []any{},
			"available": false,
			"error":     err.Error(),
		})
		return
	}
	profiles := make([]map[string]any, 0, len(resp.GetProfiles()))
	for _, p := range resp.GetProfiles() {
		profiles = append(profiles, delayProfileJSON(p))
	}
	writeJSON(w, map[string]any{"profiles": profiles, "available": true})
}

func (s *server) handleUpsertDelayProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "automation unavailable",
			"code":  "delay.unavailable",
		})
		return
	}
	var body struct {
		Protocol    string `json:"protocol"`
		WaitMinutes *int32 `json:"wait_minutes"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{
				"error": "invalid json",
				"code":  "delay.invalid_body",
			})
			return
		}
	}
	protocol := strings.ToLower(strings.TrimSpace(body.Protocol))
	if protocol == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "protocol required",
			"code":  "delay.protocol_required",
		})
		return
	}
	if body.WaitMinutes == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "wait_minutes required",
			"code":  "delay.wait_required",
		})
		return
	}
	mins := *body.WaitMinutes
	if mins < 0 || mins > maxDelayWaitMinutes {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "wait_minutes must be 0–10080",
			"code":  "delay.wait_invalid",
		})
		return
	}
	resp, err := s.automation.UpsertDelayProfile(r.Context(), &automationv1.UpsertDelayProfileRequest{
		Protocol:    protocol,
		WaitMinutes: mins,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(),
			"code":  "delay.upsert_failed",
		})
		return
	}
	writeJSON(w, delayProfileJSON(resp.GetProfile()))
}
