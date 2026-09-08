package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func publicAPIKey(raw map[string]any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	scopes := raw["scopes"]
	if scopes == nil {
		scopes = []any{}
	}
	return map[string]any{
		"id":         firstNonEmpty(asString(raw["id"])),
		"name":       firstNonEmpty(asString(raw["name"])),
		"prefix":     firstNonEmpty(asString(raw["prefix"])),
		"user_id":    firstNonEmpty(asString(raw["user_id"]), asString(raw["userId"])),
		"username":   firstNonEmpty(asString(raw["username"])),
		"scopes":     scopes,
		"created_at": firstNonEmpty(asString(raw["created_at"]), asString(raw["createdAt"])),
		"last_used":  firstNonEmpty(asString(raw["last_used"]), asString(raw["lastUsed"])),
	}
}

func writeAPIKeySecret(w http.ResponseWriter, raw []byte) {
	var payload struct {
		Token  map[string]any `json:"token"`
		Secret string         `json:"secret"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid token JSON", "code": "keys.invalid_response"})
		return
	}
	writeJSON(w, map[string]any{
		"token":  publicAPIKey(payload.Token),
		"secret": strings.TrimSpace(payload.Secret),
	})
}

func (s *server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "keys.forbidden"})
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodGet, s.authInternal+"/api/tokens", nil)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "keys": []any{}, "error": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		writeJSON(w, map[string]any{"available": false, "keys": []any{}, "error": strings.TrimSpace(string(raw))})
		return
	}
	var payload struct {
		Tokens []map[string]any `json:"tokens"`
		Keys   []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, map[string]any{"available": false, "keys": []any{}, "error": "invalid tokens JSON"})
		return
	}
	rows := payload.Tokens
	if len(rows) == 0 {
		rows = payload.Keys
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		pub := publicAPIKey(row)
		if pub["id"] == "" {
			continue
		}
		out = append(out, pub)
	}
	writeJSON(w, map[string]any{"available": true, "keys": out})
}

func (s *server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "keys.forbidden"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/tokens", body)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "keys.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "keys.create_failed"})
		return
	}
	writeAPIKeySecret(w, raw)
}

func (s *server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "keys.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "keys.id_required"})
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodDelete, s.authInternal+"/api/tokens/"+url.PathEscape(id), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "keys.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "keys.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}

func (s *server) handleRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "keys.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "keys.id_required"})
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/tokens/"+url.PathEscape(id)+"/rotate", nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "keys.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "keys.rotate_failed"})
		return
	}
	writeAPIKeySecret(w, raw)
}
