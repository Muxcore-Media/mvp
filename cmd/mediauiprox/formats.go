package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
)

func (s *server) handleFormats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.formats == nil {
		writeJSON(w, map[string]any{"available": false, "formats": []any{}, "profiles": []any{}, "release_profiles": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	writeJSON(w, s.formatsCatalog(ctx))
}

func (s *server) handleFormatsSyncTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Custom formats module is not connected.",
			"code":  "formats.unavailable",
		})
		return
	}
	var body struct {
		ScoreSet       string   `json:"scoreSet"`
		ImportProfiles *bool    `json:"importProfiles"`
		Services       []string `json:"services"`
		Official       *bool    `json:"official"`
		GuidesPath     string   `json:"guidesPath"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	official := body.Official != nil && *body.Official
	if !official && strings.EqualFold(strings.TrimSpace(body.GuidesPath), "official") {
		official = true
	}
	if official && !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{
			"error": "admin or manager role required to sync official TRaSH Guides",
			"code":  "formats.forbidden",
		})
		return
	}
	importProfiles := true
	if body.ImportProfiles != nil {
		importProfiles = *body.ImportProfiles
	}
	scoreSet := strings.TrimSpace(body.ScoreSet)
	if scoreSet == "" {
		scoreSet = "default"
	}
	guidesPath := ""
	if official {
		guidesPath = "official-refresh"
	}
	timeout := 30 * time.Second
	if official {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	resp, err := s.formats.SyncTrashGuides(ctx, &formatsv1.SyncTrashGuidesRequest{
		ScoreSet:       scoreSet,
		ImportProfiles: importProfiles,
		Services:       body.Services,
		GuidesPath:     guidesPath,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(),
			"code":  "formats.sync_failed",
		})
		return
	}
	out := s.formatsCatalog(ctx)
	out["available"] = true
	out["sync"] = map[string]any{
		"formatsUpserted":  resp.GetFormatsUpserted(),
		"formatsSkipped":   resp.GetFormatsSkipped(),
		"profilesUpserted": resp.GetProfilesUpserted(),
		"guidesPath":       resp.GetGuidesPath(),
		"warnings":         resp.GetWarnings(),
	}
	writeJSON(w, out)
}

func (s *server) handleFormatsScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Custom formats module is not connected.",
			"code":  "formats.unavailable",
		})
		return
	}
	var body struct {
		Title     string `json:"title"`
		ProfileID string `json:"profileId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Title) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "title is required",
			"code":  "formats.title_required",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.formats.ScoreRelease(ctx, &formatsv1.ScoreReleaseRequest{
		Title:     body.Title,
		ProfileId: body.ProfileID,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(),
			"code":  "formats.score_failed",
		})
		return
	}
	matches := make([]map[string]any, 0, len(resp.GetFormatMatches()))
	for _, m := range resp.GetFormatMatches() {
		matches = append(matches, map[string]any{
			"id":    m.GetFormatId(),
			"name":  m.GetFormatName(),
			"score": m.GetScore(),
		})
	}
	writeJSON(w, map[string]any{
		"totalScore":   resp.GetTotalScore(),
		"qualityScore": resp.GetQualityScore(),
		"formatScore":  resp.GetFormatScore(),
		"matches":      matches,
		"quality":      publicQualityInfo(resp.GetQuality()),
	})
}

func (s *server) handleFormatsParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Custom formats module is not connected.",
			"code":  "formats.unavailable",
		})
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Title) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "title is required",
			"code":  "formats.title_required",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.formats.ParseQuality(ctx, &formatsv1.ParseQualityRequest{Title: strings.TrimSpace(body.Title)})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(),
			"code":  "formats.parse_failed",
		})
		return
	}
	writeJSON(w, map[string]any{
		"available": true,
		"quality":   publicQualityInfo(resp.GetQuality()),
	})
}

func publicQualityInfo(q *formatsv1.QualityInfo) map[string]any {
	if q == nil {
		return map[string]any{
			"label":      "",
			"resolution": "",
			"source":     "",
			"codec":      "",
			"hdr":        false,
			"score":      int32(0),
		}
	}
	return map[string]any{
		"label":      q.GetLabel(),
		"resolution": q.GetResolution(),
		"source":     q.GetSource(),
		"codec":      q.GetCodec(),
		"hdr":        q.GetHdr(),
		"score":      q.GetScore(),
	}
}

