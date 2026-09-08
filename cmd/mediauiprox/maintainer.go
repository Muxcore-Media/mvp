package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	maintainv1 "github.com/Muxcore-Media/media-library-maintainer/proto/maintainv1"
)

func candidateStatusLabel(s maintainv1.CandidateStatus) string {
	switch s {
	case maintainv1.CandidateStatus_CANDIDATE_STATUS_PENDING:
		return "pending"
	case maintainv1.CandidateStatus_CANDIDATE_STATUS_LEAVING_SOON:
		return "leaving_soon"
	case maintainv1.CandidateStatus_CANDIDATE_STATUS_APPROVED:
		return "approved"
	case maintainv1.CandidateStatus_CANDIDATE_STATUS_POSTPONED:
		return "postponed"
	case maintainv1.CandidateStatus_CANDIDATE_STATUS_CANCELLED:
		return "cancelled"
	case maintainv1.CandidateStatus_CANDIDATE_STATUS_COMPLETED:
		return "completed"
	case maintainv1.CandidateStatus_CANDIDATE_STATUS_FAILED:
		return "failed"
	default:
		return ""
	}
}

func arrActionLabel(a maintainv1.ArrAction) string {
	switch a {
	case maintainv1.ArrAction_ARR_ACTION_DELETE:
		return "delete"
	case maintainv1.ArrAction_ARR_ACTION_UNMONITOR:
		return "unmonitor"
	case maintainv1.ArrAction_ARR_ACTION_UNMONITOR_ONLY:
		return "unmonitor_only"
	case maintainv1.ArrAction_ARR_ACTION_REMOVE_IF_EMPTY:
		return "remove_if_empty"
	case maintainv1.ArrAction_ARR_ACTION_DO_NOTHING:
		return "do_nothing"
	case maintainv1.ArrAction_ARR_ACTION_MOVE:
		return "move"
	case maintainv1.ArrAction_ARR_ACTION_CHANGE_QUALITY_PROFILE:
		return "change_quality_profile"
	default:
		return ""
	}
}

func mediaScopeLabel(s maintainv1.MediaScope) string {
	switch s {
	case maintainv1.MediaScope_MEDIA_SCOPE_MOVIE:
		return "movie"
	case maintainv1.MediaScope_MEDIA_SCOPE_SERIES:
		return "series"
	case maintainv1.MediaScope_MEDIA_SCOPE_SEASON:
		return "season"
	case maintainv1.MediaScope_MEDIA_SCOPE_EPISODE:
		return "episode"
	case maintainv1.MediaScope_MEDIA_SCOPE_MOVIE_FILE:
		return "movie_file"
	default:
		return ""
	}
}

func publicMaintainerCandidate(c *maintainv1.Candidate) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":          c.GetId(),
		"item_id":     c.GetItemId(),
		"title":       c.GetTitle(),
		"year":        c.GetYear(),
		"tmdb_id":     c.GetTmdbId(),
		"imdb_id":     c.GetImdbId(),
		"scope":       mediaScopeLabel(c.GetScope()),
		"status":      candidateStatusLabel(c.GetStatus()),
		"arr_action":  arrActionLabel(c.GetArrAction()),
		"size_bytes":  c.GetSizeBytes(),
		"added_at":    c.GetAddedAt(),
		"act_after":   c.GetActAfter(),
		"error":       c.GetError(),
		"collection":  c.GetCollectionId(),
	}
}

func publicMaintainerRun(run *maintainv1.RunLog) map[string]any {
	if run == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                run.GetId(),
		"kind":              run.GetKind(),
		"status":            run.GetStatus(),
		"candidates_found":  run.GetCandidatesFound(),
		"actions_taken":     run.GetActionsTaken(),
		"actions_failed":    run.GetActionsFailed(),
		"dry_run":           run.GetDryRun(),
		"error":             run.GetError(),
		"started_at":        run.GetStartedAt(),
		"completed_at":      run.GetCompletedAt(),
	}
}

