package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func hardenedTestServer(t *testing.T, publicURL string) (*server, http.Handler, string) {
	t.Helper()
	s := &server{
		sessions:       newSessionStore(time.Hour),
		publicURL:      publicURL,
		allowedOrigins: parseAllowedOrigins(publicURL, ""),
		authHTTP:       "https://auth.example",
		csp:            buildContentSecurityPolicy("https://auth.example"),
		requireAuth:    true,
		dist:           t.TempDir(),
	}
	tok, err := s.sessions.Create("u1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /logout", s.handleLogoutConfirm)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("/api/thing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/tv/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return s, s.hardenHandler(s.withAuth(mux)), tok
}

func TestCSRFStateChangingRequests(t *testing.T) {
	_, h, tok := hardenedTestServer(t, "https://mux.example")
	cookie := &http.Cookie{Name: "session", Value: tok}
	cases := []struct {
		name    string
		method  string
		path    string
		cookie  bool
		bearer  bool
		origin  string
		referer string
		want    int
	}{
		{"same-origin cookie POST", http.MethodPost, "/api/thing", true, false, "https://mux.example", "", http.StatusNoContent},
		{"default port normalized", http.MethodPut, "/api/thing", true, false, "https://MUX.example:443", "", http.StatusNoContent},
		{"cross-site cookie POST", http.MethodPost, "/api/thing", true, false, "https://evil.example", "", http.StatusForbidden},
		{"null origin cookie DELETE", http.MethodDelete, "/api/thing", true, false, "null", "", http.StatusForbidden},
		{"scheme mismatch", http.MethodPatch, "/api/thing", true, false, "http://mux.example", "", http.StatusForbidden},
		{"referer fallback ok", http.MethodPost, "/api/thing", true, false, "", "https://mux.example/settings", http.StatusNoContent},
		{"referer fallback cross-site", http.MethodPost, "/api/thing", true, false, "", "https://evil.example/x", http.StatusForbidden},
		{"cookie without origin or referer", http.MethodPost, "/api/thing", true, false, "", "", http.StatusForbidden},
		{"bearer API client, no cookie", http.MethodPost, "/api/thing", false, true, "", "", http.StatusNoContent},
		{"bearer API client with foreign origin", http.MethodPost, "/api/thing", false, true, "https://evil.example", "", http.StatusNoContent},
		{"bearer plus cookie cross-site", http.MethodPost, "/api/thing", true, true, "https://evil.example", "", http.StatusForbidden},
		{"unauthenticated device login", http.MethodPost, "/api/tv/login", false, false, "", "", http.StatusNoContent},
		{"unauthenticated cross-site login", http.MethodPost, "/api/tv/login", false, false, "https://evil.example", "", http.StatusForbidden},
		{"safe GET cross-site", http.MethodGet, "/api/thing", true, false, "https://evil.example", "", http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.cookie {
				req.AddCookie(cookie)
			}
			if tc.bearer {
				req.Header.Set("Authorization", "Bearer "+tok)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d want %d body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden && !strings.Contains(w.Body.String(), "csrf.rejected") {
				t.Fatalf("expected JSON csrf.rejected, got %s", w.Body.String())
			}
		})
	}
}

func TestCSRFWithoutConfiguredPublicURLUsesRequestOrigin(t *testing.T) {
	_, h, tok := hardenedTestServer(t, "")
	for origin, want := range map[string]int{
		"http://media.lan:5173": http.StatusNoContent,
		"http://evil.lan:5173":  http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodPost, "http://media.lan:5173/api/thing", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("origin %s: %d want %d", origin, w.Code, want)
		}
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	got := parseAllowedOrigins("https://Mux.Example/", "http://192.168.1.5:5173, https://mux.example:443,,bogus")
	want := []string{"https://mux.example", "http://192.168.1.5:5173"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
	if o := normalizeOrigin("http://[::1]:80/x"); o != "http://[::1]" {
		t.Fatalf("ipv6 origin %q", o)
	}
}

func TestLogoutRequiresPOST(t *testing.T) {
	s, h, tok := hardenedTestServer(t, "https://mux.example")

	// GET shows a confirm page and leaves the session alone.
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `method="post" action="/logout"`) {
		t.Fatalf("GET /logout: %d %s", w.Code, w.Body.String())
	}
	if !s.sessions.Valid(tok) {
		t.Fatal("GET /logout must not end the session")
	}

	// Cross-site POST is rejected.
	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || !s.sessions.Valid(tok) {
		t.Fatalf("cross-site POST /logout: %d valid=%v", w.Code, s.sessions.Valid(tok))
	}

	// Same-origin POST ends the session and clears the cookie.
	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	req.Header.Set("Origin", "https://mux.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("POST /logout: %d %s", w.Code, w.Header().Get("Location"))
	}
	if s.sessions.Valid(tok) {
		t.Fatal("session still valid after POST /logout")
	}
	sc := w.Header().Get("Set-Cookie")
	if !strings.Contains(sc, "session=") || !strings.Contains(sc, "Max-Age=0") || !strings.Contains(sc, "SameSite=Lax") || !strings.Contains(sc, "Secure") {
		t.Fatalf("Set-Cookie %q", sc)
	}

	// Other methods are not routed to logout.
	req = httptest.NewRequest(http.MethodPut, "/logout", nil)
	req.Header.Set("Origin", "https://mux.example")
	w = httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /logout", s.handleLogoutConfirm)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /logout: %d", w.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	_, h, _ := hardenedTestServer(t, "https://mux.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logout", nil))
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'self'", "object-src 'self'", "form-action 'self' https://auth.example", "img-src 'self' data: blob: https:", "https://www.youtube.com"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP %q missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("CSP too loose: %q", csp)
	}
	if w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Referrer-Policy") == "" {
		t.Fatalf("headers %v", w.Header())
	}

	t.Setenv("MEDIA_UI_CSP", "default-src 'none'")
	h2 := withSecurityHeaders("default-src 'self'", http.NotFoundHandler())
	w = httptest.NewRecorder()
	h2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Header().Get("Content-Security-Policy") != "default-src 'none'" {
		t.Fatal("MEDIA_UI_CSP override ignored")
	}
}

func TestHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("timeouts not set: %+v", srv)
	}
}

// Streaming routes outlive the server WriteTimeout; other routes do not.
func TestStreamingRoutesExemptFromWriteTimeout(t *testing.T) {
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "payload")
	})
	ts := httptest.NewUnstartedServer(withStreamingDeadlines(slow))
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)

	for path, wantOK := range map[string]bool{
		"/stream/movies/1":     true,
		"/api/sessions/events": true,
		"/api/movies":          false,
	} {
		resp, err := ts.Client().Get(ts.URL + path)
		ok := false
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			ok = rerr == nil && string(body) == "payload"
		}
		if ok != wantOK {
			t.Fatalf("%s: ok=%v want %v (err=%v)", path, ok, wantOK, err)
		}
	}
}
