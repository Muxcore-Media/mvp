package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// handleTVLogin exchanges username/password via auth-local /login/device and issues a media-ui session cookie token.
func (s *server) handleTVLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authInternal == "" {
		http.Error(w, `{"error":"auth not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	username := strings.TrimSpace(creds.Username)
	password := creds.Password
	if username == "" || password == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "username and password required"})
		return
	}

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(s.authInternal+"/login/device", "application/json", strings.NewReader(string(body)))
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "auth unavailable"})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeJSONStatus(w, resp.StatusCode, map[string]string{"error": strings.TrimSpace(string(raw))})
		return
	}

	var authResult struct {
		Token        string         `json:"token"`
		UserID       string         `json:"user_id"`
		Username     string         `json:"username"`
		TenantID     string         `json:"tenant_id"`
		Requires2FA  bool           `json:"requires_2fa"`
		PartialToken string         `json:"partial_token"`
		Claims       map[string]any `json:"claims"`
	}
	if err := json.Unmarshal(raw, &authResult); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "invalid auth response"})
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
	sess, err := s.sessions.CreateWithTenant(authResult.UserID, authResult.Username, tenantID)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "session error"})
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authInternal == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "auth not configured"})
		return
	}

	var creds struct {
		PartialToken string `json:"partial_token"`
		TOTPCode     string `json:"totp_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	partialToken := strings.TrimSpace(creds.PartialToken)
	totpCode := strings.TrimSpace(creds.TOTPCode)
	if partialToken == "" || totpCode == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "partial_token and totp_code required"})
		return
	}

	body, _ := json.Marshal(map[string]string{"partial_token": partialToken, "totp_code": totpCode})
	resp, err := http.Post(s.authInternal+"/login/device/totp", "application/json", strings.NewReader(string(body)))
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "auth unavailable"})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "invalid TOTP code"})
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeJSONStatus(w, resp.StatusCode, map[string]string{"error": strings.TrimSpace(string(raw))})
		return
	}

	var authResult struct {
		UserID   string         `json:"user_id"`
		Username string         `json:"username"`
		TenantID string         `json:"tenant_id"`
		Claims   map[string]any `json:"claims"`
	}
	if err := json.Unmarshal(raw, &authResult); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "invalid auth response"})
		return
	}
	tenantID := strings.TrimSpace(authResult.TenantID)
	if tenantID == "" && authResult.Claims != nil {
		if v, ok := authResult.Claims["tenant_id"].(string); ok {
			tenantID = strings.TrimSpace(v)
		}
	}
	sess, err := s.sessions.CreateWithTenant(authResult.UserID, authResult.Username, tenantID)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "session error"})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"session_token": sess,
		"user_id":       authResult.UserID,
		"username":      authResult.Username,
	})
}