func publicMaintainerProtection(p *maintainv1.Protection) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         p.GetId(),
		"item_id":    p.GetItemId(),
		"title":      p.GetTitle(),
		"reason":     p.GetReason(),
		"scope":      mediaScopeLabel(p.GetScope()),
		"expires_at": p.GetExpiresAt(),
		"created_at": p.GetCreatedAt(),
	}
}

func publicMaintainerRule(r *maintainv1.RuleGroup) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                   r.GetId(),
		"name":                 r.GetName(),
		"enabled":              r.GetEnabled(),
		"scope":                mediaScopeLabel(r.GetScope()),
		"outcome":              ruleOutcomeLabel(r.GetOutcome()),
		"arr_action":           arrActionLabel(r.GetArrAction()),
		"definition_json":      r.GetDefinitionJson(),
		"auto_act_enabled":     r.GetAutoActEnabled(),
		"auto_act_delay_days":  r.GetAutoActDelayDays(),
		"max_actions_per_run":  r.GetMaxActionsPerRun(),
		"collection_id":        r.GetCollectionId(),
	}
}

func publicMaintainerCollection(c *maintainv1.Collection) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                   c.GetId(),
		"name":                 c.GetName(),
		"enabled":              c.GetEnabled(),
		"grace_days":           c.GetGraceDays(),
		"arr_action":           arrActionLabel(c.GetArrAction()),
		"leaving_soon_enabled": c.GetLeavingSoonEnabled(),
		"leaving_soon_label":   c.GetLeavingSoonLabel(),
		"created_at":           c.GetCreatedAt(),
		"updated_at":           c.GetUpdatedAt(),
	}
}

func publicMaintainerExclusion(l *maintainv1.ExclusionList) map[string]any {
	if l == nil {
		return map[string]any{}
	}
	ids := make([]int, 0, len(l.GetTmdbIds()))
	for _, id := range l.GetTmdbIds() {
		if id > 0 {
			ids = append(ids, int(id))
		}
	}
	return map[string]any{
		"id":           l.GetId(),
		"name":         l.GetName(),
		"type":         l.GetType(),
		"list_url":     l.GetListUrl(),
		"has_api_key":  strings.TrimSpace(l.GetApiKey()) != "",
		"tmdb_ids":     ids,
		"tmdb_count":   len(ids),
		"last_synced":  l.GetLastSynced(),
		"created_at":   l.GetCreatedAt(),
	}
}

func parseHouseholdTmdbIDs(raw string, ids []int32) []int32 {
	seen := map[int32]bool{}
	out := make([]int32, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	replacer := strings.NewReplacer("\n", ",", ";", ",", " ", ",")
	for _, part := range strings.Split(replacer.Replace(raw), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 {
			continue
		}
		id := int32(n)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func householdExclusionType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "mdblist":
		return "mdblist"
	case "justwatch":
		return "justwatch"
	case "local":
		return "local"
	default:
		return "trakt"
	}
}

func ruleOutcomeLabel(o maintainv1.RuleOutcome) string {
	switch o {
	case maintainv1.RuleOutcome_RULE_OUTCOME_CANDIDATE:
		return "candidate"
	case maintainv1.RuleOutcome_RULE_OUTCOME_PROTECT:
		return "protect"
	default:
		return ""
	}
}

func parseMediaScope(raw string) maintainv1.MediaScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "movie", "media_scope_movie", "1":
		return maintainv1.MediaScope_MEDIA_SCOPE_MOVIE
	case "series", "tv", "media_scope_series", "2":
		return maintainv1.MediaScope_MEDIA_SCOPE_SERIES
	case "season", "3":
		return maintainv1.MediaScope_MEDIA_SCOPE_SEASON
	case "episode", "4":
		return maintainv1.MediaScope_MEDIA_SCOPE_EPISODE
	default:
		return maintainv1.MediaScope_MEDIA_SCOPE_MOVIE
	}
}

