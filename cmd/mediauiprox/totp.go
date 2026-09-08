package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func publicHouseholdTOTP(raw map[string]any) map[string]any {
	out := map[string]any{
		"available": true,
		"enabled":   raw["enabled"] == true,
		"verified":  raw["verified"] == true,
	}
	if secret := firstNonEmpty(asString(raw["secret"])); secret != "" {
		out["secret"] = secret
	}
	if qr := firstNonEmpty(asString(raw["qr_code_url"]), asString(raw["qrCodeUrl"])); qr != "" {
		out["qr_code_url"] = qr
	}
	return out
}

func (s *server) requireLinkedAuthSession(w http.ResponseWriter, r *http.Request) bool {
	if _, _, _, _, ok := s.sessionPrincipal(r); !ok {
		writeAPIUnauthorized(w)
		return false
	}
	if s.sessions == nil || s.sessions.LookupAuthToken(sessionTokenFromRequest(r)) == "" {
		writeAPIUnauthorized(w)
		return false
	}
	return true
}

func (s *server) handleGetTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodGet, s.authInternal+"/api/totp", nil)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "enabled": false, "error": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		writeJSON(w, map[string]any{"available": false, "enabled": false, "error": strings.TrimSpace(string(raw))})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, map[string]any{"available": false, "enabled": false, "error": "invalid totp JSON"})
		return
	}
	writeJSON(w, publicHouseholdTOTP(payload))
}

func (s *server) handleEnableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/totp", nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "totp.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "totp.enable_failed"})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid totp JSON", "code": "totp.invalid"})
		return
	}
	writeJSON(w, publicHouseholdTOTP(payload))
}

func (s *server) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodDelete, s.authInternal+"/api/totp", nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "totp.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "totp.disable_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "enabled": false})
}

func (s *server) handleVerifyTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	var body struct {
		Code     string `json:"code"`
		TOTPCode string `json:"totp_code"`
	}
	if r.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "totp.invalid"})
				return
			}
		}
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		code = strings.TrimSpace(body.TOTPCode)
	}
	if code == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "code is required", "code": "totp.code_required"})
		return
	}
	payload, _ := json.Marshal(map[string]string{"code": code})
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/totp/verify", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "totp.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "totp.verify_failed"})
		return
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid totp JSON", "code": "totp.invalid"})
		return
	}
	writeJSON(w, publicHouseholdTOTP(parsed))
}
