package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func publicHouseholdUser(raw map[string]any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	roles := raw["roles"]
	if roles == nil {
		roles = []any{}
	}
	return map[string]any{
		"id":           firstNonEmpty(asString(raw["id"])),
		"username":     firstNonEmpty(asString(raw["username"])),
		"roles":        roles,
		"totp_enabled": raw["totp_enabled"] == true || raw["totpEnabled"] == true,
		"created_at":   firstNonEmpty(asString(raw["created_at"]), asString(raw["createdAt"])),
		"tenant_id":    firstNonEmpty(asString(raw["tenant_id"]), asString(raw["tenantId"])),
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "users.forbidden"})
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodGet, s.authInternal+"/api/users", nil)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "users": []any{}, "error": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		writeJSON(w, map[string]any{"available": false, "users": []any{}, "error": strings.TrimSpace(string(raw))})
		return
	}
	var payload struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, map[string]any{"available": false, "users": []any{}, "error": "invalid users JSON"})
		return
	}
	out := make([]map[string]any, 0, len(payload.Users))
	for _, u := range payload.Users {
		out = append(out, publicHouseholdUser(u))
	}
	writeJSON(w, map[string]any{"available": true, "users": out})
}

func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "users.forbidden"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "users.invalid"})
			return
		}
	}
	username := strings.TrimSpace(body.Username)
	password := strings.TrimSpace(body.Password)
	if username == "" || password == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "username and password required", "code": "users.required"})
		return
	}
	if len(password) < 8 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "password must be at least 8 characters", "code": "users.password"})
		return
	}
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = "user"
	}
	_, _, tenantID, _, _ := s.sessionPrincipal(r)
	payload, _ := json.Marshal(map[string]any{
		"username": username,
		"password": password,
		"role":     role,
		"tenantId": tenantID,
	})
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/users", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "users.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "users.create_failed"})
		return
	}
	var payloadOut struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(raw, &payloadOut); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid user JSON", "code": "users.invalid_response"})
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{"available": true, "user": publicHouseholdUser(payloadOut.User)})
}

func (s *server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "users.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "users.id_required"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "users.invalid"})
			return
		}
	}
	password := strings.TrimSpace(body.Password)
	if len(password) < 8 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "password must be at least 8 characters", "code": "users.password"})
		return
	}
	payload, _ := json.Marshal(map[string]any{"password": password})
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/users/"+url.PathEscape(id)+"/password", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "users.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "users.password_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *server) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "users.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "users.id_required"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	raw, status, err := s.proxyAuthInvites(r, http.MethodPatch, s.authInternal+"/api/users/"+url.PathEscape(id), body)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "users.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "users.patch_failed"})
		return
	}
	var payload struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid user JSON", "code": "users.invalid_response"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "user": publicHouseholdUser(payload.User)})
}

func (s *server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "users.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "users.id_required"})
		return
	}
	userID, _, _, _, _ := s.sessionPrincipal(r)
	if userID != "" && userID == id {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "cannot delete your own account", "code": "users.self_delete"})
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodDelete, s.authInternal+"/api/users/"+url.PathEscape(id), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "users.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "users.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}
