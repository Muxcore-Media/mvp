package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const defaultMobileRedirectURI = "muxcore://auth/callback"

// handleMobileAuthLogin starts browser-based auth (passkeys, OIDC, TOTP, etc.) for native clients.
func (s *server) handleMobileAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	redirectURI := safeMobileRedirectURI(r.URL.Query().Get("redirect_uri"))
	done := s.publicOrigin(r) + "/api/mobile/auth/done?redirect_uri=" + url.QueryEscape(redirectURI)
	target := s.authHTTP + "/login?redirect=" + url.QueryEscape(done)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleMobileAuthDone receives the auth module redirect and forwards the one-time code to the app deep link.
func (s *server) handleMobileAuthDone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeAPIError(w, http.StatusBadRequest, "code required", "mobile_auth.code_required")
		return
	}
	redirectURI := safeMobileRedirectURI(r.URL.Query().Get("redirect_uri"))
	target, err := appendCodeToRedirect(redirectURI, code)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid redirect", "mobile_auth.invalid_redirect")
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleMobileSession exchanges an auth one-time code for a bearer session token (JSON API for native clients).
func (s *server) handleMobileSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json", "code": "mobile_auth.invalid_json"})
		return
	}
	sess, result, err := s.createSessionFromAuthCode(body.Code)
	if err != nil {
		writeAuthExchangeError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"session_token": sess,
		"user_id":       result.UserID,
		"username":      result.Username,
	})
}

func safeMobileRedirectURI(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultMobileRedirectURI
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "muxcore" || parsed.Host != "auth" {
		return defaultMobileRedirectURI
	}
	return raw
}

func appendCodeToRedirect(redirectURI, code string) (string, error) {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	q.Set("code", code)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}
