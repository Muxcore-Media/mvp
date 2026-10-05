package main

import (
	"io"
	"net/http"
)

// registerRequestMediaRoutes proxies search/request APIs to request-media.
// Response bodies are streamed unchanged so optional upstream fields (e.g.
// status_detail, status_label on /api/requests) pass through without breaking
// older clients when those fields are absent.
func (s *server) registerRequestMediaRoutes(mux routeRegistrar) {
	h := http.HandlerFunc(s.proxyRequestMedia)
	mux.Handle("/api/search", h)
	mux.Handle("/api/request", h)
	mux.Handle("/api/requests/", h)
	mux.Handle("/api/requests", h)
	mux.Handle("/api/request-policy", h)
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
	// ADR-0019: never pass client identity/credential headers through; the
	// module gets the signed-in user's auth-local bearer (+ X-Caller-Id for one
	// release).
	copyClientHeaders(req.Header, r.Header)
	if id, ok := s.sessionUpstreamIdentity(r); ok {
		id.apply(req.Header)
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "request-media unavailable", "request.unavailable")
		return
	}
	defer func() { _ = resp.Body.Close() }()
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
