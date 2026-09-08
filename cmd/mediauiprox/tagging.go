package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func publicTaggingTag(rec map[string]any) map[string]any {
	id := firstString(rec, "id", "ID")
	name := firstString(rec, "name", "Name")
	if id == "" && name == "" {
		return nil
	}
	return map[string]any{
		"id":       id,
		"name":     name,
		"category": firstString(rec, "category", "Category"),
		"color":    firstString(rec, "color", "Color"),
	}
}

func publicTaggingRule(rec map[string]any) map[string]any {
	id := firstString(rec, "id", "ID")
	pattern := firstString(rec, "pattern", "Pattern")
	if id == "" && pattern == "" {
		return nil
	}
	return map[string]any{
		"id":      id,
		"tagId":   firstString(rec, "tagId", "tag_id", "TagID"),
		"field":   firstString(rec, "field", "Field"),
		"match":   firstString(rec, "match", "Match"),
		"pattern": pattern,
		"enabled": boolVal(rec, "enabled", "Enabled"),
	}
}

func taggingRows(raw any) []map[string]any {
	switch v := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, row := range v {
			rec, ok := row.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, rec)
		}
		return out
	case map[string]any:
		if rows := homeOrKey(v, "tags", "rules", "items"); rows != nil {
			out := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				rec, ok := row.(map[string]any)
				if !ok {
					continue
				}
				out = append(out, rec)
			}
			return out
		}
	}
	return nil
}

func (s *server) handleGetTagging(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tagging.forbidden"})
		return
	}
	if s.taggingHTTP == nil || strings.TrimSpace(s.taggingHTTP.String()) == "" {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}, "rules": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	tagsRaw, err := s.taggingDo(ctx, http.MethodGet, "/api/tags", nil)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}, "rules": []any{}, "error": err.Error()})
		return
	}
	rulesRaw, err := s.taggingDo(ctx, http.MethodGet, "/api/rules", nil)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "tags": []any{}, "rules": []any{}, "error": err.Error()})
		return
	}
	tags := make([]map[string]any, 0)
	for _, rec := range taggingRows(tagsRaw) {
		if pub := publicTaggingTag(rec); pub != nil {
			tags = append(tags, pub)
		}
	}
	rules := make([]map[string]any, 0)
	for _, rec := range taggingRows(rulesRaw) {
		if pub := publicTaggingRule(rec); pub != nil {
			rules = append(rules, pub)
		}
	}
	writeJSON(w, map[string]any{"available": true, "tags": tags, "rules": rules})
}

func (s *server) handleCreateTaggingTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tagging.forbidden"})
		return
	}
	if s.taggingHTTP == nil || strings.TrimSpace(s.taggingHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-tagging is not connected", "code": "tagging.unavailable"})
		return
	}
	var body struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Color    string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "tagging.invalid_json"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name required", "code": "tagging.name_required"})
		return
	}
	payload, _ := json.Marshal(map[string]any{"name": body.Name, "category": body.Category, "color": body.Color})
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	raw, err := s.taggingDo(ctx, http.MethodPost, "/api/tags", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tagging.create_failed"})
		return
	}
	rec, _ := raw.(map[string]any)
	if rec == nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid tagging response", "code": "tagging.invalid_response"})
		return
	}
	writeJSON(w, publicTaggingTag(rec))
}

