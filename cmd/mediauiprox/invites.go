package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
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
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
