package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type acquisitionPeerDef struct {
	ID    string
	Kind  string // indexer | downloader
	Label string
	URL   *url.URL
}

type acquisitionPeerJSON struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Live  bool   `json:"live"`
}

func (s *server) handleAcquisition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	writeJSON(w, s.acquisitionStatus(ctx))
}

func (s *server) acquisitionStatus(ctx context.Context) map[string]any {
	peers := make([]acquisitionPeerJSON, 0, len(s.acquisitionPeers)+1)
	hasIndexer := false
	hasDownloader := false
	for _, def := range s.acquisitionPeers {
		if def.URL == nil || strings.TrimSpace(def.URL.String()) == "" {
			continue
		}
		live := s.probeAcquisitionPeer(ctx, def.URL)
		peers = append(peers, acquisitionPeerJSON{
			ID: def.ID, Kind: def.Kind, Label: def.Label, Live: live,
		})
		if live && def.Kind == "indexer" {
			hasIndexer = true
		}
		if live && def.Kind == "downloader" {
			hasDownloader = true
		}
	}
	indexers, indexerAvailable, torznabLive := s.householdIndexers(ctx)
	caps, capsAvail := s.householdIndexerCaps(ctx)
	if s.indexer != nil {
		peers = append(peers, acquisitionPeerJSON{
			ID: "indexer-torznab", Kind: "indexer", Label: "Prowlarr / Jackett", Live: torznabLive,
		})
	}
	if torznabLive {
		hasIndexer = true
	}
	policy := liveGrabPolicy()
	ready := hasIndexer && hasDownloader
	msg := ""
	switch {
	case ready && policy.allowed:
		msg = "Indexer and downloader are connected. Requests can be grabbed."
	case ready && !policy.allowed:
		msg = "Indexer and downloader are connected, but live torrent grab needs a connected WireGuard VPN (WG_CONF)."
	case !hasIndexer && !hasDownloader:
		msg = "No indexer or downloader is running. Enable fixture acquisition or connect qBittorrent, SABnzbd, or debrid."
	case !hasIndexer:
		msg = "A downloader is up, but no indexer is connected."
	default:
		msg = "An indexer is up, but no downloader is connected."
	}
	return map[string]any{
		"ready":                  ready,
		"hasIndexer":             hasIndexer,
		"hasDownloader":          hasDownloader,
		"peers":                  peers,
		"indexers":               indexers,
		"indexers_available":     indexerAvailable,
		"capabilities":           caps,
		"capabilities_available": capsAvail,
		"message":                msg,
		"live_grab_allowed":      policy.allowed,
		"indexer_mode":           policy.indexerMode,
		"downloader_mode":        policy.downloaderMode,
		"vpn": map[string]any{
			"configured":   policy.vpnConfigured,
			"conf_present": policy.vpnConfPresent,
		},
	}
}

type liveGrabPolicyResult struct {
	allowed        bool
	indexerMode    string
	downloaderMode string
	vpnConfigured  bool
	vpnConfPresent bool
}

func liveGrabPolicy() liveGrabPolicyResult {
	engine := strings.ToLower(strings.TrimSpace(os.Getenv("DOWNLOADER_ENGINE")))
	if engine == "" {
		engine = "fixture"
	}
	downloaderMode := "live"
	if engine == "fixture" || engine == "fake" {
		downloaderMode = "fixture"
	}
	indexerMode := "live"
	if downloaderMode == "fixture" && strings.TrimSpace(os.Getenv("PROWLARR_URL")) == "" && strings.TrimSpace(os.Getenv("TORZNAB_URL")) == "" {
		indexerMode = "fixture"
	}
	wg := strings.TrimSpace(os.Getenv("WG_CONF"))
	confPresent := false
	if wg != "" {
		if _, err := os.Stat(wg); err == nil {
			confPresent = true
		}
	}
	allowed := downloaderMode == "fixture" || confPresent
	return liveGrabPolicyResult{
		allowed:        allowed,
		indexerMode:    indexerMode,
		downloaderMode: downloaderMode,
		vpnConfigured:  wg != "",
		vpnConfPresent: confPresent,
	}
}

func (s *server) probeAcquisitionPeer(ctx context.Context, raw *url.URL) bool {
	if raw == nil {
		return false
	}
	for _, path := range []string{"/healthz", "/"} {
		u := *raw
		u.Path = strings.TrimRight(raw.Path, "/") + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			continue
		}
		resp, err := upstreamClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 500 {
			return true
		}
	}
	return false
}

func (s *server) acquisitionModuleReady(ctx context.Context) bool {
	st := s.acquisitionStatus(ctx)
	ready, _ := st["ready"].(bool)
	return ready
}

func optionalAcquisitionPeer(id, kind, label, raw string) acquisitionPeerDef {
	return acquisitionPeerDef{ID: id, Kind: kind, Label: label, URL: optionalURL(raw)}
}
