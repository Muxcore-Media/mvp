package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	"google.golang.org/grpc"
)

type fixtureGuard struct {
	guardv1.PlaybackGuardServiceClient
	rules      []*guardv1.GuardRule
	violations []*guardv1.Violation
	trust      []*guardv1.TrustScore
	upserted   string
	deleted    string
	acked      []string
	resetUser  string
	mergedFrom string
	mergedTo   string
}

func (f *fixtureGuard) ListRules(context.Context, *guardv1.ListRulesRequest, ...grpc.CallOption) (*guardv1.ListRulesResponse, error) {
	return &guardv1.ListRulesResponse{Rules: f.rules}, nil
}

func (f *fixtureGuard) UpsertRule(_ context.Context, req *guardv1.UpsertRuleRequest, _ ...grpc.CallOption) (*guardv1.UpsertRuleResponse, error) {
	rule := req.GetRule()
	if rule.GetId() == "" {
		rule.Id = "rule-new"
	}
	f.upserted = rule.GetName()
	return &guardv1.UpsertRuleResponse{Rule: rule}, nil
}

func (f *fixtureGuard) DeleteRule(_ context.Context, req *guardv1.DeleteRuleRequest, _ ...grpc.CallOption) (*guardv1.DeleteRuleResponse, error) {
	f.deleted = req.GetId()
	return &guardv1.DeleteRuleResponse{Ok: true}, nil
}

func (f *fixtureGuard) ListViolations(context.Context, *guardv1.ListViolationsRequest, ...grpc.CallOption) (*guardv1.ListViolationsResponse, error) {
	return &guardv1.ListViolationsResponse{Violations: f.violations}, nil
}

func (f *fixtureGuard) AcknowledgeViolations(_ context.Context, req *guardv1.AcknowledgeViolationsRequest, _ ...grpc.CallOption) (*guardv1.AcknowledgeViolationsResponse, error) {
	f.acked = append([]string(nil), req.GetViolationIds()...)
	return &guardv1.AcknowledgeViolationsResponse{Updated: int32(len(f.acked))}, nil
}

func (f *fixtureGuard) TerminateSession(context.Context, *guardv1.TerminateSessionRequest, ...grpc.CallOption) (*guardv1.TerminateSessionResponse, error) {
	return &guardv1.TerminateSessionResponse{Ok: true}, nil
}

func (f *fixtureGuard) GetTrustScore(context.Context, *guardv1.GetTrustScoreRequest, ...grpc.CallOption) (*guardv1.GetTrustScoreResponse, error) {
	return &guardv1.GetTrustScoreResponse{}, nil
}

func (f *fixtureGuard) ListTrustScores(context.Context, *guardv1.ListTrustScoresRequest, ...grpc.CallOption) (*guardv1.ListTrustScoresResponse, error) {
	return &guardv1.ListTrustScoresResponse{Scores: f.trust}, nil
}

func (f *fixtureGuard) ResetTrustScore(_ context.Context, req *guardv1.ResetTrustScoreRequest, _ ...grpc.CallOption) (*guardv1.ResetTrustScoreResponse, error) {
	f.resetUser = req.GetUserId()
	return &guardv1.ResetTrustScoreResponse{Score: &guardv1.TrustScore{UserId: req.GetUserId(), UserName: req.GetUserName(), Score: 100}}, nil
}

func (f *fixtureGuard) MergeUsers(_ context.Context, req *guardv1.MergeUsersRequest, _ ...grpc.CallOption) (*guardv1.MergeUsersResponse, error) {
	f.mergedFrom = req.GetSourceUserName()
	f.mergedTo = req.GetTargetUserName()
	return &guardv1.MergeUsersResponse{ViolationsUpdated: 2, AliasesCreated: 1, SessionsUpdated: 3}, nil
}

func privilegedGuardRequest(t *testing.T, s *server, method, path, body string) *http.Request {
	t.Helper()
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return req
}

