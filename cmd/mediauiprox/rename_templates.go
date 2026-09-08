package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
)

func publicNamingTemplate(t *renamev1.NamingTemplate) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         t.GetId(),
		"name":       t.GetName(),
		"media_type": t.GetMediaType(),
		"pattern":    t.GetPattern(),
		"is_default": t.GetIsDefault(),
		"updated_at": t.GetUpdatedAt(),
	}
}

func normalizeRenameMediaType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "tv", "show", "series":
		return "tv"
	default:
		return "movie"
	}
}

func (s *server) handleListRenameTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "rename.forbidden"})
		return
	}
	if s.rename == nil {
		writeJSON(w, map[string]any{"available": false, "templates": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.rename.ListTemplates(ctx, &renamev1.ListTemplatesRequest{
		MediaType: strings.TrimSpace(r.URL.Query().Get("media_type")),
	})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "templates": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetTemplates()))
	for _, t := range resp.GetTemplates() {
		if t == nil {
			continue
		}
		out = append(out, publicNamingTemplate(t))
	}
	writeJSON(w, map[string]any{"available": true, "templates": out})
}

func (s *server) handleCreateRenameTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "rename.forbidden"})
		return
	}
	if s.rename == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "rename unavailable", "code": "rename.unavailable"})
		return
	}
	var body struct {
		Name      string `json:"name"`
		MediaType string `json:"media_type"`
		Pattern   string `json:"pattern"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "rename.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	pattern := strings.TrimSpace(body.Pattern)
	if name == "" || pattern == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name and pattern required", "code": "rename.template_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.rename.CreateTemplate(ctx, &renamev1.CreateTemplateRequest{
		Name:      name,
		MediaType: normalizeRenameMediaType(body.MediaType),
		Pattern:   pattern,
		IsDefault: body.IsDefault,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "rename.template_create_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "template": publicNamingTemplate(resp.GetTemplate())})
}

func (s *server) handlePatchRenameTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "rename.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "rename.template_id_required"})
		return
	}
	if s.rename == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "rename unavailable", "code": "rename.unavailable"})
		return
	}
	var body struct {
		Name      string `json:"name"`
		Pattern   string `json:"pattern"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "rename.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	pattern := strings.TrimSpace(body.Pattern)
	if name == "" || pattern == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name and pattern required", "code": "rename.template_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.rename.UpdateTemplate(ctx, &renamev1.UpdateTemplateRequest{
		Id:        id,
		Name:      name,
		Pattern:   pattern,
		IsDefault: body.IsDefault,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "rename.template_update_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "template": publicNamingTemplate(resp.GetTemplate())})
}

func (s *server) handleDeleteRenameTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "rename.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "rename.template_id_required"})
		return
	}
	if s.rename == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "rename unavailable", "code": "rename.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.rename.DeleteTemplate(ctx, &renamev1.DeleteTemplateRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "rename.template_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}
