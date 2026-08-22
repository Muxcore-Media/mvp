package main

import (
	"io"
	"net/http"
	"net/url"
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

func (s *server) handleDebridVFS(w http.ResponseWriter, r *http.Request) {
	s.proxyDebridGET(w, r, "/api/vfs")
}

func (s *server) handleDebridStream(w http.ResponseWriter, r *http.Request) {
	s.proxyDebridGET(w, r, "/api/vfs/stream")
}

func (s *server) proxyDebridGET(w http.ResponseWriter, r *http.Request, path string) {
	if s.debridHTTP == nil || strings.TrimSpace(s.debridHTTP.String()) == "" {
		http.Error(w, `{"error":"debrid unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	target := strings.TrimRight(s.debridHTTP.String(), "/") + path
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	if err != nil {
		http.Error(w, `{"error":"proxy build failed"}`, http.StatusInternalServerError)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	req.Header.Set("Accept", r.Header.Get("Accept"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"debrid unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && strings.Contains(ct, "json") {
		w.Header().Set("Content-Type", ct)
	} else if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, resp.Body)
}

func debridStreamURL(id string) string {
	return "/api/debrid/stream?id=" + url.QueryEscape(id)
}
