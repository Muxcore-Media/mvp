package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func defaultTranscoderURL() string {
	if value, ok := os.LookupEnv("TRANSCODER_HTTP_URL"); ok {
		return strings.TrimSpace(value)
	}
	if meshInsecureAllowed() {
		return "http://127.0.0.1:9526"
	}
	return "https://127.0.0.1:9526"
}

func newTranscoderTransport(target *url.URL) (http.RoundTripper, error) {
	if target == nil {
		return nil, nil
	}
	if target.Host == "" || target.User != nil || (target.Scheme != "https" && target.Scheme != "http") {
		return nil, fmt.Errorf("TRANSCODER_HTTP_URL must be an HTTP(S) origin without credentials")
	}
	if target.Scheme != "https" && !meshInsecureAllowed() {
		return nil, fmt.Errorf("TRANSCODER_HTTP_URL must use HTTPS outside insecure dev mode")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if target.Scheme == "https" {
		config, err := meshTLSConfig()
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = config
	}
	return transport, nil
}

// transcoderProxy returns a reverse proxy to the transcoder module.
//
// It uses Rewrite only: NewSingleHostReverseProxy also sets the deprecated
// Director, and a proxy with both set fails every request. Rewrite runs after
// hop-by-hop headers are removed, so a client "Connection: Authorization"
// cannot strip the operator token set below.
func (s *server) transcoderProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: s.transcoderTransport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Same scheme/host/path-join/query-merge the single-host Director
			// applied; also points Host at the target rather than the browser.
			pr.SetURL(target)
			for key := range pr.Out.Header {
				if isClientIdentityHeader(key) {
					delete(pr.Out.Header, key)
				}
			}
			// Rewrite strips client Forwarded/X-Forwarded-*. Keep the prior
			// X-Forwarded-For chain and append the peer, as Director did.
			appendForwardedFor(pr)
			// The certificate identifies the BFF in household. Dev containers use
			// the operator's shared token; browser credentials never cross here.
			if s.transcoderToken != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+s.transcoderToken)
			}
		},
	}
}

// appendForwardedFor reproduces ReverseProxy's Director-mode X-Forwarded-For:
// the inbound chain (if any) followed by the immediate peer address.
func appendForwardedFor(pr *httputil.ProxyRequest) {
	clientIP, _, err := net.SplitHostPort(pr.In.RemoteAddr)
	if err != nil {
		return
	}
	if prior := pr.In.Header["X-Forwarded-For"]; len(prior) > 0 {
		clientIP = strings.Join(prior, ", ") + ", " + clientIP
	}
	pr.Out.Header.Set("X-Forwarded-For", clientIP)
}
