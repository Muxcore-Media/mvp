package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

var watchNotifyEventTypes = map[string]bool{
	"playback.started":    true,
	"playback.stopped":    true,
	"guard.violation":     true,
	"media.request.ready": true,
}

var watchNotifyDestinationTypes = map[string]bool{
	"discord": true,
	"slack":   true,
	"webhook": true,
	"apprise": true,
}

func publicWatchNotifyFilters(raw any) map[string]any {
	rec, _ := raw.(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}
	return map[string]any{
		"userIds":        stringList(rec, "userIds", "user_ids"),
		"platforms":      stringList(rec, "platforms"),
		"mediaTypes":     stringList(rec, "mediaTypes", "media_types"),
		"transcodeOnly":  boolVal(rec, "transcodeOnly", "transcode_only"),
		"minDurationSec": floatVal(rec, "minDurationSec", "min_duration_sec"),
	}
}

func publicWatchNotifyRule(rec map[string]any) map[string]any {
	id := firstString(rec, "id", "ID")
	name := firstString(rec, "name", "Name")
	if id == "" && name == "" {
		return nil
	}
	return map[string]any{
		"id":              id,
		"name":            name,
		"enabled":         boolVal(rec, "enabled", "Enabled"),
		"eventType":       firstString(rec, "eventType", "event_type"),
		"titleTemplate":   firstString(rec, "titleTemplate", "title_template"),
		"messageTemplate": firstString(rec, "messageTemplate", "message_template"),
		"severity":        firstNonEmpty(firstString(rec, "severity", "Severity"), "info"),
		"filters":         publicWatchNotifyFilters(rec["filters"]),
		"destinationIds":  stringList(rec, "destinationIds", "destination_ids"),
		"createdAt":       firstString(rec, "createdAt", "created_at"),
		"updatedAt":       firstString(rec, "updatedAt", "updated_at"),
	}
}

func publicWatchNotifyDestination(rec map[string]any) map[string]any {
	id := firstString(rec, "id", "ID")
	name := firstString(rec, "name", "Name")
	if id == "" && name == "" {
		return nil
	}
	config, _ := rec["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	return map[string]any{
		"id":        id,
		"name":      name,
		"type":      firstString(rec, "type", "Type"),
		"enabled":   boolVal(rec, "enabled", "Enabled"),
		"config":    config,
		"events":    stringList(rec, "events", "Events"),
		"createdAt": firstString(rec, "createdAt", "created_at"),
		"updatedAt": firstString(rec, "updatedAt", "updated_at"),
	}
}

