package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
	"google.golang.org/grpc"
)

type stubFormatsClient struct {
	formats  []*formatsv1.CustomFormat
	profiles []*formatsv1.QualityProfile
	release  []*formatsv1.ReleaseProfile
	sync     *formatsv1.SyncTrashGuidesResponse
	score    *formatsv1.ScoreReleaseResponse
	created        *formatsv1.CreateProfileRequest
	updated        *formatsv1.UpdateProfileRequest
	deleted        string
	createdFormat  *formatsv1.CreateFormatRequest
	updatedFormat  *formatsv1.UpdateFormatRequest
	deletedFormat  string
	upsertedRelease *formatsv1.UpsertReleaseProfileRequest
	deletedRelease  string
	lastSync        *formatsv1.SyncTrashGuidesRequest
	parsedTitle     string
	parsed          *formatsv1.ParseQualityResponse
}

func (s *stubFormatsClient) ListFormats(context.Context, *formatsv1.ListFormatsRequest, ...grpc.CallOption) (*formatsv1.ListFormatsResponse, error) {
	return &formatsv1.ListFormatsResponse{Formats: s.formats}, nil
}
func (s *stubFormatsClient) CreateFormat(_ context.Context, req *formatsv1.CreateFormatRequest, _ ...grpc.CallOption) (*formatsv1.CreateFormatResponse, error) {
	s.createdFormat = req
	return &formatsv1.CreateFormatResponse{Format: &formatsv1.CustomFormat{
		Id: "cf-new", Name: req.GetName(), DefaultScore: req.GetDefaultScore(), Rules: req.GetRules(),
	}}, nil
}
func (s *stubFormatsClient) UpdateFormat(_ context.Context, req *formatsv1.UpdateFormatRequest, _ ...grpc.CallOption) (*formatsv1.UpdateFormatResponse, error) {
	s.updatedFormat = req
	return &formatsv1.UpdateFormatResponse{Format: &formatsv1.CustomFormat{
		Id: req.GetId(), Name: req.GetName(), DefaultScore: req.GetDefaultScore(), Rules: req.GetRules(),
	}}, nil
}
func (s *stubFormatsClient) DeleteFormat(_ context.Context, req *formatsv1.DeleteFormatRequest, _ ...grpc.CallOption) (*formatsv1.DeleteFormatResponse, error) {
	s.deletedFormat = req.GetId()
	return &formatsv1.DeleteFormatResponse{}, nil
}
func (s *stubFormatsClient) ListProfiles(context.Context, *formatsv1.ListProfilesRequest, ...grpc.CallOption) (*formatsv1.ListProfilesResponse, error) {
	return &formatsv1.ListProfilesResponse{Profiles: s.profiles}, nil
}
func (s *stubFormatsClient) CreateProfile(_ context.Context, req *formatsv1.CreateProfileRequest, _ ...grpc.CallOption) (*formatsv1.CreateProfileResponse, error) {
	s.created = req
	p := &formatsv1.QualityProfile{
		Id: "qp-new", Name: req.GetName(), MinScore: req.GetMinScore(), CutoffScore: req.GetCutoffScore(),
		UpgradeAllowed: req.GetUpgradeAllowed(), UpgradeDelayMinutes: req.GetUpgradeDelayMinutes(), FormatScores: req.GetFormatScores(),
	}
	s.profiles = append(s.profiles, p)
	return &formatsv1.CreateProfileResponse{Profile: p}, nil
}
func (s *stubFormatsClient) UpdateProfile(_ context.Context, req *formatsv1.UpdateProfileRequest, _ ...grpc.CallOption) (*formatsv1.UpdateProfileResponse, error) {
	s.updated = req
	return &formatsv1.UpdateProfileResponse{Profile: &formatsv1.QualityProfile{
		Id: req.GetId(), Name: req.GetName(), MinScore: req.GetMinScore(), CutoffScore: req.GetCutoffScore(),
		UpgradeAllowed: req.GetUpgradeAllowed(), UpgradeDelayMinutes: req.GetUpgradeDelayMinutes(), FormatScores: req.GetFormatScores(),
	}}, nil
}
func (s *stubFormatsClient) DeleteProfile(_ context.Context, req *formatsv1.DeleteProfileRequest, _ ...grpc.CallOption) (*formatsv1.DeleteProfileResponse, error) {
	s.deleted = req.GetId()
	return &formatsv1.DeleteProfileResponse{}, nil
}
func (s *stubFormatsClient) ScoreRelease(context.Context, *formatsv1.ScoreReleaseRequest, ...grpc.CallOption) (*formatsv1.ScoreReleaseResponse, error) {
	if s.score != nil {
		return s.score, nil
	}
	return &formatsv1.ScoreReleaseResponse{TotalScore: 100, FormatScore: 50, Quality: &formatsv1.QualityInfo{Label: "1080p Remux"}}, nil
}
func (s *stubFormatsClient) ParseQuality(_ context.Context, req *formatsv1.ParseQualityRequest, _ ...grpc.CallOption) (*formatsv1.ParseQualityResponse, error) {
	s.parsedTitle = req.GetTitle()
	if s.parsed != nil {
		return s.parsed, nil
	}
	return &formatsv1.ParseQualityResponse{Quality: &formatsv1.QualityInfo{
		Label: "1080p Remux", Resolution: "1080p", Source: "Remux", Codec: "hevc", Hdr: true, Score: 150,
	}}, nil
}
func (s *stubFormatsClient) SyncTrashGuides(_ context.Context, req *formatsv1.SyncTrashGuidesRequest, _ ...grpc.CallOption) (*formatsv1.SyncTrashGuidesResponse, error) {
	s.lastSync = req
	if s.sync != nil {
		return s.sync, nil
	}
	return &formatsv1.SyncTrashGuidesResponse{FormatsUpserted: 8, ProfilesUpserted: 3, GuidesPath: "embedded:guides-fixture"}, nil
}
func (s *stubFormatsClient) ListReleaseProfiles(context.Context, *formatsv1.ListReleaseProfilesRequest, ...grpc.CallOption) (*formatsv1.ListReleaseProfilesResponse, error) {
	return &formatsv1.ListReleaseProfilesResponse{Profiles: s.release}, nil
}
func (s *stubFormatsClient) UpsertReleaseProfile(_ context.Context, req *formatsv1.UpsertReleaseProfileRequest, _ ...grpc.CallOption) (*formatsv1.UpsertReleaseProfileResponse, error) {
	s.upsertedRelease = req
	enabled := true
	if req.Enabled != nil {
		enabled = req.GetEnabled()
	}
	return &formatsv1.UpsertReleaseProfileResponse{Profile: &formatsv1.ReleaseProfile{
		Id: "rpg-new", Name: req.GetName(), Preferred: req.GetPreferred(),
		MustContain: req.GetMustContain(), MustNotContain: req.GetMustNotContain(),
		PreferredScore: req.GetPreferredScore(), Enabled: enabled,
	}}, nil
}
func (s *stubFormatsClient) DeleteReleaseProfile(_ context.Context, req *formatsv1.DeleteReleaseProfileRequest, _ ...grpc.CallOption) (*formatsv1.DeleteReleaseProfileResponse, error) {
	s.deletedRelease = req.GetId()
	return &formatsv1.DeleteReleaseProfileResponse{}, nil
}

func TestHandleFormatsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleFormats(w, httptest.NewRequest(http.MethodGet, "/api/formats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available {
		t.Fatal("expected available=false")
	}
}

func TestHandleFormatsAndSync(t *testing.T) {
	s := &server{formats: &stubFormatsClient{
		formats: []*formatsv1.CustomFormat{{Id: "cf_trash_1", Name: "Remux-1080p", DefaultScore: 1850}},
		profiles: []*formatsv1.QualityProfile{
			{Id: "qp_radarr_hd_bluray_web", Name: "HD Bluray + WEB", CutoffScore: 10000, UpgradeAllowed: true},
		},
		release: []*formatsv1.ReleaseProfile{{Id: "rpg1", Name: "Default Blocklist", MustNotContain: []string{"cam"}, Enabled: true}},
		sync:    &formatsv1.SyncTrashGuidesResponse{FormatsUpserted: 8, ProfilesUpserted: 3, GuidesPath: "embedded:guides-fixture"},
	}}
	w := httptest.NewRecorder()
	s.handleFormats(w, httptest.NewRequest(http.MethodGet, "/api/formats", nil))
	var listed struct {
		Available bool `json:"available"`
		Formats   []struct {
			Name  string `json:"name"`
			Score int32  `json:"score"`
		} `json:"formats"`
		Profiles []struct {
			Name string `json:"name"`
		} `json:"profiles"`
		ReleaseProfiles []struct {
			Name string `json:"name"`
		} `json:"release_profiles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if !listed.Available || listed.Formats[0].Name != "Remux-1080p" || listed.Profiles[0].Name != "HD Bluray + WEB" || listed.ReleaseProfiles[0].Name != "Default Blocklist" {
		t.Fatalf("catalog %#v", listed)
	}

	sw := httptest.NewRecorder()
	s.handleFormatsSyncTrash(sw, httptest.NewRequest(http.MethodPost, "/api/formats/sync-trash", strings.NewReader(`{"importProfiles":true}`)))
	if sw.Code != http.StatusOK {
		t.Fatalf("sync status %d %s", sw.Code, sw.Body.String())
	}
	var synced struct {
		Sync struct {
			FormatsUpserted int32  `json:"formatsUpserted"`
			GuidesPath      string `json:"guidesPath"`
		} `json:"sync"`
	}
	if err := json.NewDecoder(sw.Body).Decode(&synced); err != nil {
		t.Fatal(err)
	}
	if synced.Sync.FormatsUpserted != 8 || synced.Sync.GuidesPath == "" {
		t.Fatalf("sync %#v", synced)
	}
	if s.formats.(*stubFormatsClient).lastSync.GetGuidesPath() != "" {
		t.Fatal("household sync must not pass a client guides path")
	}
}

func TestHandleFormatsSyncTrashIgnoresClientPath(t *testing.T) {
	s := &server{formats: &stubFormatsClient{}}
	w := httptest.NewRecorder()
	s.handleFormatsSyncTrash(w, httptest.NewRequest(http.MethodPost, "/api/formats/sync-trash", strings.NewReader(`{"guidesPath":"/etc/passwd"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if s.formats.(*stubFormatsClient).lastSync.GetGuidesPath() != "" {
		t.Fatalf("leaked path %q", s.formats.(*stubFormatsClient).lastSync.GetGuidesPath())
	}
}

func TestHandleFormatsSyncTrashOfficialForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour), formats: &stubFormatsClient{}}
	w := httptest.NewRecorder()
	s.handleFormatsSyncTrash(w, httptest.NewRequest(http.MethodPost, "/api/formats/sync-trash", strings.NewReader(`{"official":true}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleFormatsSyncTrashOfficial(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodPost, "/api/formats/sync-trash", `{"official":true,"importProfiles":true}`)
	fake.sync = &formatsv1.SyncTrashGuidesResponse{FormatsUpserted: 200, ProfilesUpserted: 6, GuidesPath: "/var/lib/media-custom-formats/trash-guides-official"}
	w := httptest.NewRecorder()
	s.handleFormatsSyncTrash(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.lastSync == nil || fake.lastSync.GetGuidesPath() != "official" {
		t.Fatalf("sync req %#v", fake.lastSync)
	}
}

func TestHandleFormatsScore(t *testing.T) {
	s := &server{formats: &stubFormatsClient{}}
	w := httptest.NewRecorder()
	s.handleFormatsScore(w, httptest.NewRequest(http.MethodPost, "/api/formats/score", strings.NewReader(`{"title":"Dune.2021.1080p.REMUX.mkv"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		TotalScore int32 `json:"totalScore"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.TotalScore != 100 {
		t.Fatalf("score=%d", body.TotalScore)
	}
}

func TestHandleFormatsParse(t *testing.T) {
	s := &server{formats: &stubFormatsClient{}}
	w := httptest.NewRecorder()
	s.handleFormatsParse(w, httptest.NewRequest(http.MethodPost, "/api/formats/parse", strings.NewReader(`{"title":"Dune.2021.1080p.REMUX.mkv"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Quality   struct {
			Label      string `json:"label"`
			Resolution string `json:"resolution"`
			Source     string `json:"source"`
			HDR        bool   `json:"hdr"`
		} `json:"quality"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Quality.Label != "1080p Remux" || body.Quality.Resolution != "1080p" || !body.Quality.HDR {
		t.Fatalf("parse %#v", body)
	}
	if s.formats.(*stubFormatsClient).parsedTitle != "Dune.2021.1080p.REMUX.mkv" {
		t.Fatalf("title %q", s.formats.(*stubFormatsClient).parsedTitle)
	}
}

func TestHandleFormatsParseUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleFormatsParse(w, httptest.NewRequest(http.MethodPost, "/api/formats/parse", strings.NewReader(`{"title":"Dune"}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleFormatsParseRequiresTitle(t *testing.T) {
	s := &server{formats: &stubFormatsClient{}}
	w := httptest.NewRecorder()
	s.handleFormatsParse(w, httptest.NewRequest(http.MethodPost, "/api/formats/parse", strings.NewReader(`{}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestCapabilitiesFormatsWhenClientSet(t *testing.T) {
	s := &server{
		movies:       stubMoviesClient{},
		tv:           stubTVClient{},
		formats:      &stubFormatsClient{},
		libraryPaths: newLibraryPathsStore("", t.TempDir()),
	}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var body struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Features["formats"] {
		t.Fatalf("expected formats true, got %#v", body.Features)
	}
}

func privilegedFormatsRequest(t *testing.T, method, path, body string) (*server, *http.Request, *stubFormatsClient) {
	t.Helper()
	fake := &stubFormatsClient{}
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{formats: fake, sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleCreateQualityProfileForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour), formats: &stubFormatsClient{}}
	w := httptest.NewRecorder()
	s.handleCreateQualityProfile(w, httptest.NewRequest(http.MethodPost, "/api/formats/profiles", strings.NewReader(`{"name":"HD"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleCreateQualityProfile(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodPost, "/api/formats/profiles", `{"name":"UHD","min_score":0,"cutoff_score":15000,"upgrade_allowed":true,"format_scores":{"cf1":2000}}`)
	w := httptest.NewRecorder()
	s.handleCreateQualityProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.created == nil || fake.created.GetName() != "UHD" || fake.created.GetCutoffScore() != 15000 || fake.created.GetFormatScores()["cf1"] != 2000 {
		t.Fatalf("created %#v", fake.created)
	}
}

func TestHandlePatchQualityProfile(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodPatch, "/api/formats/profiles/qp1", `{"name":"HD","cutoffScore":12000,"upgradeAllowed":true}`)
	req.SetPathValue("id", "qp1")
	w := httptest.NewRecorder()
	s.handlePatchQualityProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.updated == nil || fake.updated.GetId() != "qp1" || fake.updated.GetCutoffScore() != 12000 || !fake.updated.GetUpgradeAllowed() {
		t.Fatalf("updated %#v", fake.updated)
	}
}

func TestHandleDeleteQualityProfile(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodDelete, "/api/formats/profiles/qp1", "")
	req.SetPathValue("id", "qp1")
	w := httptest.NewRecorder()
	s.handleDeleteQualityProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deleted != "qp1" {
		t.Fatalf("deleted %q", fake.deleted)
	}
}

func TestHandleCreateCustomFormatForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour), formats: &stubFormatsClient{}}
	w := httptest.NewRecorder()
	s.handleCreateCustomFormat(w, httptest.NewRequest(http.MethodPost, "/api/formats", strings.NewReader(`{"name":"REMUX"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleCreateCustomFormat(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodPost, "/api/formats", `{"name":"REMUX","score":2000,"rules":[{"field":"title","op":"contains","value":"REMUX"}]}`)
	w := httptest.NewRecorder()
	s.handleCreateCustomFormat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.createdFormat == nil || fake.createdFormat.GetName() != "REMUX" || fake.createdFormat.GetDefaultScore() != 2000 {
		t.Fatalf("created %#v", fake.createdFormat)
	}
	if len(fake.createdFormat.GetRules()) != 1 || fake.createdFormat.GetRules()[0].GetValue() != "REMUX" {
		t.Fatalf("rules %#v", fake.createdFormat.GetRules())
	}
}

func TestHandlePatchCustomFormat(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodPatch, "/api/formats/cf1", `{"name":"HDR","default_score":500,"rules":[{"field":"title","op":"contains","value":"HDR","negate":false}]}`)
	req.SetPathValue("id", "cf1")
	w := httptest.NewRecorder()
	s.handlePatchCustomFormat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.updatedFormat == nil || fake.updatedFormat.GetId() != "cf1" || fake.updatedFormat.GetDefaultScore() != 500 {
		t.Fatalf("updated %#v", fake.updatedFormat)
	}
}

func TestHandleDeleteCustomFormat(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodDelete, "/api/formats/cf1", "")
	req.SetPathValue("id", "cf1")
	w := httptest.NewRecorder()
	s.handleDeleteCustomFormat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deletedFormat != "cf1" {
		t.Fatalf("deleted %q", fake.deletedFormat)
	}
}

func TestHandleUpsertReleaseProfile(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodPost, "/api/formats/release-profiles", `{"name":"No CAM","must_not_contain":["cam"],"preferred_text":"bluray, remux","preferred_score":15}`)
	w := httptest.NewRecorder()
	s.handleUpsertReleaseProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.upsertedRelease == nil || fake.upsertedRelease.GetName() != "No CAM" {
		t.Fatalf("upserted %#v", fake.upsertedRelease)
	}
	if len(fake.upsertedRelease.GetMustNotContain()) != 1 || fake.upsertedRelease.GetMustNotContain()[0] != "cam" {
		t.Fatalf("must_not %#v", fake.upsertedRelease.GetMustNotContain())
	}
	if len(fake.upsertedRelease.GetPreferred()) != 2 {
		t.Fatalf("preferred %#v", fake.upsertedRelease.GetPreferred())
	}
}

func TestHandleDeleteReleaseProfile(t *testing.T) {
	s, req, fake := privilegedFormatsRequest(t, http.MethodDelete, "/api/formats/release-profiles/rpg1", "")
	req.SetPathValue("id", "rpg1")
	w := httptest.NewRecorder()
	s.handleDeleteReleaseProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deletedRelease != "rpg1" {
		t.Fatalf("deleted %q", fake.deletedRelease)
	}
}