func parseArrAction(raw string) maintainv1.ArrAction {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "delete", "1":
		return maintainv1.ArrAction_ARR_ACTION_DELETE
	case "unmonitor", "2":
		return maintainv1.ArrAction_ARR_ACTION_UNMONITOR
	case "unmonitor_only", "3":
		return maintainv1.ArrAction_ARR_ACTION_UNMONITOR_ONLY
	case "remove_if_empty", "4":
		return maintainv1.ArrAction_ARR_ACTION_REMOVE_IF_EMPTY
	case "do_nothing", "5":
		return maintainv1.ArrAction_ARR_ACTION_DO_NOTHING
	case "move", "6":
		return maintainv1.ArrAction_ARR_ACTION_MOVE
	default:
		return maintainv1.ArrAction_ARR_ACTION_DELETE
	}
}

func householdRuleDefinition(kind string, days int) (string, error) {
	if days < 1 {
		days = 90
	}
	var def map[string]any
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "last_watched":
		def = map[string]any{
			"op": "and",
			"conditions": []map[string]any{
				{"field": "watch.days_since_last_watched", "operator": "greater_than", "value": days},
			},
		}
	default:
		def = map[string]any{
			"op": "and",
			"conditions": []map[string]any{
				{"field": "watch.never_watched", "operator": "equals", "value": true},
				{"field": "media.days_since_added", "operator": "greater_than", "value": days},
			},
		}
	}
	b, err := json.Marshal(def)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func publicStorageMetric(m *maintainv1.StoragePathMetric) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return map[string]any{
		"path":           m.GetPath(),
		"free_percent":   m.GetFreePercent(),
		"free_bytes":     m.GetFreeBytes(),
		"total_bytes":    m.GetTotalBytes(),
		"library_bytes":  m.GetLibraryBytes(),
		"item_count":     m.GetItemCount(),
	}
}

func (s *server) handleListMaintainer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSON(w, map[string]any{
			"available":   false,
			"candidates":  []any{},
			"runs":        []any{},
			"storage":     []any{},
			"rules":       []any{},
			"protections": []any{},
			"collections": []any{},
			"exclusions":  []any{},
			"rules_total": 0,
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	listed, err := s.maintainer.ListCandidates(ctx, &maintainv1.ListCandidatesRequest{Page: 1, PageSize: 50})
	if err != nil {
		writeJSON(w, map[string]any{
			"available":   false,
			"candidates":  []any{},
			"runs":        []any{},
			"storage":     []any{},
			"rules":       []any{},
			"protections": []any{},
			"collections": []any{},
			"exclusions":  []any{},
			"rules_total": 0,
			"error":       err.Error(),
		})
		return
	}
	cands := make([]map[string]any, 0, len(listed.GetCandidates()))
	for _, row := range listed.GetCandidates() {
		if row == nil {
			continue
		}
		cands = append(cands, publicMaintainerCandidate(row))
	}
	runs := []map[string]any{}
	if resp, err := s.maintainer.ListRuns(ctx, &maintainv1.ListRunsRequest{Page: 1, PageSize: 10}); err == nil {
		for _, row := range resp.GetRuns() {
			if row == nil {
				continue
			}
			runs = append(runs, publicMaintainerRun(row))
		}
	}
	storage := []map[string]any{}
	if resp, err := s.maintainer.GetStorageMetrics(ctx, &maintainv1.GetStorageMetricsRequest{}); err == nil {
		for _, row := range resp.GetPaths() {
			if row == nil {
				continue
			}
			storage = append(storage, publicStorageMetric(row))
		}
	}
	rules := []map[string]any{}
	if resp, err := s.maintainer.ListRules(ctx, &maintainv1.ListRulesRequest{}); err == nil {
		for _, row := range resp.GetRules() {
			if row == nil {
				continue
			}
			rules = append(rules, publicMaintainerRule(row))
		}
	}
	protections := []map[string]any{}
	if resp, err := s.maintainer.ListProtections(ctx, &maintainv1.ListProtectionsRequest{}); err == nil {
		for _, row := range resp.GetProtections() {
			if row == nil {
				continue
			}
			protections = append(protections, publicMaintainerProtection(row))
		}
	}
	collections := []map[string]any{}
	if resp, err := s.maintainer.ListCollections(ctx, &maintainv1.ListCollectionsRequest{}); err == nil {
		for _, row := range resp.GetCollections() {
			if row == nil {
				continue
			}
			collections = append(collections, publicMaintainerCollection(row))
		}
	}
	exclusions := []map[string]any{}
	if resp, err := s.maintainer.ListExclusionLists(ctx, &maintainv1.ListExclusionListsRequest{}); err == nil {
		for _, row := range resp.GetLists() {
			if row == nil {
				continue
			}
			exclusions = append(exclusions, publicMaintainerExclusion(row))
		}
	}
	writeJSON(w, map[string]any{
		"available":   true,
		"candidates":  cands,
		"total":       listed.GetTotal(),
		"runs":        runs,
		"storage":     storage,
		"rules":       rules,
		"protections": protections,
		"collections": collections,
		"exclusions":  exclusions,
		"rules_total": len(rules),
	})
}

func (s *server) handleMaintainerScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body struct {
		DryRun *bool `json:"dry_run"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	dryRun := true
	if body.DryRun != nil {
		dryRun = *body.DryRun
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.maintainer.ScanNow(ctx, &maintainv1.ScanNowRequest{DryRun: dryRun})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.scan_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"ok":               true,
		"dry_run":          dryRun,
		"candidates_found": resp.GetCandidatesFound(),
		"run":              publicMaintainerRun(resp.GetRun()),
	})
}

func (s *server) handleMaintainerAct(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body struct {
		DryRun            bool    `json:"dry_run"`
		FreeUp            bool    `json:"free_up"`
		TargetFreePercent float64 `json:"target_free_percent"`
		MaxActions        int32   `json:"max_actions"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "maintainer.invalid_json"})
			return
		}
	}
	if body.FreeUp && body.TargetFreePercent <= 0 {
		body.TargetFreePercent = 15
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.maintainer.ActNow(ctx, &maintainv1.ActNowRequest{
		DryRun:            body.DryRun,
		FreeUp:            body.FreeUp,
		TargetFreePercent: body.TargetFreePercent,
		MaxActions:        body.MaxActions,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.act_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"ok":            true,
		"dry_run":       body.DryRun,
		"free_up":       body.FreeUp,
		"actions_taken": resp.GetActionsTaken(),
		"run":           publicMaintainerRun(resp.GetRun()),
	})
}

func (s *server) handleMaintainerCandidateAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "maintainer.id_required"})
		return
	}
	action := strings.TrimSpace(r.PathValue("action"))
	var body struct {
		Days int32 `json:"days"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var cand *maintainv1.Candidate
	switch action {
	case "approve":
		resp, err := s.maintainer.ApproveCandidate(ctx, &maintainv1.ApproveCandidateRequest{Id: id})
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.approve_failed"})
			return
		}
		cand = resp.GetCandidate()
	case "postpone":
		days := body.Days
		if days <= 0 {
			days = 7
		}
		resp, err := s.maintainer.PostponeCandidate(ctx, &maintainv1.PostponeCandidateRequest{Id: id, Days: days})
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.postpone_failed"})
			return
		}
		cand = resp.GetCandidate()
	case "cancel":
		resp, err := s.maintainer.CancelCandidate(ctx, &maintainv1.CancelCandidateRequest{Id: id})
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.cancel_failed"})
			return
		}
		cand = resp.GetCandidate()
	default:
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "unknown action", "code": "maintainer.unknown_action"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "action": action, "candidate": publicMaintainerCandidate(cand)})
}

type householdRuleBody struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Preset       string `json:"preset"`
	Days         int    `json:"days"`
	Scope        string `json:"scope"`
	Action       string `json:"action"`
	Enabled      *bool  `json:"enabled"`
	DefJSON      string `json:"definition_json"`
	CollectionID string `json:"collection_id"`
}

func (s *server) ruleFromHouseholdBody(body householdRuleBody) (*maintainv1.RuleGroup, error) {
	def := strings.TrimSpace(body.DefJSON)
	if def == "" {
		built, err := householdRuleDefinition(body.Preset, body.Days)
		if err != nil {
			return nil, err
		}
		def = built
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Stale unwatched"
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	return &maintainv1.RuleGroup{
		Id:               strings.TrimSpace(body.ID),
		Name:             name,
		Enabled:          enabled,
		Scope:            parseMediaScope(body.Scope),
		CollectionId:     strings.TrimSpace(body.CollectionID),
		DefinitionJson:   def,
		Outcome:          maintainv1.RuleOutcome_RULE_OUTCOME_CANDIDATE,
		ArrAction:        parseArrAction(body.Action),
		MaxActionsPerRun: 50,
	}, nil
}

func (s *server) handleUpsertMaintainerRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body householdRuleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "maintainer.invalid_json"})
		return
	}
	rule, err := s.ruleFromHouseholdBody(body)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": "maintainer.rule_invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.maintainer.UpsertRule(ctx, &maintainv1.UpsertRuleRequest{Rule: rule})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.rule_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "rule": publicMaintainerRule(resp.GetRule())})
}

func (s *server) handlePreviewMaintainerRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body householdRuleBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	rule, err := s.ruleFromHouseholdBody(body)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": "maintainer.rule_invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resp, err := s.maintainer.PreviewRule(ctx, &maintainv1.PreviewRuleRequest{Rule: rule, Limit: 20})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.preview_failed"})
		return
	}
	matches := make([]map[string]any, 0, len(resp.GetMatches()))
	for _, row := range resp.GetMatches() {
		if row == nil {
			continue
		}
		matches = append(matches, publicMaintainerCandidate(row))
	}
	writeJSON(w, map[string]any{"ok": true, "total": resp.GetTotal(), "matches": matches})
}

func (s *server) handleToggleMaintainerRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "maintainer.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	got, err := s.maintainer.GetRule(ctx, &maintainv1.GetRuleRequest{Id: id})
	if err != nil || got.GetRule() == nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "rule not found", "code": "maintainer.rule_missing"})
		return
	}
	rule := got.GetRule()
	rule.Enabled = !rule.GetEnabled()
	resp, err := s.maintainer.UpsertRule(ctx, &maintainv1.UpsertRuleRequest{Rule: rule})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.rule_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "rule": publicMaintainerRule(resp.GetRule())})
}

func (s *server) handleDeleteMaintainerRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "maintainer.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.maintainer.DeleteRule(ctx, &maintainv1.DeleteRuleRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.rule_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "removed": true, "id": id})
}

func (s *server) handleUpsertMaintainerProtection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body struct {
		ItemID    string `json:"item_id"`
		Title     string `json:"title"`
		Reason    string `json:"reason"`
		Scope     string `json:"scope"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "maintainer.invalid_json"})
		return
	}
	itemID := strings.TrimSpace(body.ItemID)
	if itemID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "item_id required", "code": "maintainer.item_id_required"})
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "Household favorite"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.maintainer.UpsertProtection(ctx, &maintainv1.UpsertProtectionRequest{Protection: &maintainv1.Protection{
		ItemId:    itemID,
		Title:     strings.TrimSpace(body.Title),
		Reason:    reason,
		Scope:     parseMediaScope(body.Scope),
		ExpiresAt: strings.TrimSpace(body.ExpiresAt),
	}})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.protect_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "protection": publicMaintainerProtection(resp.GetProtection())})
}

