package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *server) handleInvitePeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeAPIError(w, http.StatusBadRequest, "token required", "invite.token_required")
		return
	}
	s.proxyAuthJSON(w, r, s.authInternal+"/api/invite/peek?token="+url.QueryEscape(token))
}

func (s *server) handleInviteRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	s.proxyAuthJSON(w, r, s.authInternal+"/api/invite/redeem")
}

func (s *server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "invite.forbidden"})
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodGet, s.authInternal+"/api/invites", nil)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "invites": []any{}, "error": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		writeJSON(w, map[string]any{"available": false, "invites": []any{}, "error": strings.TrimSpace(string(raw))})
		return
	}
	var payload struct {
		Invites []map[string]any `json:"invites"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, map[string]any{"available": false, "invites": []any{}, "error": "invalid invites JSON"})
		return
	}
	out := make([]map[string]any, 0, len(payload.Invites))
	for _, inv := range payload.Invites {
		out = append(out, publicInvite(inv, ""))
	}
	writeJSON(w, map[string]any{"available": true, "invites": out})
}

func (s *server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "invite.forbidden"})
		return
	}
	var body struct {
		Role     string `json:"role"`
		MaxUses  *int   `json:"max_uses"`
		TTLHours int    `json:"ttl_hours"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "invite.invalid"})
			return
		}
	}
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = "user"
	}
	ttl := body.TTLHours
	if ttl <= 0 {
		ttl = 168
	}
	maxUses := 1
	if body.MaxUses != nil {
		maxUses = *body.MaxUses
	}
	_, username, tenantID, _, _ := s.sessionPrincipal(r)
	payload, _ := json.Marshal(map[string]any{
		"role":      role,
		"maxUses":   maxUses,
		"ttlHours":  ttl,
		"tenantId":  tenantID,
		"createdBy": username,
	})
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/invites", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "invite.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "invite.create_failed"})
		return
	}
	var inv map[string]any
	if err := json.Unmarshal(raw, &inv); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid invite JSON", "code": "invite.invalid_response"})
		return
	}
	token, _ := inv["token"].(string)
	writeJSON(w, map[string]any{
		"available": true,
		"invite":    publicInvite(inv, s.publicInviteURL(r, token)),
	})
}

func (s *server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "invite.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "invite.id_required"})
		return
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodDelete, s.authInternal+"/api/invites/"+url.PathEscape(id), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "invite.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "invite.revoke_failed"})
		return
	}
	writeJSON(w, map[string]any{"revoked": true, "id": id})
}

func (s *server) proxyAuthInvites(r *http.Request, method, target string, body []byte) ([]byte, int, error) {
	if strings.TrimSpace(s.authInternal) == "" {
		return nil, 0, errPlaybackMonitorStatus(http.StatusServiceUnavailable, "auth not configured")
	}
	tok := ""
	if s.sessions != nil {
		tok = s.sessions.LookupAuthToken(sessionTokenFromRequest(r))
	}
	if tok == "" {
		return nil, 0, errPlaybackMonitorStatus(http.StatusUnauthorized, "auth session is not linked")
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, target, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	client := authUpstreamClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, nil
}

func (s *server) publicInviteURL(r *http.Request, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	base := strings.TrimRight(s.publicURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		host := strings.TrimSpace(r.Host)
		if host != "" {
			base = scheme + "://" + host
		}
	}
	if base == "" {
		return "/invite/" + url.PathEscape(token)
	}
	return base + "/invite/" + url.PathEscape(token)
}

func publicInvite(inv map[string]any, joinURL string) map[string]any {
	revoked := firstString(inv, "revoked_at", "revokedAt")
	out := map[string]any{
		"id":         firstString(inv, "id"),
		"prefix":     firstString(inv, "prefix"),
		"created_by": firstString(inv, "created_by", "createdBy"),
		"role":       firstString(inv, "role"),
		"max_uses":   int(floatVal(inv, "max_uses", "maxUses")),
		"use_count":  int(floatVal(inv, "use_count", "useCount")),
		"expires_at": firstString(inv, "expires_at", "expiresAt"),
		"created_at": firstString(inv, "created_at", "createdAt"),
		"revoked":    revoked != "" && revoked != "0001-01-01T00:00:00Z",
	}
	if joinURL != "" {
		out["join_url"] = joinURL
	}
	return out
}

func (s *server) proxyAuthJSON(w http.ResponseWriter, r *http.Request, target string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "proxy build failed", "invite.proxy_failed")
		return
	}
	req.Header.Set("Accept", "application/json")
	if r.Body != nil && r.Method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "auth unavailable", "invite.auth_unavailable")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
