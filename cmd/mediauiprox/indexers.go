package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	indexerv1 "github.com/Muxcore-Media/contracts-indexer/muxcore/indexer/v1"
)

func publicIndexerCaps(caps *indexerv1.GetCapabilitiesResponse) map[string]any {
	if caps == nil {
		return map[string]any{}
	}
	cats := make([]string, 0, len(caps.GetSupportedCategories()))
	for _, c := range caps.GetSupportedCategories() {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		cats = append(cats, c)
	}
	protos := make([]string, 0, len(caps.GetSupportedProtocols()))
	for _, p := range caps.GetSupportedProtocols() {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		protos = append(protos, p)
	}
	return map[string]any{
		"supports_search":       caps.GetSupportsSearch(),
		"supports_movie_search": caps.GetSupportsMovieSearch(),
		"supports_tv_search":    caps.GetSupportsTvSearch(),
		"supports_music_search": caps.GetSupportsMusicSearch(),
		"supports_book_search":  caps.GetSupportsBookSearch(),
		"supports_id_search":    caps.GetSupportsIdSearch(),
		"supports_season_pack":  caps.GetSupportsSeasonPack(),
		"supported_categories":  cats,
		"supported_protocols":   protos,
	}
}

func (s *server) householdIndexerCaps(ctx context.Context) (map[string]any, bool) {
	if s.indexer == nil {
		return map[string]any{}, false
	}
	resp, err := s.indexer.GetCapabilities(ctx, &indexerv1.GetCapabilitiesRequest{})
	if err != nil || resp == nil {
		return map[string]any{}, false
	}
	return publicIndexerCaps(resp), true
}

func publicIndexer(info *indexerv1.IndexerInfo) map[string]any {
	if info == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         info.GetId(),
		"name":       info.GetName(),
		"protocol":   info.GetProtocol(),
		"language":   info.GetLanguage(),
		"configured": info.GetConfigured(),
	}
}

func (s *server) householdIndexers(ctx context.Context) (rows []map[string]any, available bool, live bool) {
	if s.indexer == nil {
		return []map[string]any{}, false, false
	}
	resp, err := s.indexer.ListIndexers(ctx, &indexerv1.ListIndexersRequest{})
	if err != nil {
		return []map[string]any{}, false, false
	}
	out := make([]map[string]any, 0, len(resp.GetIndexers()))
	for _, info := range resp.GetIndexers() {
		row := publicIndexer(info)
		if info.GetName() == "" && info.GetId() == 0 {
			continue
		}
		if info.GetConfigured() {
			live = true
		}
		out = append(out, row)
	}
	return out, true, live
}

func (s *server) handleListIndexers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rows, available, _ := s.householdIndexers(ctx)
	caps, capsAvail := s.householdIndexerCaps(ctx)
	writeJSON(w, map[string]any{"available": available, "indexers": rows, "capabilities": caps, "capabilities_available": capsAvail})
}