func (s *server) handleDeleteTaggingTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tagging.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tagging.id_required"})
		return
	}
	if s.taggingHTTP == nil || strings.TrimSpace(s.taggingHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-tagging is not connected", "code": "tagging.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.taggingDo(ctx, http.MethodDelete, "/api/tags/"+id, nil); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tagging.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *server) handleUpsertTaggingRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tagging.forbidden"})
		return
	}
	if s.taggingHTTP == nil || strings.TrimSpace(s.taggingHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-tagging is not connected", "code": "tagging.unavailable"})
		return
	}
	var body struct {
		ID      string `json:"id"`
		TagID   string `json:"tag_id"`
		Field   string `json:"field"`
		Match   string `json:"match"`
		Pattern string `json:"pattern"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "tagging.invalid_json"})
		return
	}
	if strings.TrimSpace(body.TagID) == "" || strings.TrimSpace(body.Pattern) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "tag_id and pattern required", "code": "tagging.rule_required"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	payload, _ := json.Marshal(map[string]any{
		"id":      body.ID,
		"tag_id":  body.TagID,
		"field":   body.Field,
		"match":   body.Match,
		"pattern": body.Pattern,
		"enabled": enabled,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	raw, err := s.taggingDo(ctx, http.MethodPost, "/api/rules", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tagging.rule_failed"})
		return
	}
	rec, _ := raw.(map[string]any)
	if rec == nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid tagging response", "code": "tagging.invalid_response"})
		return
	}
	writeJSON(w, publicTaggingRule(rec))
}

func (s *server) handleDeleteTaggingRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tagging.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "tagging.id_required"})
		return
	}
	if s.taggingHTTP == nil || strings.TrimSpace(s.taggingHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-tagging is not connected", "code": "tagging.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.taggingDo(ctx, http.MethodDelete, "/api/rules/"+id, nil); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tagging.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *server) handleClassifyTagging(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "tagging.forbidden"})
		return
	}
	if s.taggingHTTP == nil || strings.TrimSpace(s.taggingHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-tagging is not connected", "code": "tagging.unavailable"})
		return
	}
	var body struct {
		MediaID    string   `json:"media_id"`
		MediaID2   string   `json:"mediaId"`
		Title      string   `json:"title"`
		Genres     []string `json:"genres"`
		Path       string   `json:"path"`
		MediaType  string   `json:"media_type"`
		MediaType2 string   `json:"mediaType"`
		Merge      *bool    `json:"merge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "tagging.invalid_json"})
		return
	}
	mediaID := firstNonEmpty(strings.TrimSpace(body.MediaID), strings.TrimSpace(body.MediaID2))
	if mediaID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "media_id required", "code": "tagging.media_id_required"})
		return
	}
	merge := true
	if body.Merge != nil {
		merge = *body.Merge
	}
	payload, _ := json.Marshal(map[string]any{
		"media_id":   mediaID,
		"title":      strings.TrimSpace(body.Title),
		"genres":     body.Genres,
		"path":       strings.TrimSpace(body.Path),
		"media_type": firstNonEmpty(strings.TrimSpace(body.MediaType), strings.TrimSpace(body.MediaType2)),
		"merge":      merge,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	raw, err := s.taggingDo(ctx, http.MethodPost, "/api/classify", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "tagging.classify_failed"})
		return
	}
	rec, _ := raw.(map[string]any)
	if rec == nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "invalid tagging response", "code": "tagging.invalid_response"})
		return
	}
	tags := make([]map[string]any, 0)
	for _, row := range taggingRows(rec) {
		if pub := publicTaggingTag(row); pub != nil {
			tags = append(tags, pub)
		}
	}
	if len(tags) == 0 {
		for _, row := range homeOrKey(rec, "tags", "Tags") {
			mapped, ok := row.(map[string]any)
			if !ok {
				continue
			}
			if pub := publicTaggingTag(mapped); pub != nil {
				tags = append(tags, pub)
			}
		}
	}
	writeJSON(w, map[string]any{
		"mediaId":        firstNonEmpty(firstString(rec, "mediaId", "media_id"), mediaID),
		"tags":           tags,
		"matchedRuleIds": stringList(rec, "matchedRuleIds", "matched_rule_ids"),
	})
}

func (s *server) taggingDo(ctx context.Context, method, path string, payload []byte) (any, error) {
	if s.taggingHTTP == nil || strings.TrimSpace(s.taggingHTTP.String()) == "" {
		return nil, errPlaybackMonitorStatus(http.StatusServiceUnavailable, "media-tagging is not connected")
	}
	u := *s.taggingHTTP
	u.Path = strings.TrimRight(s.taggingHTTP.Path, "/") + path
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, errPlaybackMonitorStatus(resp.StatusCode, msg)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
