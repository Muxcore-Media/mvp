package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
)

func splitReleaseTerms(raw string, extra []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" || seen[strings.ToLower(term)] {
			return
		}
		seen[strings.ToLower(term)] = true
		out = append(out, term)
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	}) {
		add(part)
	}
	for _, part := range extra {
		add(part)
	}
	return out
}

func publicReleaseProfile(p *formatsv1.ReleaseProfile) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":               p.GetId(),
		"name":             p.GetName(),
		"preferred":        p.GetPreferred(),
		"must_contain":     p.GetMustContain(),
		"must_not_contain": p.GetMustNotContain(),
		"preferred_score":  p.GetPreferredScore(),
		"enabled":          p.GetEnabled(),
	}
}

func (s *server) listPublicReleaseProfiles(ctx context.Context) []map[string]any {
	out := []map[string]any{}
	if s.formats == nil {
		return out
	}
	list, err := s.formats.ListReleaseProfiles(ctx, &formatsv1.ListReleaseProfilesRequest{})
	if err != nil {
		return out
	}
	for _, p := range list.GetProfiles() {
		if p == nil {
			continue
		}
		out = append(out, publicReleaseProfile(p))
	}
	return out
}

func (s *server) handleListReleaseProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.formats == nil {
		writeJSON(w, map[string]any{"available": false, "profiles": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	list, err := s.formats.ListReleaseProfiles(ctx, &formatsv1.ListReleaseProfilesRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "profiles": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(list.GetProfiles()))
	for _, p := range list.GetProfiles() {
		if p == nil {
			continue
		}
		out = append(out, publicReleaseProfile(p))
	}
	writeJSON(w, map[string]any{"available": true, "profiles": out})
}

type releaseProfileBody struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Preferred       []string `json:"preferred"`
	MustContain     []string `json:"must_contain"`
	MustNotContain  []string `json:"must_not_contain"`
	PreferredScore  int32    `json:"preferred_score"`
	Enabled         *bool    `json:"enabled"`
	PreferredText   string   `json:"preferred_text"`
	MustContainText string   `json:"must_contain_text"`
	MustNotText     string   `json:"must_not_contain_text"`
}

func (b releaseProfileBody) preferred() []string {
	return splitReleaseTerms(b.PreferredText, b.Preferred)
}

func (b releaseProfileBody) mustContain() []string {
	return splitReleaseTerms(b.MustContainText, b.MustContain)
}

func (b releaseProfileBody) mustNotContain() []string {
	return splitReleaseTerms(b.MustNotText, b.MustNotContain)
}

func (s *server) handleUpsertReleaseProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodPut {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "formats.forbidden"})
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "formats unavailable", "code": "formats.unavailable"})
		return
	}
	var body releaseProfileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "formats.invalid_json"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		id = strings.TrimSpace(body.ID)
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name required", "code": "formats.release_name_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.formats.UpsertReleaseProfile(ctx, &formatsv1.UpsertReleaseProfileRequest{
		Id:             id,
		Name:           name,
		Preferred:      body.preferred(),
		MustContain:    body.mustContain(),
		MustNotContain: body.mustNotContain(),
		PreferredScore: body.PreferredScore,
		Enabled:        body.Enabled,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.release_upsert_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "profile": publicReleaseProfile(resp.GetProfile())})
}

func (s *server) handleDeleteReleaseProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "formats.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "formats.release_id_required"})
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "formats unavailable", "code": "formats.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.formats.DeleteReleaseProfile(ctx, &formatsv1.DeleteReleaseProfileRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.release_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}
