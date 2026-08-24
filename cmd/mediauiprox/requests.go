package main

import (
	"io"
	"net/http"
	"strings"
)

// registerRequestMediaRoutes proxies search/request APIs to request-media.
// Response bodies are streamed unchanged so optional upstream fields (e.g.
// status_detail, status_label on /api/requests) pass through without breaking
// older clients when those fields are absent.
func (s *server) registerRequestMediaRoutes(mux *http.ServeMux) {
	h := http.HandlerFunc(s.proxyRequestMedia)
	mux.Handle("/api/search", h)
	mux.Handle("/api/request", h)
	mux.Handle("/api/requests/", h)
	mux.Handle("/api/requests", h)
}

func (s *server) proxyRequestMedia(w http.ResponseWriter, r *http.Request) {
	if s.requestHTTP == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "request-media unavailable", "request.unavailable")
		return
	}
	upstream := *s.requestHTTP
	upstream.Path = r.URL.Path
	upstream.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstream.String(), r.Body)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "proxy build failed", "request.proxy_failed")
		return
	}
	for k, vals := range r.Header {
		kl := strings.ToLower(k)
		if strings.EqualFold(k, "Host") || kl == "x-muxcore-roles" || kl == "x-muxcore-user" {
			continue
		}
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	if username, roles, ok := s.sessionIdentity(r); ok {
		req.Header.Set("X-MuxCore-User", username)
		if len(roles) > 0 {
			req.Header.Set("X-MuxCore-Roles", strings.Join(roles, ","))
		}
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "request-media unavailable", "request.unavailable")
		return
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}
