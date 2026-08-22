package main

import (
	"io"
	"net/http"
	"strings"
)

func (s *server) handleDebridAdd(w http.ResponseWriter, r *http.Request) {
	if s.debridHTTP == nil || strings.TrimSpace(s.debridHTTP.String()) == "" {
		http.Error(w, `{"error":"debrid unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	target := strings.TrimRight(s.debridHTTP.String(), "/") + "/api/add"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, r.Body)
	if err != nil {
		http.Error(w, `{"error":"proxy build failed"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"debrid unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
