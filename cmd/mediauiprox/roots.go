package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
)

func rootJSON(root *rootsv1.RootFolder) map[string]any {
	if root == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                 root.GetId(),
		"path":               root.GetPath(),
		"name":               root.GetName(),
		"media_kind":         root.GetMediaKind(),
		"accessible":         root.GetAccessible(),
		"free_bytes":         root.GetFreeBytes(),
		"total_bytes":        root.GetTotalBytes(),
		"naming_template_id": root.GetNamingTemplateId(),
		"is_default":         root.GetIsDefault(),
	}
}

func (s *server) handleListRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.roots == nil {
		writeJSON(w, map[string]any{"available": false, "roots": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.roots.ListRoots(ctx, &rootsv1.ListRootsRequest{MediaKind: strings.TrimSpace(r.URL.Query().Get("kind"))})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "roots": []any{}})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetRoots()))
	for _, root := range resp.GetRoots() {
		if root == nil {
			continue
		}
		out = append(out, rootJSON(root))
	}
	writeJSON(w, map[string]any{"available": true, "roots": out})
}

func (s *server) handleBrowseRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "roots.forbidden"})
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if s.roots == nil {
		writeJSON(w, map[string]any{"available": false, "path": path, "parent": "", "entries": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.roots.BrowsePath(ctx, &rootsv1.BrowsePathRequest{Path: path})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "path": path, "parent": "", "entries": []any{}, "error": err.Error()})
		return
	}
	entries := make([]map[string]any, 0, len(resp.GetEntries()))
	for _, ent := range resp.GetEntries() {
		if ent == nil {
			continue
		}
		entries = append(entries, map[string]any{
			"name":   ent.GetName(),
			"path":   ent.GetPath(),
			"is_dir": ent.GetIsDir(),
		})
	}
	writeJSON(w, map[string]any{
		"available": true,
		"path":      resp.GetPath(),
		"parent":    resp.GetParent(),
		"entries":   entries,
	})
}

func (s *server) handlePickRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "kind required", "code": "roots.kind_required"})
		return
	}
	if s.roots == nil {
		writeJSON(w, map[string]any{"available": false, "root": nil})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.roots.PickRoot(ctx, &rootsv1.PickRootRequest{MediaKind: kind})
	if err != nil || resp.GetRoot() == nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		writeJSON(w, map[string]any{"available": false, "root": nil, "error": errMsg})
		return
	}
	writeJSON(w, map[string]any{"available": true, "root": rootJSON(resp.GetRoot())})
}

func (s *server) handleProbeRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "roots.forbidden"})
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	path := strings.TrimSpace(body.Path)
	if path == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "path required", "code": "roots.path_required"})
		return
	}
	if s.roots == nil {
		writeJSON(w, map[string]any{
			"available":  false,
			"path":       path,
			"accessible": false,
			"free_bytes": 0,
			"total_bytes": 0,
			"error":      "roots unavailable",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.roots.ProbeRoot(ctx, &rootsv1.ProbeRootRequest{Path: path})
	if err != nil {
		writeJSON(w, map[string]any{
			"available":   false,
			"path":        path,
			"accessible":  false,
			"free_bytes":  0,
			"total_bytes": 0,
			"error":       err.Error(),
		})
		return
	}
	writeJSON(w, map[string]any{
		"available":   true,
		"path":        resp.GetPath(),
		"accessible":  resp.GetAccessible(),
		"free_bytes":  resp.GetFreeBytes(),
		"total_bytes": resp.GetTotalBytes(),
		"error":       resp.GetError(),
	})
}

func (s *server) handleCreateRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "roots.forbidden"})
		return
	}
	if s.roots == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "roots unavailable", "code": "roots.unavailable"})
		return
	}
	var body struct {
		Path      string `json:"path"`
		Name      string `json:"name"`
		MediaKind string `json:"media_kind"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "roots.invalid_json"})
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "path required", "code": "roots.path_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	isDefault := body.IsDefault
	resp, err := s.roots.CreateRoot(ctx, &rootsv1.CreateRootRequest{
		Path:      strings.TrimSpace(body.Path),
		Name:      strings.TrimSpace(body.Name),
		MediaKind: strings.TrimSpace(body.MediaKind),
		IsDefault: &isDefault,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "roots.create_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "root": rootJSON(resp.GetRoot())})
}

func (s *server) handlePatchRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "roots.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "roots.id_required"})
		return
	}
	if s.roots == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "roots unavailable", "code": "roots.unavailable"})
		return
	}
	var body struct {
		Path               string `json:"path"`
		Name               string `json:"name"`
		MediaKind          string `json:"media_kind"`
		MediaKindCamel     string `json:"mediaKind"`
		NamingTemplateID   string `json:"naming_template_id"`
		NamingTemplateCamel string `json:"namingTemplateId"`
		IsDefault          *bool  `json:"is_default"`
		IsDefaultCamel     *bool  `json:"isDefault"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "roots.invalid_json"})
		return
	}
	kind := strings.TrimSpace(body.MediaKind)
	if kind == "" {
		kind = strings.TrimSpace(body.MediaKindCamel)
	}
	tpl := strings.TrimSpace(body.NamingTemplateID)
	if tpl == "" {
		tpl = strings.TrimSpace(body.NamingTemplateCamel)
	}
	isDefault := body.IsDefault
	if isDefault == nil {
		isDefault = body.IsDefaultCamel
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.roots.UpdateRoot(ctx, &rootsv1.UpdateRootRequest{
		Id:                id,
		Path:              strings.TrimSpace(body.Path),
		Name:              strings.TrimSpace(body.Name),
		MediaKind:         kind,
		NamingTemplateId:  tpl,
		IsDefault:         isDefault,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "roots.update_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "root": rootJSON(resp.GetRoot())})
}

func (s *server) handleDeleteRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "roots.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "roots.id_required"})
		return
	}
	if s.roots == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "roots unavailable", "code": "roots.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := s.roots.DeleteRoot(ctx, &rootsv1.DeleteRootRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "roots.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}