func (s *server) parseQualityForTitle(ctx context.Context, title string) map[string]any {
	if s == nil || s.formats == nil {
		return nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	resp, err := s.formats.ParseQuality(ctx, &formatsv1.ParseQualityRequest{Title: title})
	if err != nil || resp == nil || resp.GetQuality() == nil {
		return nil
	}
	q := resp.GetQuality()
	if strings.TrimSpace(q.GetLabel()) == "" && strings.TrimSpace(q.GetResolution()) == "" {
		return nil
	}
	return publicQualityInfo(q)
}

func (s *server) formatsCatalog(ctx context.Context) map[string]any {
	formats := []map[string]any{}
	profiles := []map[string]any{}
	if s.formats == nil {
		return map[string]any{"available": false, "formats": formats, "profiles": profiles, "release_profiles": []any{}}
	}
	if list, err := s.formats.ListFormats(ctx, &formatsv1.ListFormatsRequest{}); err == nil {
		for _, f := range list.GetFormats() {
			formats = append(formats, publicCustomFormat(f))
		}
	}
	if list, err := s.formats.ListProfiles(ctx, &formatsv1.ListProfilesRequest{}); err == nil {
		for _, p := range list.GetProfiles() {
			profiles = append(profiles, publicQualityProfile(p))
		}
	}
	releaseProfiles := s.listPublicReleaseProfiles(ctx)
	return map[string]any{"available": true, "formats": formats, "profiles": profiles, "release_profiles": releaseProfiles}
}

func publicCustomFormat(f *formatsv1.CustomFormat) map[string]any {
	if f == nil {
		return map[string]any{}
	}
	rules := make([]map[string]any, 0, len(f.GetRules()))
	for _, rule := range f.GetRules() {
		if rule == nil {
			continue
		}
		rules = append(rules, map[string]any{
			"field":  rule.GetField(),
			"op":     rule.GetOp(),
			"value":  rule.GetValue(),
			"negate": rule.GetNegate(),
		})
	}
	return map[string]any{
		"id":        f.GetId(),
		"name":      f.GetName(),
		"score":     f.GetDefaultScore(),
		"ruleCount": len(rules),
		"rules":     rules,
	}
}

func publicQualityProfile(p *formatsv1.QualityProfile) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	scores := p.GetFormatScores()
	if scores == nil {
		scores = map[string]int32{}
	}
	return map[string]any{
		"id":                    p.GetId(),
		"name":                  p.GetName(),
		"min_score":             p.GetMinScore(),
		"cutoff_score":          p.GetCutoffScore(),
		"upgrade_allowed":       p.GetUpgradeAllowed(),
		"upgrade_delay_minutes": p.GetUpgradeDelayMinutes(),
		"format_scores":         scores,
		"minScore":              p.GetMinScore(),
		"cutoffScore":           p.GetCutoffScore(),
		"upgradeAllowed":        p.GetUpgradeAllowed(),
		"upgradeDelayMinutes":   p.GetUpgradeDelayMinutes(),
		"formatScores":          scores,
	}
}

type qualityProfileBody struct {
	Name                string           `json:"name"`
	MinScore            int32            `json:"min_score"`
	CutoffScore         int32            `json:"cutoff_score"`
	UpgradeAllowed      bool             `json:"upgrade_allowed"`
	UpgradeDelayMinutes int32            `json:"upgrade_delay_minutes"`
	FormatScores        map[string]int32 `json:"format_scores"`
	// camelCase aliases from the SPA
	MinScoreCamel            *int32           `json:"minScore"`
	CutoffScoreCamel         *int32           `json:"cutoffScore"`
	UpgradeAllowedCamel      *bool            `json:"upgradeAllowed"`
	UpgradeDelayMinutesCamel *int32           `json:"upgradeDelayMinutes"`
	FormatScoresCamel        map[string]int32 `json:"formatScores"`
}

func (b qualityProfileBody) minScore() int32 {
	if b.MinScoreCamel != nil {
		return *b.MinScoreCamel
	}
	return b.MinScore
}

func (b qualityProfileBody) cutoffScore() int32 {
	if b.CutoffScoreCamel != nil {
		return *b.CutoffScoreCamel
	}
	return b.CutoffScore
}

func (b qualityProfileBody) upgradeAllowed() bool {
	if b.UpgradeAllowedCamel != nil {
		return *b.UpgradeAllowedCamel
	}
	return b.UpgradeAllowed
}

