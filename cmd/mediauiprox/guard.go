package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

func guardRuleTypeLabel(t guardv1.RuleType) string {
	switch t {
	case guardv1.RuleType_RULE_TYPE_IMPOSSIBLE_TRAVEL:
		return "impossible_travel"
	case guardv1.RuleType_RULE_TYPE_SIMULTANEOUS_LOCATIONS:
		return "simultaneous_locations"
	case guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY:
		return "device_velocity"
	case guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS:
		return "concurrent_streams"
	case guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION:
		return "geo_restriction"
	case guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY:
		return "account_inactivity"
	default:
		return ""
	}
}

func guardRuleTypeFromString(raw string) guardv1.RuleType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "impossible_travel":
		return guardv1.RuleType_RULE_TYPE_IMPOSSIBLE_TRAVEL
	case "simultaneous_locations":
		return guardv1.RuleType_RULE_TYPE_SIMULTANEOUS_LOCATIONS
	case "device_velocity":
		return guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY
	case "concurrent_streams":
		return guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS
	case "geo_restriction":
		return guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION
	case "account_inactivity":
		return guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY
	default:
		return guardv1.RuleType_RULE_TYPE_UNSPECIFIED
	}
}

func publicGuardRule(rule *guardv1.GuardRule) map[string]any {
	if rule == nil {
		return map[string]any{}
	}
	params := rule.GetParams()
	if params == nil {
		params = map[string]string{}
	}
	return map[string]any{
		"id":      rule.GetId(),
		"type":    guardRuleTypeLabel(rule.GetType()),
		"name":    rule.GetName(),
		"enabled": rule.GetEnabled(),
		"params":  params,
	}
}

func publicGuardViolation(v *guardv1.Violation) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"id":           v.GetId(),
		"ruleId":       v.GetRuleId(),
		"ruleType":     guardRuleTypeLabel(v.GetRuleType()),
		"userId":       v.GetUserId(),
		"userName":     v.GetUserName(),
		"summary":      v.GetSummary(),
		"severity":     v.GetSeverity(),
		"acknowledged": v.GetAcknowledged(),
	}
	if ts := v.GetCreatedAtUnix(); ts > 0 {
		out["createdAt"] = time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
	return out
}

func publicGuardTrust(score *guardv1.TrustScore) map[string]any {
	if score == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"userId":   score.GetUserId(),
		"userName": score.GetUserName(),
		"score":    score.GetScore(),
	}
	if ts := score.GetUpdatedAtUnix(); ts > 0 {
		out["updatedAt"] = time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
	return out
}

func (s *server) handleGetGuard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "guard.forbidden"})
		return
	}
	if s.playbackGuard == nil {
		writeJSON(w, map[string]any{"available": false, "rules": []any{}, "violations": []any{}, "trust": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rulesResp, err := s.playbackGuard.ListRules(ctx, &guardv1.ListRulesRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "rules": []any{}, "violations": []any{}, "trust": []any{}, "error": err.Error()})
		return
	}
	includeAck := strings.TrimSpace(r.URL.Query().Get("include_acknowledged")) == "1"
	violResp, err := s.playbackGuard.ListViolations(ctx, &guardv1.ListViolationsRequest{IncludeAcknowledged: includeAck, Limit: 50})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "rules": []any{}, "violations": []any{}, "trust": []any{}, "error": err.Error()})
		return
	}
	trustResp, err := s.playbackGuard.ListTrustScores(ctx, &guardv1.ListTrustScoresRequest{Limit: 40})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "rules": []any{}, "violations": []any{}, "trust": []any{}, "error": err.Error()})
		return
	}
	rules := make([]map[string]any, 0, len(rulesResp.GetRules()))
	for _, row := range rulesResp.GetRules() {
		if row.GetId() == "" && row.GetName() == "" {
			continue
		}
		rules = append(rules, publicGuardRule(row))
	}
	violations := make([]map[string]any, 0, len(violResp.GetViolations()))
	for _, row := range violResp.GetViolations() {
		if row.GetId() == "" {
			continue
		}
		violations = append(violations, publicGuardViolation(row))
	}
	trust := make([]map[string]any, 0, len(trustResp.GetScores()))
	for _, row := range trustResp.GetScores() {
		if row.GetUserId() == "" && row.GetUserName() == "" {
			continue
		}
		trust = append(trust, publicGuardTrust(row))
	}
	writeJSON(w, map[string]any{"available": true, "rules": rules, "violations": violations, "trust": trust})
}

