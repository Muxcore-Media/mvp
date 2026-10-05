package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Regression: reverseProxy set both Director and Rewrite, so every proxied
// /stream/movies/ (and image) request returned 502.
func TestReverseProxyForwardsPathAndRange(t *testing.T) {
	var gotPath, gotRange, gotHost string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotRange, gotHost = r.URL.Path, r.Header.Get("Range"), r.Host
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "chunk")
	}))
	defer up.Close()
	target, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(reverseProxy(target))
	defer front.Close()

	req, err := http.NewRequest(http.MethodGet, front.URL+"/stream/movies/mv_550", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "chunk" {
		t.Fatalf("proxy: HTTP %d body %q", resp.StatusCode, body)
	}
	if gotPath != "/stream/movies/mv_550" || gotRange != "bytes=0-1023" || gotHost != target.Host {
		t.Fatalf("upstream saw path=%q range=%q host=%q", gotPath, gotRange, gotHost)
	}
}
