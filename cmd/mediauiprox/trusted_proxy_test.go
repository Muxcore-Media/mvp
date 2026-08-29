package main

import (
	"net/http/httptest"
	"testing"
)

func TestPublicOriginHonorsTrustedForwardedProto(t *testing.T) {
	t.Parallel()
	s := &server{trustedProxies: parseTrustedProxies([]string{"127.0.0.0/8"})}
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "mux.zem.systems")
	got := s.publicOrigin(req)
	want := "https://mux.zem.systems"
	if got != want {
		t.Fatalf("publicOrigin() = %q, want %q", got, want)
	}
}

func TestPublicOriginIgnoresForwardedProtoFromUntrustedPeer(t *testing.T) {
	t.Parallel()
	s := &server{trustedProxies: parseTrustedProxies([]string{"127.0.0.0/8"})}
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	got := s.publicOrigin(req)
	if got == "https://evil.example" {
		t.Fatalf("untrusted peer must not drive https origin, got %q", got)
	}
}