func (s *server) handleUpsertGuardRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "guard.forbidden"})
		return
	}
	if s.playbackGuard == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback guard is not connected", "code": "guard.unavailable"})
		return
	}
	var body struct {
		ID      string            `json:"id"`
		Type    string            `json:"type"`
		Name    string            `json:"name"`
		Enabled bool              `json:"enabled"`
		Params  map[string]string `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "guard.invalid_json"})
		return
	}
	typ := guardRuleTypeFromString(body.Type)
	if typ == guardv1.RuleType_RULE_TYPE_UNSPECIFIED || strings.TrimSpace(body.Name) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "type and name required", "code": "guard.rule_required"})
		return
	}
	if body.Params == nil {
		body.Params = map[string]string{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.playbackGuard.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Id:      strings.TrimSpace(body.ID),
			Type:    typ,
			Name:    strings.TrimSpace(body.Name),
			Enabled: body.Enabled,
			Params:  body.Params,
		},
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "guard.upsert_failed"})
		return
	}
	writeJSON(w, publicGuardRule(resp.GetRule()))
}

func (s *server) handleDeleteGuardRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "guard.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "guard.id_required"})
		return
	}
	if s.playbackGuard == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback guard is not connected", "code": "guard.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.playbackGuard.DeleteRule(ctx, &guardv1.DeleteRuleRequest{Id: id})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "guard.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": resp.GetOk(), "id": id})
}

func (s *server) handleAckGuardViolations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "guard.forbidden"})
		return
	}
	if s.playbackGuard == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback guard is not connected", "code": "guard.unavailable"})
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "guard.invalid_json"})
		return
	}
	ids := make([]string, 0, len(body.IDs))
	for _, id := range body.IDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	if len(ids) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "ids required", "code": "guard.ids_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.playbackGuard.AcknowledgeViolations(ctx, &guardv1.AcknowledgeViolationsRequest{ViolationIds: ids})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "guard.ack_failed"})
		return
	}
	writeJSON(w, map[string]any{"updated": resp.GetUpdated()})
}

func (s *server) handleResetGuardTrust(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "guard.forbidden"})
		return
	}
	if s.playbackGuard == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback guard is not connected", "code": "guard.unavailable"})
		return
	}
	var body struct {
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "guard.invalid_json"})
		return
	}
	if strings.TrimSpace(body.UserID) == "" && strings.TrimSpace(body.UserName) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "user_id or user_name required", "code": "guard.user_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.playbackGuard.ResetTrustScore(ctx, &guardv1.ResetTrustScoreRequest{
		UserId:   strings.TrimSpace(body.UserID),
		UserName: strings.TrimSpace(body.UserName),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "guard.reset_failed"})
		return
	}
	writeJSON(w, publicGuardTrust(resp.GetScore()))
}

func (s *server) handleMergeGuardUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "guard.forbidden"})
		return
	}
	if s.playbackGuard == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback guard is not connected", "code": "guard.unavailable"})
		return
	}
	var body struct {
		SourceUserID   string `json:"source_user_id"`
		SourceUserName string `json:"source_user_name"`
		TargetUserID   string `json:"target_user_id"`
		TargetUserName string `json:"target_user_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "guard.invalid_json"})
		return
	}
	if strings.TrimSpace(body.SourceUserID) == "" && strings.TrimSpace(body.SourceUserName) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "source user required", "code": "guard.merge_source_required"})
		return
	}
	if strings.TrimSpace(body.TargetUserID) == "" && strings.TrimSpace(body.TargetUserName) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "target user required", "code": "guard.merge_target_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.playbackGuard.MergeUsers(ctx, &guardv1.MergeUsersRequest{
		SourceUserId:   strings.TrimSpace(body.SourceUserID),
		SourceUserName: strings.TrimSpace(body.SourceUserName),
		TargetUserId:   strings.TrimSpace(body.TargetUserID),
		TargetUserName: strings.TrimSpace(body.TargetUserName),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "guard.merge_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"violationsUpdated": resp.GetViolationsUpdated(),
		"aliasesCreated":    resp.GetAliasesCreated(),
		"sessionsUpdated":   resp.GetSessionsUpdated(),
	})
}
