package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const authUpstreamTimeout = 8 * time.Second

var authUpstreamClient = &http.Client{Timeout: authUpstreamTimeout}

type authExchangeError struct {
	message string
	code    string
	status  int
}

func (e *authExchangeError) Error() string {
	return e.message
}

func newAuthErr(status int, message, code string) error {
	return &authExchangeError{message: message, code: code, status: status}
}

func authExchangeHTTP(err error) (status int, message, code string) {
	var ae *authExchangeError
	if errors.As(err, &ae) {
		return ae.status, ae.message, ae.code
	}
	return http.StatusInternalServerError, "session error", "auth.session_error"
}

func writeAuthExchangeError(w http.ResponseWriter, err error) {
	status, message, code := authExchangeHTTP(err)
	writeJSONStatus(w, status, map[string]string{"error": message, "code": code})
}

type authExchangeResult struct {
	UserID   string
	Username string
	TenantID string
	Roles    []string
}

func (s *server) exchangeAuthCode(code string) (*authExchangeResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, newAuthErr(http.StatusBadRequest, "code required", "auth.code_required")
	}
	if s.authInternal == "" {
		return nil, newAuthErr(http.StatusServiceUnavailable, "auth not configured", "auth.not_configured")
	}

	body, _ := json.Marshal(map[string]string{"code": code})
	ctx, cancel := context.WithTimeout(context.Background(), authUpstreamTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.authInternal+"/login/exchange", bytes.NewReader(body))
	if err != nil {
		return nil, newAuthErr(http.StatusInternalServerError, "auth unavailable", "auth.unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := authUpstreamClient.Do(req)
	if err != nil {
		return nil, newAuthErr(http.StatusServiceUnavailable, "auth unavailable", "auth.unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, newAuthErr(http.StatusUnauthorized, "code exchange failed", "auth.exchange_failed")
	}

	var result struct {
		Token    string         `json:"token"`
		UserID   string         `json:"user_id"`
		Username string         `json:"username"`
		TenantID string         `json:"tenant_id"`
		Roles    []string       `json:"roles"`
		Claims   map[string]any `json:"claims"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, newAuthErr(http.StatusInternalServerError, "invalid auth response", "auth.invalid_response")
	}

	tenantID := strings.TrimSpace(result.TenantID)
	if tenantID == "" && result.Claims != nil {
		if v, ok := result.Claims["tenant_id"].(string); ok {
			tenantID = strings.TrimSpace(v)
		}
	}
	roles := append([]string(nil), result.Roles...)
	if len(roles) == 0 {
		roles = rolesFromClaims(result.Claims)
	}
	return &authExchangeResult{
		UserID:   result.UserID,
		Username: result.Username,
		TenantID: tenantID,
		Roles:    roles,
	}, nil
}

func (s *server) createSessionFromAuthCode(code string) (sessionToken string, result *authExchangeResult, err error) {
	result, err = s.exchangeAuthCode(code)
	if err != nil {
		return "", nil, err
	}
	sessionToken, err = s.sessions.CreateWithRoles(result.UserID, result.Username, result.TenantID, result.Roles)
	if err != nil {
		return "", nil, newAuthErr(http.StatusInternalServerError, "session error", "auth.session_error")
	}
	return sessionToken, result, nil
}