func (b qualityProfileBody) upgradeDelayMinutes() int32 {
	if b.UpgradeDelayMinutesCamel != nil {
		return *b.UpgradeDelayMinutesCamel
	}
	return b.UpgradeDelayMinutes
}

func (b qualityProfileBody) formatScores() map[string]int32 {
	if len(b.FormatScoresCamel) > 0 {
		return b.FormatScoresCamel
	}
	if b.FormatScores == nil {
		return map[string]int32{}
	}
	return b.FormatScores
}

func (s *server) handleCreateQualityProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	var body qualityProfileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "formats.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name required", "code": "formats.profile_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.formats.CreateProfile(ctx, &formatsv1.CreateProfileRequest{
		Name:                name,
		MinScore:            body.minScore(),
		CutoffScore:         body.cutoffScore(),
		UpgradeAllowed:      body.upgradeAllowed(),
		UpgradeDelayMinutes: body.upgradeDelayMinutes(),
		FormatScores:        body.formatScores(),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.profile_create_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "profile": publicQualityProfile(resp.GetProfile())})
}

func (s *server) handlePatchQualityProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "formats.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "formats.profile_id_required"})
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "formats unavailable", "code": "formats.unavailable"})
		return
	}
	var body qualityProfileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "formats.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name required", "code": "formats.profile_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.formats.UpdateProfile(ctx, &formatsv1.UpdateProfileRequest{
		Id:                  id,
		Name:                name,
		MinScore:            body.minScore(),
		CutoffScore:         body.cutoffScore(),
		UpgradeAllowed:      body.upgradeAllowed(),
		UpgradeDelayMinutes: body.upgradeDelayMinutes(),
		FormatScores:        body.formatScores(),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.profile_update_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "profile": publicQualityProfile(resp.GetProfile())})
}

func (s *server) handleDeleteQualityProfile(w http.ResponseWriter, r *http.Request) {
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
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "formats.profile_id_required"})
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "formats unavailable", "code": "formats.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.formats.DeleteProfile(ctx, &formatsv1.DeleteProfileRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.profile_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}

type customFormatBody struct {
	Name         string `json:"name"`
	DefaultScore int32  `json:"default_score"`
	Score        *int32 `json:"score"`
	Rules        []struct {
		Field  string `json:"field"`
		Op     string `json:"op"`
		Value  string `json:"value"`
		Negate bool   `json:"negate"`
	} `json:"rules"`
}

func (b customFormatBody) defaultScore() int32 {
	if b.Score != nil {
		return *b.Score
	}
	return b.DefaultScore
}

func (b customFormatBody) rules() []*formatsv1.FormatRule {
	out := make([]*formatsv1.FormatRule, 0, len(b.Rules))
	for _, rule := range b.Rules {
		field := strings.TrimSpace(rule.Field)
		op := strings.TrimSpace(rule.Op)
		value := strings.TrimSpace(rule.Value)
		if field == "" || op == "" || value == "" {
			continue
		}
		out = append(out, &formatsv1.FormatRule{
			Field:  field,
			Op:     op,
			Value:  value,
			Negate: rule.Negate,
		})
	}
	return out
}

func (s *server) handleCreateCustomFormat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	var body customFormatBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "formats.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name required", "code": "formats.format_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.formats.CreateFormat(ctx, &formatsv1.CreateFormatRequest{
		Name:         name,
		DefaultScore: body.defaultScore(),
		Rules:        body.rules(),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.format_create_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "format": publicCustomFormat(resp.GetFormat())})
}

func (s *server) handlePatchCustomFormat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "formats.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "formats.format_id_required"})
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "formats unavailable", "code": "formats.unavailable"})
		return
	}
	var body customFormatBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "formats.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "name required", "code": "formats.format_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.formats.UpdateFormat(ctx, &formatsv1.UpdateFormatRequest{
		Id:           id,
		Name:         name,
		DefaultScore: body.defaultScore(),
		Rules:        body.rules(),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.format_update_failed"})
		return
	}
	writeJSON(w, map[string]any{"available": true, "format": publicCustomFormat(resp.GetFormat())})
}

func (s *server) handleDeleteCustomFormat(w http.ResponseWriter, r *http.Request) {
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
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "formats.format_id_required"})
		return
	}
	if s.formats == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "formats unavailable", "code": "formats.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.formats.DeleteFormat(ctx, &formatsv1.DeleteFormatRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "formats.format_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}
