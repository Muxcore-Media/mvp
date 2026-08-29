package main

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
)

func parseTrustedProxies(cidrs []string) []net.IPNet {
	loopback := []net.IPNet{
		{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
	}
	if len(cidrs) == 0 {
		return loopback
	}
	parsed := make([]net.IPNet, 0, len(cidrs)+1)
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			slog.Warn("ignoring invalid trusted proxy CIDR", "cidr", c, "error", err)
			continue
		}
		parsed = append(parsed, *n)
	}
	if len(parsed) == 0 {
		return loopback
	}
	parsed = append(parsed, net.IPNet{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)})
	return parsed
}

func parseTrustedProxiesCSV(csv string) []net.IPNet {
	if strings.TrimSpace(csv) == "" {
		return parseTrustedProxies(nil)
	}
	return parseTrustedProxies(strings.Split(csv, ","))
}

func trustedProxiesFromEnv() []net.IPNet {
	return parseTrustedProxiesCSV(os.Getenv("MEDIA_UI_TRUSTED_PROXIES"))
}

func isTrustedProxy(addr string, trustedProxies []net.IPNet) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func requestFromTrustedProxy(r *http.Request, trustedProxies []net.IPNet) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return isTrustedProxy(host, trustedProxies)
}
