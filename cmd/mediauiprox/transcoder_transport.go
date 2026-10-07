package main

import (
	"fmt"
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

func (s *server) transcoderProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = s.transcoderTransport
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		for key := range r.Header {
			if isClientIdentityHeader(key) {
				r.Header.Del(key)
			}
		}
		// The certificate identifies the BFF in household. Dev containers use
		// the operator's shared token; browser credentials never cross here.
		if s.transcoderToken != "" {
			r.Header.Set("Authorization", "Bearer "+s.transcoderToken)
		}
	}
	return proxy
}
