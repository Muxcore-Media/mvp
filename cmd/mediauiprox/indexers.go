package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	indexerv1 "github.com/Muxcore-Media/contracts-indexer/muxcore/indexer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func publicIndexerSpec(spec *indexerv1.IndexerSpec) map[string]any {
	if spec == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":             spec.GetId(),
		"name":           spec.GetName(),
		"protocol":       spec.GetProtocol(),
		"implementation": spec.GetImplementation(),
		"base_url":       spec.GetBaseUrl(),
		"enable":         spec.GetEnable(),
		"has_api_key":    spec.GetHasApiKey(),
		"language":       spec.GetLanguage(),
	}
}

func writeIndexerRPCError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "indexers.upstream"})
		return
	}
	switch st.Code() {
	case codes.Unimplemented, codes.FailedPrecondition:
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": st.Message(), "code": "indexers.unsupported"})
	case codes.InvalidArgument:
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": st.Message(), "code": "indexers.invalid"})
	case codes.NotFound:
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": st.Message(), "code": "indexers.not_found"})
	default:
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": st.Message(), "code": "indexers.upstream"})
	}
}

func (s *server) requireIndexerWrite(w http.ResponseWriter, r *http.Request) bool {
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "indexers.forbidden"})
		return false
	}
	if s.indexer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "indexer module is not connected", "code": "indexers.unavailable"})
		return false
	}
	return true
}

func decodeIndexerSpec(r *http.Request) (*indexerv1.IndexerSpec, error) {
	var body struct {
		Name           string `json:"name"`
		Protocol       string `json:"protocol"`
		Implementation string `json:"implementation"`
		BaseURL        string `json:"base_url"`
		APIKey         string `json:"api_key"`
		Enable         *bool  `json:"enable"`
		Language       string `json:"language"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
	}
	enable := true
	if body.Enable != nil {
		enable = *body.Enable
	}
	return &indexerv1.IndexerSpec{
		Name:           strings.TrimSpace(body.Name),
		Protocol:       strings.TrimSpace(body.Protocol),
		Implementation: strings.TrimSpace(body.Implementation),
		BaseUrl:        strings.TrimSpace(body.BaseURL),
		ApiKey:         strings.TrimSpace(body.APIKey),
		Enable:         enable,
		Language:       strings.TrimSpace(body.Language),
	}, nil
}

func (s *server) handleCreateIndexer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireIndexerWrite(w, r) {
		return
	}
	spec, err := decodeIndexerSpec(r)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "indexers.invalid_json"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := s.indexer.CreateIndexer(ctx, &indexerv1.CreateIndexerRequest{Indexer: spec})
	if err != nil {
		writeIndexerRPCError(w, err)
		return
	}
	writeJSON(w, publicIndexerSpec(out))
}

func (s *server) handleUpdateIndexer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireIndexerWrite(w, r) {
		return
	}
	id, err := strconv.Atoi(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id <= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "indexer id is required", "code": "indexers.invalid"})
		return
	}
	spec, err := decodeIndexerSpec(r)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "indexers.invalid_json"})
		return
	}
	spec.Id = int32(id)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := s.indexer.UpdateIndexer(ctx, &indexerv1.UpdateIndexerRequest{Indexer: spec})
	if err != nil {
		writeIndexerRPCError(w, err)
		return
	}
	writeJSON(w, publicIndexerSpec(out))
}

func (s *server) handleDeleteIndexer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.requireIndexerWrite(w, r) {
		return
	}
	id, err := strconv.Atoi(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id <= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "indexer id is required", "code": "indexers.invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.indexer.DeleteIndexer(ctx, &indexerv1.DeleteIndexerRequest{Id: int32(id)}); err != nil {
		writeIndexerRPCError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
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
