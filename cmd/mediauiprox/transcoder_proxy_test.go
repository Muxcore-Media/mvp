package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The transcoder proxy is built on ReverseProxy.Rewrite (Director is
// deprecated). These tests pin the header, path, and forwarding behavior the
// previous NewSingleHostReverseProxy+Director wrapper provided.
func TestTranscoderProxyRewriteBehavior(t *testing.T) {
	var got *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	identity := []string{
		"Cookie", "Authorization", "Proxy-Authorization", "X-Caller-Id", "X-Tenant-ID",
		"X-Auth-Claims-Tenant", "X-User-ID", "X-Auth-Token", "X-MuxCore-Session", "X-Muxcore-Tenant",
	}
	send := func(t *testing.T, token string, mutate func(*http.Request)) *http.Request {
		t.Helper()
		got = nil
		target := mustURL(upstream.URL + "/base")
		s := &server{transcoderTransport: http.DefaultTransport, transcoderToken: token}
		req := httptest.NewRequest("GET", "http://browser.example/ignored?c=3", nil)
		req.RemoteAddr = "198.51.100.7:4242"
		for _, key := range identity {
			req.Header.Set(key, "browser-controlled")
		}
		req.Header.Set("Range", "bytes=0-99")
		req.Header.Set("Accept", "application/vnd.apple.mpegurl")
		// Callers set scheme, host, path, and query before proxying.
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		req.URL.Path = "/stream/hls"
		req.URL.RawQuery = "a=1&b=2"
		if mutate != nil {
			mutate(req)
		}
		w := httptest.NewRecorder()
		s.transcoderProxy(target).ServeHTTP(w, req)
		if w.Code != http.StatusNoContent || got == nil {
			t.Fatalf("proxy request failed: %d %s", w.Code, w.Body.String())
		}
		return got
	}

	t.Run("operator token replaces browser credentials", func(t *testing.T) {
		r := send(t, "op-token", nil)
		if v := r.Header.Get("Authorization"); v != "Bearer op-token" {
			t.Fatalf("Authorization = %q", v)
		}
		for _, key := range identity {
			if key == "Authorization" {
				continue
			}
			if v := r.Header.Get(key); v != "" {
				t.Errorf("client %s forwarded: %q", key, v)
			}
		}
	})

	t.Run("no token configured still strips browser credentials", func(t *testing.T) {
		r := send(t, "", nil)
		for _, key := range identity {
			if v := r.Header.Get(key); v != "" {
				t.Errorf("client %s forwarded: %q", key, v)
			}
		}
	})

	t.Run("non-identity headers pass through", func(t *testing.T) {
		r := send(t, "op-token", nil)
		if r.Header.Get("Range") != "bytes=0-99" || r.Header.Get("Accept") != "application/vnd.apple.mpegurl" {
			t.Fatalf("headers lost: %v", r.Header)
		}
	})

	t.Run("path joined under target base and query preserved", func(t *testing.T) {
		r := send(t, "op-token", nil)
		if r.URL.Path != "/base/stream/hls" || r.URL.RawQuery != "a=1&b=2" {
			t.Fatalf("got %s?%s", r.URL.Path, r.URL.RawQuery)
		}
	})

	t.Run("host is the transcoder not the browser", func(t *testing.T) {
		r := send(t, "op-token", nil)
		if r.Host != mustURL(upstream.URL).Host {
			t.Fatalf("Host = %q", r.Host)
		}
	})

	t.Run("connection header cannot strip the operator token", func(t *testing.T) {
		r := send(t, "op-token", func(r *http.Request) { r.Header.Set("Connection", "Authorization, X-Caller-Id") })
		if v := r.Header.Get("Authorization"); v != "Bearer op-token" {
			t.Fatalf("Authorization = %q", v)
		}
	})

	t.Run("x-forwarded-for chain is kept and peer appended", func(t *testing.T) {
		r := send(t, "op-token", func(r *http.Request) {
			r.Header.Add("X-Forwarded-For", "203.0.113.9")
			r.Header.Add("X-Forwarded-For", "203.0.113.10")
		})
		if v := r.Header.Get("X-Forwarded-For"); v != "203.0.113.9, 203.0.113.10, 198.51.100.7" {
			t.Fatalf("X-Forwarded-For = %q", v)
		}
		r = send(t, "op-token", nil)
		if v := r.Header.Get("X-Forwarded-For"); v != "198.51.100.7" {
			t.Fatalf("X-Forwarded-For = %q", v)
		}
	})

	t.Run("client forwarded host/proto/forwarded are not trusted", func(t *testing.T) {
		r := send(t, "op-token", func(r *http.Request) {
			r.Header.Set("X-Forwarded-Host", "evil.example")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("Forwarded", "for=evil")
		})
		for _, key := range []string{"X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded"} {
			if v := r.Header.Get(key); v != "" {
				t.Errorf("%s forwarded: %q", key, v)
			}
		}
	})
}
