package main

import (
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Browser-facing hardening for the BFF (T-M3-04; NFR-SEC-004, NFR-SEC-006).

// HTTP server timeouts. Streaming routes (see isStreamingPath) clear the
// per-connection deadlines so long video/SSE responses are not cut off.
const (
	bffReadHeaderTimeout = 10 * time.Second
	bffReadTimeout       = 2 * time.Minute
	bffWriteTimeout      = 2 * time.Minute
	bffIdleTimeout       = 2 * time.Minute
)

// newHTTPServer returns the BFF http.Server with timeouts set.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: bffReadHeaderTimeout,
		ReadTimeout:       bffReadTimeout,
		WriteTimeout:      bffWriteTimeout,
		IdleTimeout:       bffIdleTimeout,
	}
}

// isStreamingPath reports routes whose responses may legitimately run longer
// than bffWriteTimeout: media streams, HLS/transcode, debrid passthrough and
// the playback-session SSE feed.
func isStreamingPath(p string) bool {
	return strings.HasPrefix(p, "/stream/") ||
		p == "/api/sessions/events" ||
		p == "/api/debrid/stream"
}

// withStreamingDeadlines lifts the server read/write deadlines for streaming
// routes via http.ResponseController (zero time = no deadline).
func withStreamingDeadlines(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStreamingPath(r.URL.Path) {
			rc := http.NewResponseController(w)
			_ = rc.SetWriteDeadline(time.Time{})
			_ = rc.SetReadDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}

// buildContentSecurityPolicy returns the SPA CSP. The SPA bundle is
// same-origin; it embeds TMDB artwork and YouTube trailers, plays media over
// same-origin streams (debrid links may be remote https), uses hls.js (blob:
// workers / MediaSource), inline style attributes (React style={{}}), and
// same-origin iframes/objects for the book and comic readers. form-action
// includes the auth-local origin because POST /logout redirects to login.
func buildContentSecurityPolicy(authHTTP string) string {
	formAction := "'self'"
	if o := normalizeOrigin(authHTTP); o != "" {
		formAction += " " + o
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"media-src 'self' blob: https:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"worker-src 'self' blob:",
		"frame-src 'self' https://www.youtube.com https://www.youtube-nocookie.com",
		"object-src 'self'",
		"frame-ancestors 'self'",
		"base-uri 'self'",
		"form-action " + formAction,
	}, "; ")
}

// withSecurityHeaders sets CSP, framing, nosniff and referrer headers on every
// response. Framing is SAMEORIGIN (not DENY) because the SPA's book/comic
// readers iframe same-origin /stream/ URLs. MEDIA_UI_CSP overrides the CSP.
func withSecurityHeaders(csp string, next http.Handler) http.Handler {
	if v := strings.TrimSpace(os.Getenv("MEDIA_UI_CSP")); v != "" {
		csp = v
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if csp != "" {
			h.Set("Content-Security-Policy", csp)
		}
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		// Cross-origin requests get the origin only; YouTube embeds need it.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// normalizeOrigin returns scheme://host[:port] (lowercase, default port
// dropped) for an origin or absolute URL, or "" when it is not one.
func normalizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

// parseAllowedOrigins builds the configured browser origin allow-list from the
// public URL plus MEDIA_UI_ALLOWED_ORIGINS (comma-separated).
func parseAllowedOrigins(publicURL, extraCSV string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if o := normalizeOrigin(v); o != "" && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	add(publicURL)
	for _, v := range strings.Split(extraCSV, ",") {
		add(v)
	}
	return out
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// originAllowed reports whether origin (already normalized) is accepted. With
// a configured allow-list only those origins pass; otherwise the request's own
// public origin (Host, or X-Forwarded-Host from a trusted proxy) is used.
func (s *server) originAllowed(r *http.Request, origin string) bool {
	if origin == "" {
		return false
	}
	allowed := s.allowedOrigins
	if len(allowed) == 0 {
		allowed = []string{normalizeOrigin(s.publicOrigin(r))}
	}
	for _, a := range allowed {
		if a != "" && a == origin {
			return true
		}
	}
	return false
}

// csrfVerdict decides whether a request may proceed (NFR-SEC-004).
//
//   - Safe methods always pass.
//   - Otherwise Origin (fallback Referer) must match an allowed origin.
//   - Requests with neither header pass only when they carry no session
//     cookie: non-browser clients (TV/mobile apps, CLI) that authenticate
//     with a bearer token cannot be driven cross-site.
//   - A bearer-authenticated request without a session cookie passes even
//     with a foreign Origin: browsers cannot attach Authorization cross-site
//     without a CORS preflight, which the BFF never grants.
func (s *server) csrfVerdict(r *http.Request) (bool, string) {
	if isSafeMethod(r.Method) {
		return true, ""
	}
	_, cookieErr := r.Cookie("session")
	hasCookie := cookieErr == nil
	if !hasCookie && bearerSessionToken(r) != "" {
		return true, ""
	}
	src := strings.TrimSpace(r.Header.Get("Origin"))
	kind := "origin"
	if src == "" {
		src = strings.TrimSpace(r.Header.Get("Referer"))
		kind = "referer"
	}
	if src == "" {
		if hasCookie {
			return false, "missing Origin/Referer on cookie-authenticated request"
		}
		return true, ""
	}
	if s.originAllowed(r, normalizeOrigin(src)) {
		return true, ""
	}
	return false, kind + " not allowed"
}

// withCSRF rejects cross-site state-changing requests with 403.
func (s *server) withCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, reason := s.csrfVerdict(r); !ok {
			log.Printf("csrf: rejected %s %s from %s: %s (origin=%q)", r.Method, r.URL.Path, r.RemoteAddr, reason, r.Header.Get("Origin"))
			if strings.HasPrefix(r.URL.Path, "/api/") || wantsJSON(r) {
				writeAPIError(w, http.StatusForbidden, "cross-site request rejected", "csrf.rejected")
				return
			}
			http.Error(w, "forbidden: cross-site request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hardenHandler wraps the routed handler: security headers outermost, then
// CSRF, then streaming deadline relief.
func (s *server) hardenHandler(h http.Handler) http.Handler {
	return withSecurityHeaders(s.csp, s.withCSRF(withStreamingDeadlines(h)))
}

var logoutConfirmPage = template.Must(template.New("logout").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sign out</title></head>
<body><main><h1>Sign out of MuxCore Media?</h1>
<form method="post" action="/logout"><button type="submit">Sign out</button></form>
<p><a href="/">Cancel</a></p></main></body></html>
`))

// handleLogoutConfirm is GET /logout: a confirm page whose form POSTs to
// /logout. Logging out on GET would let any cross-site link end a session.
func (s *server) handleLogoutConfirm(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = logoutConfirmPage.Execute(w, nil)
}

// handleLogout is POST /logout: revoke the cookie (and bearer) session.
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.sessions != nil {
		if c, err := r.Cookie("session"); err == nil {
			s.sessions.Delete(c.Value)
		}
		if tok := bearerSessionToken(r); tok != "" {
			s.sessions.Delete(tok)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.publicOrigin(r), "https://"),
	})
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"logged_out": true})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
