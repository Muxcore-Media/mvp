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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}
	redirectURI := safeMobileRedirectURI(r.URL.Query().Get("redirect_uri"))
	target, err := appendCodeToRedirect(redirectURI, code)
	if err != nil {
		http.Error(w, "invalid redirect", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleMobileSession exchanges an auth one-time code for a bearer session token (JSON API for native clients).
func (s *server) handleMobileSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	sess, result, err := s.createSessionFromAuthCode(body.Code)
	if err != nil {
		switch err.Error() {
		case "code required":
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case "auth not configured":
			writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		case "code exchange failed":
			writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired code"})
		default:
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
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
