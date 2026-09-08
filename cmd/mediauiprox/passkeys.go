package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func publicHouseholdPasskey(raw map[string]any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":              firstNonEmpty(asString(raw["id"])),
		"credential_type": firstNonEmpty(asString(raw["credential_type"]), asString(raw["credentialType"])),
		"transports":      firstNonEmpty(asString(raw["transports"])),
		"created_at":      firstNonEmpty(asString(raw["created_at"]), asString(raw["createdAt"])),
		"last_used_at":    firstNonEmpty(asString(raw["last_used_at"]), asString(raw["lastUsedAt"])),
	}
}

func passkeyChallenge(options map[string]any) string {
	if options == nil {
		return ""
	}
	if pk, ok := options["publicKey"].(map[string]any); ok {
		if challenge := firstNonEmpty(asString(pk["challenge"])); challenge != "" {
			return challenge
		}
	}
	return firstNonEmpty(asString(options["challenge"]))
}

func (s *server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodGet, s.authInternal+"/api/webauthn/credentials", nil)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "passkeys": []any{}, "error": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		writeJSON(w, map[string]any{"available": false, "passkeys": []any{}, "error": strings.TrimSpace(string(raw))})
		return
	}
	var payload struct {
		Credentials []map[string]any `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, map[string]any{"available": false, "passkeys": []any{}, "error": "invalid passkey JSON"})
		return
	}
	out := make([]map[string]any, 0, len(payload.Credentials))
	for _, row := range payload.Credentials {
		item := publicHouseholdPasskey(row)
		if asString(item["id"]) != "" {
			out = append(out, item)
		}
	}
	writeJSON(w, map[string]any{"available": true, "passkeys": out})
}

func (s *server) handleBeginPasskeyRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/webauthn/register/begin", nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "passkey.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "passkey.begin_failed"})
		return
	}
	var options map[string]any
	if err := json.Unmarshal(raw, &options); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid passkey JSON", "code": "passkey.invalid"})
		return
	}
	writeJSON(w, map[string]any{
		"available": true,
		"options":   options,
		"challenge": passkeyChallenge(options),
	})
}

func (s *server) handleCompletePasskeyRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	challenge := strings.TrimSpace(r.URL.Query().Get("challenge"))
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	}
	if challenge == "" && len(body) > 0 {
		var wrapped struct {
			Challenge  string          `json:"challenge"`
			Credential json.RawMessage `json:"credential"`
		}
		if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Challenge != "" {
			challenge = strings.TrimSpace(wrapped.Challenge)
			if len(wrapped.Credential) > 0 {
				body = wrapped.Credential
			}
		}
	}
	if challenge == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "challenge is required", "code": "passkey.challenge_required"})
		return
	}
	target := s.authInternal + "/api/webauthn/register/complete?challenge=" + url.QueryEscape(challenge)
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, target, body)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "passkey.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "passkey.complete_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "registered": true})
}

func (s *server) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireLinkedAuthSession(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "passkey id required", "code": "passkey.id_required"})
		return
	}
	target := s.authInternal + "/api/webauthn/credentials/" + url.PathEscape(id)
	raw, status, err := s.proxyAuthInvites(r, http.MethodDelete, target, nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "passkey.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "passkey.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}
