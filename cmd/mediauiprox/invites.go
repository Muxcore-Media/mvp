package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (s *server) handleInvitePeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, `{"error":"token required"}`, http.StatusBadRequest)
		return
	}
	s.proxyAuthJSON(w, r, s.authInternal+"/api/invite/peek?token="+url.QueryEscape(token))
}

func (s *server) handleInviteRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.proxyAuthJSON(w, r, s.authInternal+"/api/invite/redeem")
}

func (s *server) proxyAuthJSON(w http.ResponseWriter, r *http.Request, target string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, `{"error":"proxy build failed"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "application/json")
	if r.Body != nil && r.Method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"auth unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