func TestHandleGetGuardUnavailable(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleGetGuard(w, privilegedGuardRequest(t, s, http.MethodGet, "/api/guard", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Rules     []any `json:"rules"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Rules == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleGetGuardForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleGetGuard(w, httptest.NewRequest(http.MethodGet, "/api/guard", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleGetGuardLive(t *testing.T) {
	s := &server{
		sessions: newSessionStore(time.Hour),
		playbackGuard: &fixtureGuard{
			rules: []*guardv1.GuardRule{{
				Id: "r1", Type: guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, Name: "Two streams", Enabled: true,
				Params: map[string]string{"max_streams": "2"},
			}},
			violations: []*guardv1.Violation{{
				Id: "v1", RuleId: "r1", RuleType: guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS,
				UserName: "pat", Summary: "pat has 3 active streams", Severity: "warning",
			}},
			trust: []*guardv1.TrustScore{{UserId: "u1", UserName: "pat", Score: 80}},
		},
	}
	w := httptest.NewRecorder()
	s.handleGetGuard(w, privilegedGuardRequest(t, s, http.MethodGet, "/api/guard", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Rules     []struct {
			Name string `json:"name"`
		} `json:"rules"`
		Violations []struct {
			Summary string `json:"summary"`
		} `json:"violations"`
		Trust []struct {
			Score int `json:"score"`
		} `json:"trust"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Rules) != 1 || body.Rules[0].Name != "Two streams" || len(body.Violations) != 1 || body.Trust[0].Score != 80 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleUpsertGuardRule(t *testing.T) {
	fx := &fixtureGuard{}
	s := &server{sessions: newSessionStore(time.Hour), playbackGuard: fx}
	w := httptest.NewRecorder()
	s.handleUpsertGuardRule(w, privilegedGuardRequest(t, s, http.MethodPut, "/api/guard/rules",
		`{"type":"concurrent_streams","name":"Two streams","enabled":true,"params":{"max_streams":"2"}}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fx.upserted != "Two streams" {
		t.Fatalf("upserted %q", fx.upserted)
	}
}

func TestHandleDeleteGuardRule(t *testing.T) {
	fx := &fixtureGuard{}
	s := &server{sessions: newSessionStore(time.Hour), playbackGuard: fx}
	req := privilegedGuardRequest(t, s, http.MethodDelete, "/api/guard/rules/r1", "")
	req.SetPathValue("id", "r1")
	w := httptest.NewRecorder()
	s.handleDeleteGuardRule(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fx.deleted != "r1" {
		t.Fatalf("deleted %q", fx.deleted)
	}
}

func TestHandleAckGuardViolations(t *testing.T) {
	fx := &fixtureGuard{}
	s := &server{sessions: newSessionStore(time.Hour), playbackGuard: fx}
	w := httptest.NewRecorder()
	s.handleAckGuardViolations(w, privilegedGuardRequest(t, s, http.MethodPost, "/api/guard/violations/ack", `{"ids":["v1"]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if len(fx.acked) != 1 || fx.acked[0] != "v1" {
		t.Fatalf("acked %#v", fx.acked)
	}
}

func TestHandleResetGuardTrust(t *testing.T) {
	fx := &fixtureGuard{}
	s := &server{sessions: newSessionStore(time.Hour), playbackGuard: fx}
	w := httptest.NewRecorder()
	s.handleResetGuardTrust(w, privilegedGuardRequest(t, s, http.MethodPost, "/api/guard/trust/reset", `{"user_id":"u1","user_name":"pat"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fx.resetUser != "u1" {
		t.Fatalf("reset %q", fx.resetUser)
	}
}

func TestHandleMergeGuardUsers(t *testing.T) {
	fx := &fixtureGuard{}
	s := &server{sessions: newSessionStore(time.Hour), playbackGuard: fx}
	w := httptest.NewRecorder()
	s.handleMergeGuardUsers(w, privilegedGuardRequest(t, s, http.MethodPost, "/api/guard/users/merge",
		`{"source_user_name":"pat-tv","target_user_name":"pat"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fx.mergedFrom != "pat-tv" || fx.mergedTo != "pat" {
		t.Fatalf("merged %q -> %q", fx.mergedFrom, fx.mergedTo)
	}
	var body struct {
		AliasesCreated int `json:"aliasesCreated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AliasesCreated != 1 {
		t.Fatalf("%#v", body)
	}
}