func (s *server) handleDeleteMaintainerProtection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "maintainer.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.maintainer.DeleteProtection(ctx, &maintainv1.DeleteProtectionRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.protect_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "removed": true, "id": id})
}

func (s *server) handleUpsertMaintainerCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		GraceDays          int    `json:"grace_days"`
		Action             string `json:"action"`
		LeavingSoonEnabled *bool  `json:"leaving_soon_enabled"`
		LeavingSoonLabel   string `json:"leaving_soon_label"`
		Enabled            *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "maintainer.invalid_json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Leaving soon"
	}
	grace := body.GraceDays
	if grace < 1 {
		grace = 7
	}
	leaving := true
	if body.LeavingSoonEnabled != nil {
		leaving = *body.LeavingSoonEnabled
	}
	label := strings.TrimSpace(body.LeavingSoonLabel)
	if label == "" {
		label = "Leaving Soon"
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.maintainer.UpsertCollection(ctx, &maintainv1.UpsertCollectionRequest{Collection: &maintainv1.Collection{
		Id:                 strings.TrimSpace(body.ID),
		Name:               name,
		Enabled:            enabled,
		GraceDays:          int32(grace),
		ArrAction:          parseArrAction(body.Action),
		LeavingSoonEnabled: leaving,
		LeavingSoonLabel:   label,
	}})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.collection_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "collection": publicMaintainerCollection(resp.GetCollection())})
}

func (s *server) handleDeleteMaintainerCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "maintainer.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.maintainer.DeleteCollection(ctx, &maintainv1.DeleteCollectionRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.collection_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "removed": true, "id": id})
}

func (s *server) handleUpsertMaintainerExclusion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body struct {
		ID      string  `json:"id"`
		Name    string  `json:"name"`
		Type    string  `json:"type"`
		ListURL string  `json:"list_url"`
		APIKey  string  `json:"api_key"`
		TmdbIDs []int32 `json:"tmdb_ids"`
		TmdbRaw string  `json:"tmdb_ids_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "maintainer.invalid_json"})
		return
	}
	ids := parseHouseholdTmdbIDs(body.TmdbRaw, body.TmdbIDs)
	typ := householdExclusionType(body.Type)
	if typ == "trakt" && strings.TrimSpace(body.Type) == "" && len(ids) > 0 && strings.TrimSpace(body.ListURL) == "" {
		typ = "local"
	}
	if typ != "local" && strings.TrimSpace(body.ListURL) == "" && len(ids) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "list_url or tmdb_ids required", "code": "maintainer.exclusion_invalid"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Never delete list"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.maintainer.UpsertExclusionList(ctx, &maintainv1.UpsertExclusionListRequest{List: &maintainv1.ExclusionList{
		Id:      strings.TrimSpace(body.ID),
		Name:    name,
		Type:    typ,
		ListUrl: strings.TrimSpace(body.ListURL),
		ApiKey:  strings.TrimSpace(body.APIKey),
		TmdbIds: ids,
	}})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.exclusion_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "exclusion": publicMaintainerExclusion(resp.GetList())})
}

func (s *server) handleDeleteMaintainerExclusion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "maintainer.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.maintainer.DeleteExclusionList(ctx, &maintainv1.DeleteExclusionListRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.exclusion_delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "removed": true, "id": id})
}

func (s *server) handleSyncMaintainerExclusions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.maintainer.SyncExclusionLists(ctx, &maintainv1.SyncExclusionListsRequest{})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.exclusion_sync_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "lists_synced": resp.GetListsSynced(), "ids_loaded": resp.GetIdsLoaded()})
}

func (s *server) handleExportMaintainerRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.maintainer.ExportRules(ctx, &maintainv1.ExportRulesRequest{})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.export_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "rules_json": resp.GetRulesJson(), "rules_yaml": resp.GetRulesYaml()})
}

func (s *server) handleImportMaintainerRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "maintainer.forbidden"})
		return
	}
	if s.maintainer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-library-maintainer unavailable", "code": "maintainer.unavailable"})
		return
	}
	var body struct {
		RulesJSON string `json:"rules_json"`
		RulesYAML string `json:"rules_yaml"`
		Replace   bool   `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "maintainer.invalid_json"})
		return
	}
	if strings.TrimSpace(body.RulesJSON) == "" && strings.TrimSpace(body.RulesYAML) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "rules_json or rules_yaml required", "code": "maintainer.import_empty"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resp, err := s.maintainer.ImportRules(ctx, &maintainv1.ImportRulesRequest{
		RulesJson: strings.TrimSpace(body.RulesJSON),
		RulesYaml: strings.TrimSpace(body.RulesYAML),
		Replace:   body.Replace,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "maintainer.import_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "imported": resp.GetImported()})
}
