package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// handleTVLogin exchanges username/password via auth-local /login/device and issues a media-ui session cookie token.
func (s *server) handleTVLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.authInternal == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "auth not configured", "auth.not_configured")
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "auth.invalid_json")
		return
	}
	username := strings.TrimSpace(creds.Username)
	password := creds.Password
	if username == "" || password == "" {
		writeAPIError(w, http.StatusBadRequest, "username and password required", "auth.credentials_required")
		return
	}

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.authInternal+"/login/device", bytes.NewReader(body))
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "auth unavailable", "auth.unavailable")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := authUpstreamClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "auth unavailable", "auth.unavailable")
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		writeAPIError(w, http.StatusUnauthorized, "invalid username or password", "auth.invalid_credentials")
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeAPIError(w, resp.StatusCode, strings.TrimSpace(string(raw)), "auth.login_failed")
		return
	}

	var authResult struct {
		Token        string         `json:"token"`
		UserID       string         `json:"user_id"`
		Username     string         `json:"username"`
		TenantID     string         `json:"tenant_id"`
		Roles        []string       `json:"roles"`
		Requires2FA  bool           `json:"requires_2fa"`
		PartialToken string         `json:"partial_token"`
		Claims       map[string]any `json:"claims"`
	}
	if err := json.Unmarshal(raw, &authResult); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "invalid auth response", "auth.invalid_response")
		return
	}
	if authResult.Requires2FA {
		writeJSONStatus(w, http.StatusOK, map[string]any{
			"requires_2fa":  true,
			"partial_token": authResult.PartialToken,
			"user_id":       authResult.UserID,
			"username":      authResult.Username,
		})
		return
	}

	tenantID := strings.TrimSpace(authResult.TenantID)
	if tenantID == "" && authResult.Claims != nil {
		if v, ok := authResult.Claims["tenant_id"].(string); ok {
			tenantID = strings.TrimSpace(v)
		}
	}
	roles := append([]string(nil), authResult.Roles...)
	if len(roles) == 0 {
		roles = rolesFromClaims(authResult.Claims)
	}
	sess, err := s.sessions.CreateWithRoles(authResult.UserID, authResult.Username, tenantID, roles)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session error", "auth.session_error")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"session_token": sess,
		"user_id":       authResult.UserID,
		"username":      authResult.Username,
	})
}

// handleTVLoginTOTP completes native login after password step when TOTP is required.
func (s *server) handleTVLoginTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.authInternal == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "auth not configured", "auth.not_configured")
		return
	}

	var creds struct {
		PartialToken string `json:"partial_token"`
		TOTPCode     string `json:"totp_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "auth.invalid_json")
		return
	}
	partialToken := strings.TrimSpace(creds.PartialToken)
	totpCode := strings.TrimSpace(creds.TOTPCode)
	if partialToken == "" || totpCode == "" {
		writeAPIError(w, http.StatusBadRequest, "partial_token and totp_code required", "auth.totp_required")
		return
	}

	body, _ := json.Marshal(map[string]string{"partial_token": partialToken, "totp_code": totpCode})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.authInternal+"/login/device/totp", bytes.NewReader(body))
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "auth unavailable", "auth.unavailable")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := authUpstreamClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "auth unavailable", "auth.unavailable")
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		writeAPIError(w, http.StatusUnauthorized, "invalid TOTP code", "auth.invalid_totp")
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeAPIError(w, resp.StatusCode, strings.TrimSpace(string(raw)), "auth.totp_failed")
		return
	}

	var authResult struct {
		UserID   string         `json:"user_id"`
		Username string         `json:"username"`
		TenantID string         `json:"tenant_id"`
		Claims   map[string]any `json:"claims"`
	}
	if err := json.Unmarshal(raw, &authResult); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "invalid auth response", "auth.invalid_response")
		return
	}
	tenantID := strings.TrimSpace(authResult.TenantID)
	if tenantID == "" && authResult.Claims != nil {
		if v, ok := authResult.Claims["tenant_id"].(string); ok {
			tenantID = strings.TrimSpace(v)
		}
	}
	roles := rolesFromClaims(authResult.Claims)
	sess, err := s.sessions.CreateWithRoles(authResult.UserID, authResult.Username, tenantID, roles)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session error", "auth.session_error")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"session_token": sess,
		"user_id":       authResult.UserID,
		"username":      authResult.Username,
	})
}
