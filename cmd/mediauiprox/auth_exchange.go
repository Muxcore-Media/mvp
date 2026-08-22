package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type authExchangeResult struct {
	UserID   string
	Username string
	TenantID string
}

func (s *server) exchangeAuthCode(code string) (*authExchangeResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("code required")
	}
	if s.authInternal == "" {
		return nil, fmt.Errorf("auth not configured")
	}

	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(s.authInternal+"/login/exchange", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("auth unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("code exchange failed")
	}

	var result struct {
		Token    string         `json:"token"`
		UserID   string         `json:"user_id"`
		Username string         `json:"username"`
		TenantID string         `json:"tenant_id"`
		Claims   map[string]any `json:"claims"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid auth response")
	}

	tenantID := strings.TrimSpace(result.TenantID)
	if tenantID == "" && result.Claims != nil {
		if v, ok := result.Claims["tenant_id"].(string); ok {
			tenantID = strings.TrimSpace(v)
		}
	}
	return &authExchangeResult{
		UserID:   result.UserID,
		Username: result.Username,
		TenantID: tenantID,
	}, nil
}

func (s *server) createSessionFromAuthCode(code string) (sessionToken string, result *authExchangeResult, err error) {
	result, err = s.exchangeAuthCode(code)
	if err != nil {
		return "", nil, err
	}
	sessionToken, err = s.sessions.CreateWithTenant(result.UserID, result.Username, result.TenantID)
	if err != nil {
		return "", nil, fmt.Errorf("session error")
	}
	return sessionToken, result, nil
}
