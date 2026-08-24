package main

import (
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultUpstreamTimeout = 8 * time.Second

var upstreamClient = &http.Client{Timeout: defaultUpstreamTimeout}

func init() {
	if v := strings.TrimSpace(os.Getenv("MEDIA_UI_UPSTREAM_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			upstreamClient.Timeout = d
		}
	}
}