func watchNotifyRows(raw map[string]any, keys ...string) []map[string]any {
	out := make([]map[string]any, 0)
	if raw == nil {
		return out
	}
	for _, key := range keys {
		for _, row := range homeOrKey(raw, key) {
			rec, ok := row.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, rec)
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

func stringList(rec map[string]any, keys ...string) []string {
	out := make([]string, 0)
	if rec == nil {
		return out
	}
	for _, key := range keys {
		switch v := rec[key].(type) {
		case []any:
			for _, row := range v {
				s := strings.TrimSpace(watchNotifyString(row))
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		case []string:
			for _, row := range v {
				s := strings.TrimSpace(row)
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		case string:
			if strings.TrimSpace(v) == "" {
				continue
			}
			for _, part := range strings.Split(v, ",") {
				s := strings.TrimSpace(part)
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return out
}

func watchNotifyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return strings.TrimSpace(firstString(map[string]any{"v": t}, "v"))
	}
}

func allowedWatchNotifyEvents(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, ev := range raw {
		ev = strings.TrimSpace(ev)
		if !watchNotifyEventTypes[ev] || seen[ev] {
			continue
		}
		seen[ev] = true
		out = append(out, ev)
	}
	return out
}

func (s *server) handleGetWatchNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_notify.forbidden"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, map[string]any{"available": false, "rules": []any{}, "destinations": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rulesRaw, ok, err := s.playbackMonitorGET(ctx, "/notification/rules")
	if err != nil || !ok {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		writeJSON(w, map[string]any{"available": false, "rules": []any{}, "destinations": []any{}, "error": msg})
		return
	}
	destsRaw, destOK, destErr := s.playbackMonitorGET(ctx, "/notification/destinations")
	if destErr != nil || !destOK {
		msg := ""
		if destErr != nil {
			msg = destErr.Error()
		}
		writeJSON(w, map[string]any{"available": false, "rules": []any{}, "destinations": []any{}, "error": msg})
		return
	}
	rules := make([]map[string]any, 0)
	for _, rec := range watchNotifyRows(rulesRaw, "rules") {
		if pub := publicWatchNotifyRule(rec); pub != nil {
			rules = append(rules, pub)
		}
	}
	dests := make([]map[string]any, 0)
	for _, rec := range watchNotifyRows(destsRaw, "destinations") {
		if pub := publicWatchNotifyDestination(rec); pub != nil {
			dests = append(dests, pub)
		}
	}
	writeJSON(w, map[string]any{"available": true, "rules": rules, "destinations": dests})
}

func (s *server) handleUpsertWatchNotifyRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_notify.forbidden"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "watch_notify.unavailable"})
		return
	}
	var body struct {
		ID              string         `json:"id"`
		Name            string         `json:"name"`
		Enabled         *bool          `json:"enabled"`
		EventType       string         `json:"event_type"`
		EventTypeCamel  string         `json:"eventType"`
		TitleTemplate   string         `json:"title_template"`
		TitleCamel      string         `json:"titleTemplate"`
		MessageTemplate string         `json:"message_template"`
		MessageCamel    string         `json:"messageTemplate"`
		Severity        string         `json:"severity"`
		DestinationIDs  []string       `json:"destination_ids"`
		DestIDsCamel    []string       `json:"destinationIds"`
		Filters         map[string]any `json:"filters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "watch_notify.invalid_json"})
		return
	}
	eventType := firstNonEmpty(strings.TrimSpace(body.EventType), strings.TrimSpace(body.EventTypeCamel))
	if strings.TrimSpace(body.Name) == "" || !watchNotifyEventTypes[eventType] {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name and event_type required", "code": "watch_notify.rule_required"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	destIDs := body.DestinationIDs
	if len(destIDs) == 0 {
		destIDs = body.DestIDsCamel
	}
	if destIDs == nil {
		destIDs = []string{}
	}
	filters := publicWatchNotifyFilters(body.Filters)
	payload, _ := json.Marshal(map[string]any{
		"id":               strings.TrimSpace(body.ID),
		"name":             strings.TrimSpace(body.Name),
		"enabled":          enabled,
		"event_type":       eventType,
		"title_template":   firstNonEmpty(body.TitleTemplate, body.TitleCamel),
		"message_template": firstNonEmpty(body.MessageTemplate, body.MessageCamel),
		"severity":         firstNonEmpty(strings.TrimSpace(body.Severity), "info"),
		"destination_ids":  destIDs,
		"filters": map[string]any{
			"user_ids":         filters["userIds"],
			"platforms":        filters["platforms"],
			"media_types":      filters["mediaTypes"],
			"transcode_only":   filters["transcodeOnly"],
			"min_duration_sec": filters["minDurationSec"],
		},
	})
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	raw, err := s.playbackMonitorDo(ctx, http.MethodPost, "/notification/rules", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watch_notify.rule_failed"})
		return
	}
	rec := raw
	if nested, ok := raw["rule"].(map[string]any); ok {
		rec = nested
	}
	pub := publicWatchNotifyRule(rec)
	if pub == nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid watch notify response", "code": "watch_notify.invalid_response"})
		return
	}
	writeJSON(w, pub)
}

func (s *server) handleDeleteWatchNotifyRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_notify.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "watch_notify.id_required"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "watch_notify.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.playbackMonitorDo(ctx, http.MethodDelete, "/notification/rules/"+id, nil); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watch_notify.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *server) handleUpsertWatchNotifyDestination(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_notify.forbidden"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "watch_notify.unavailable"})
		return
	}
	var body struct {
		ID      string            `json:"id"`
		Name    string            `json:"name"`
		Type    string            `json:"type"`
		Enabled *bool             `json:"enabled"`
		Config  map[string]string `json:"config"`
		Events  []string          `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "watch_notify.invalid_json"})
		return
	}
	destType := strings.ToLower(strings.TrimSpace(body.Type))
	events := allowedWatchNotifyEvents(body.Events)
	if strings.TrimSpace(body.Name) == "" || !watchNotifyDestinationTypes[destType] || len(events) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name, type, and events required", "code": "watch_notify.destination_required"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if body.Config == nil {
		body.Config = map[string]string{}
	}
	payload, _ := json.Marshal(map[string]any{
		"id":      strings.TrimSpace(body.ID),
		"name":    strings.TrimSpace(body.Name),
		"type":    destType,
		"enabled": enabled,
		"config":  body.Config,
		"events":  events,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	raw, err := s.playbackMonitorDo(ctx, http.MethodPost, "/notification/destinations", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watch_notify.destination_failed"})
		return
	}
	rec := raw
	if nested, ok := raw["destination"].(map[string]any); ok {
		rec = nested
	}
	pub := publicWatchNotifyDestination(rec)
	if pub == nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid watch notify response", "code": "watch_notify.invalid_response"})
		return
	}
	writeJSON(w, pub)
}

func (s *server) handleDeleteWatchNotifyDestination(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_notify.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "watch_notify.id_required"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "watch_notify.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.playbackMonitorDo(ctx, http.MethodDelete, "/notification/destinations/"+id, nil); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watch_notify.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *server) handleTestWatchNotifyDestination(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_notify.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "watch_notify.id_required"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "watch_notify.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	raw, err := s.playbackMonitorDo(ctx, http.MethodPost, "/notification/destinations/"+id+"/test", nil)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watch_notify.test_failed"})
		return
	}
	ok := boolVal(raw, "success", "ok")
	if !ok && len(raw) == 0 {
		ok = true
	}
	writeJSON(w, map[string]any{"ok": ok, "id": id})
}
