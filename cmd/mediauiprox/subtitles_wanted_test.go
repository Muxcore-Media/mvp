package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureSubtitleWanted struct {
	subtv1.UnimplementedSubtitleServiceServer
	wanted     []*subtv1.WantedItem
	providers  []*subtv1.SubtitleProvider
	history    []*subtv1.HistoryEntry
	profiles   []*subtv1.LanguageProfile
	created    string
	deleted    string
	searched   int32
	searchIDs  []string
	enabledID  string
	enabledVal bool
	cleared     bool
	profileName string
	blacklist   []*subtv1.BlacklistEntry
	unblocked   string
	media       []*subtv1.SubtitleMediaItem
	profileID   string
	mediaIDs    []string
	setMon      bool
	monitored   bool
	languages   []*subtv1.LanguageInfo
}

func (f *fixtureSubtitleWanted) ListWanted(context.Context, *subtv1.ListWantedRequest) (*subtv1.ListWantedResponse, error) {
	return &subtv1.ListWantedResponse{Items: f.wanted, Total: int32(len(f.wanted))}, nil
}

func (f *fixtureSubtitleWanted) UpsertWanted(_ context.Context, req *subtv1.UpsertWantedRequest) (*subtv1.UpsertWantedResponse, error) {
	f.created = req.GetItem().GetTitle()
	return &subtv1.UpsertWantedResponse{Item: &subtv1.WantedItem{Id: "want-new", Title: req.GetItem().GetTitle(), Language: "eng"}}, nil
}

func (f *fixtureSubtitleWanted) DeleteWanted(_ context.Context, req *subtv1.DeleteWantedRequest) (*subtv1.DeleteWantedResponse, error) {
	f.deleted = req.GetId()
	return &subtv1.DeleteWantedResponse{}, nil
}

func (f *fixtureSubtitleWanted) SearchWanted(_ context.Context, req *subtv1.SearchWantedRequest) (*subtv1.SearchWantedResponse, error) {
	f.searched = 2
	f.searchIDs = append([]string{}, req.GetMediaIds()...)
	return &subtv1.SearchWantedResponse{Searched: 2, Downloaded: 0}, nil
}

func (f *fixtureSubtitleWanted) ListProviders(context.Context, *subtv1.ListProvidersRequest) (*subtv1.ListProvidersResponse, error) {
	return &subtv1.ListProvidersResponse{Providers: f.providers}, nil
}

func (f *fixtureSubtitleWanted) SetProviderEnabled(_ context.Context, req *subtv1.SetProviderEnabledRequest) (*subtv1.SetProviderEnabledResponse, error) {
	f.enabledID = req.GetId()
	f.enabledVal = req.GetEnabled()
	return &subtv1.SetProviderEnabledResponse{Provider: &subtv1.SubtitleProvider{Id: req.GetId(), Name: "OpenSubtitles", Enabled: req.GetEnabled(), Implemented: true}}, nil
}

func (f *fixtureSubtitleWanted) ListHistory(context.Context, *subtv1.ListHistoryRequest) (*subtv1.ListHistoryResponse, error) {
	return &subtv1.ListHistoryResponse{Entries: f.history, Total: int32(len(f.history))}, nil
}

func (f *fixtureSubtitleWanted) ClearHistory(context.Context, *subtv1.ClearHistoryRequest) (*subtv1.ClearHistoryResponse, error) {
	f.cleared = true
	return &subtv1.ClearHistoryResponse{}, nil
}

func (f *fixtureSubtitleWanted) ListLanguageProfiles(context.Context, *subtv1.ListLanguageProfilesRequest) (*subtv1.ListLanguageProfilesResponse, error) {
	return &subtv1.ListLanguageProfilesResponse{Profiles: f.profiles}, nil
}

func (f *fixtureSubtitleWanted) ListLanguages(context.Context, *subtv1.ListLanguagesRequest) (*subtv1.ListLanguagesResponse, error) {
	return &subtv1.ListLanguagesResponse{Languages: f.languages}, nil
}

func (f *fixtureSubtitleWanted) UpsertLanguageProfile(_ context.Context, req *subtv1.UpsertLanguageProfileRequest) (*subtv1.UpsertLanguageProfileResponse, error) {
	f.profileName = req.GetProfile().GetName()
	return &subtv1.UpsertLanguageProfileResponse{Profile: req.GetProfile()}, nil
}

func (f *fixtureSubtitleWanted) ListBlacklist(context.Context, *subtv1.ListBlacklistRequest) (*subtv1.ListBlacklistResponse, error) {
	return &subtv1.ListBlacklistResponse{Entries: f.blacklist, Total: int32(len(f.blacklist))}, nil
}

func (f *fixtureSubtitleWanted) RemoveBlacklist(_ context.Context, req *subtv1.RemoveBlacklistRequest) (*subtv1.RemoveBlacklistResponse, error) {
	f.unblocked = req.GetId()
	return &subtv1.RemoveBlacklistResponse{}, nil
}

func (f *fixtureSubtitleWanted) ListMedia(context.Context, *subtv1.ListMediaRequest) (*subtv1.ListMediaResponse, error) {
	return &subtv1.ListMediaResponse{Items: f.media, Total: int32(len(f.media))}, nil
}

func (f *fixtureSubtitleWanted) SetMediaLanguageProfile(_ context.Context, req *subtv1.SetMediaLanguageProfileRequest) (*subtv1.SetMediaLanguageProfileResponse, error) {
	f.profileID = req.GetLanguageProfileId()
	f.mediaIDs = []string{req.GetMediaId()}
	return &subtv1.SetMediaLanguageProfileResponse{}, nil
}

func (f *fixtureSubtitleWanted) MassEditMedia(_ context.Context, req *subtv1.MassEditMediaRequest) (*subtv1.MassEditMediaResponse, error) {
	f.mediaIDs = append([]string{}, req.GetMediaIds()...)
	if pid := req.GetLanguageProfileId(); pid != "" {
		f.profileID = pid
	}
	f.setMon = req.GetSetMonitored()
	f.monitored = req.GetMonitored()
	return &subtv1.MassEditMediaResponse{Updated: int32(len(req.GetMediaIds()))}, nil
}

func privilegedSubtitleWanted(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureSubtitleWanted) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureSubtitleWanted{
		wanted: []*subtv1.WantedItem{{Id: "w1", Title: "Interstellar", Language: "eng", MediaType: "movie"}},
		providers: []*subtv1.SubtitleProvider{
			{Id: "opensubtitles", Name: "OpenSubtitles", Enabled: true, Implemented: true},
		},
		history:   []*subtv1.HistoryEntry{{Id: "h1", Title: "Interstellar", Provider: "opensubtitles", Action: "download"}},
		profiles:  []*subtv1.LanguageProfile{{Id: "lp_default", Name: "English", IsDefault: true, Languages: []*subtv1.LanguageRequirement{{Language: "eng"}}}},
		languages: []*subtv1.LanguageInfo{{Code: "eng", Name: "English"}, {Code: "spa", Name: "Spanish"}},
		blacklist: []*subtv1.BlacklistEntry{{Id: "bl1", Title: "Dune.2021.1080p", Provider: "opensubtitles", Language: "eng", Reason: "wrong hash"}},
		media: []*subtv1.SubtitleMediaItem{
			{Id: "mov1", Title: "Dune", MediaType: "movie", Monitored: true, LanguageProfileId: "lp_default", Year: 2021, HasFile: true},
			{Id: "ep1", Title: "Pilot", MediaType: "episode", SeriesId: "show1", SeriesName: "Severance", Season: 1, Episode: 1, LanguageProfileId: "lp_default", Monitored: true},
		},
	}
	srv := grpc.NewServer()
	subtv1.RegisterSubtitleServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{subtitles: subtv1.NewSubtitleServiceClient(conn), sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleListSubtitleWantedForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListSubtitleWanted(w, httptest.NewRequest(http.MethodGet, "/api/subtitles/wanted", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListSubtitleWantedUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/subtitles/wanted", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListSubtitleWanted(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Wanted    []any `json:"wanted"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Wanted == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListSubtitleWantedLive(t *testing.T) {
	s, req, _ := privilegedSubtitleWanted(t, http.MethodGet, "/api/subtitles/wanted", "")
	w := httptest.NewRecorder()
	s.handleListSubtitleWanted(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Wanted    []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"wanted"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Wanted[0].Title != "Interstellar" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateSearchDeleteWanted(t *testing.T) {
	s, req, fake := privilegedSubtitleWanted(t, http.MethodPost, "/api/subtitles/wanted", `{"title":"Dune","language":"eng"}`)
	w := httptest.NewRecorder()
	s.handleCreateSubtitleWanted(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	if fake.created != "Dune" {
		t.Fatalf("created %q", fake.created)
	}
	s, req, fake = privilegedSubtitleWanted(t, http.MethodPost, "/api/subtitles/wanted/search", `{}`)
	w = httptest.NewRecorder()
	s.handleSearchSubtitleWanted(w, req)
	if w.Code != http.StatusOK || fake.searched != 2 {
		t.Fatalf("search %d searched=%d %s", w.Code, fake.searched, w.Body.String())
	}
	s, req, fake = privilegedSubtitleWanted(t, http.MethodPost, "/api/subtitles/wanted/search", `{"media_ids":["mov1","ep1"]}`)
	w = httptest.NewRecorder()
	s.handleSearchSubtitleWanted(w, req)
	if w.Code != http.StatusOK || fake.searched != 2 || len(fake.searchIDs) != 2 || fake.searchIDs[0] != "mov1" {
		t.Fatalf("search media %d searched=%d ids=%v %s", w.Code, fake.searched, fake.searchIDs, w.Body.String())
	}
	s, req, fake = privilegedSubtitleWanted(t, http.MethodDelete, "/api/subtitles/wanted/w1", "")
	req.SetPathValue("id", "w1")
	w = httptest.NewRecorder()
	s.handleDeleteSubtitleWanted(w, req)
	if w.Code != http.StatusOK || fake.deleted != "w1" {
		t.Fatalf("delete %d deleted=%q %s", w.Code, fake.deleted, w.Body.String())
	}
}

func TestHandleListAndSetSubtitleProviders(t *testing.T) {
	s, req, _ := privilegedSubtitleWanted(t, http.MethodGet, "/api/subtitles/providers", "")
	w := httptest.NewRecorder()
	s.handleListSubtitleProviders(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Providers []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Providers[0].ID != "opensubtitles" {
		t.Fatalf("%#v", body)
	}
	s, req, fake := privilegedSubtitleWanted(t, http.MethodPut, "/api/subtitles/providers/opensubtitles", `{"enabled":false}`)
	req.SetPathValue("id", "opensubtitles")
	w = httptest.NewRecorder()
	s.handleSetSubtitleProvider(w, req)
	if w.Code != http.StatusOK || fake.enabledID != "opensubtitles" || fake.enabledVal {
		t.Fatalf("toggle %d id=%q enabled=%v %s", w.Code, fake.enabledID, fake.enabledVal, w.Body.String())
	}
}

func TestHandleSubtitleHistoryAndProfiles(t *testing.T) {
	s, req, _ := privilegedSubtitleWanted(t, http.MethodGet, "/api/subtitles/history", "")
	w := httptest.NewRecorder()
	s.handleListSubtitleHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("history %d %s", w.Code, w.Body.String())
	}
	s, req, fake := privilegedSubtitleWanted(t, http.MethodPost, "/api/subtitles/history/clear", "")
	w = httptest.NewRecorder()
	s.handleClearSubtitleHistory(w, req)
	if w.Code != http.StatusOK || !fake.cleared {
		t.Fatalf("clear %d cleared=%v", w.Code, fake.cleared)
	}
	s, req, _ = privilegedSubtitleWanted(t, http.MethodGet, "/api/subtitles/profiles", "")
	w = httptest.NewRecorder()
	s.handleListSubtitleProfiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("profiles %d %s", w.Code, w.Body.String())
	}
	s, req, fake = privilegedSubtitleWanted(t, http.MethodPut, "/api/subtitles/profiles", `{"name":"English+HI","languages":"eng+hi","is_default":true}`)
	w = httptest.NewRecorder()
	s.handleUpsertSubtitleProfile(w, req)
	if w.Code != http.StatusOK || fake.profileName != "English+HI" {
		t.Fatalf("upsert %d name=%q %s", w.Code, fake.profileName, w.Body.String())
	}
}

func TestHandleListSubtitleLanguagesForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListSubtitleLanguages(w, httptest.NewRequest(http.MethodGet, "/api/subtitles/languages", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListSubtitleLanguagesUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/subtitles/languages", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListSubtitleLanguages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Languages []any `json:"languages"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Languages == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListSubtitleLanguagesLive(t *testing.T) {
	s, req, _ := privilegedSubtitleWanted(t, http.MethodGet, "/api/subtitles/languages", "")
	w := httptest.NewRecorder()
	s.handleListSubtitleLanguages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Languages []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"languages"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Languages) != 2 || body.Languages[0].Code != "eng" || body.Languages[1].Name != "Spanish" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleSubtitleBlacklistForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListSubtitleBlacklist(w, httptest.NewRequest(http.MethodGet, "/api/subtitles/blacklist", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleSubtitleBlacklistUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/subtitles/blacklist", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListSubtitleBlacklist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Entries   []any `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Entries == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListAndRemoveSubtitleBlacklist(t *testing.T) {
	s, req, _ := privilegedSubtitleWanted(t, http.MethodGet, "/api/subtitles/blacklist", "")
	w := httptest.NewRecorder()
	s.handleListSubtitleBlacklist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Entries   []struct {
			Title  string `json:"title"`
			Reason string `json:"reason"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Entries[0].Title != "Dune.2021.1080p" {
		t.Fatalf("%#v", body)
	}
	s, req, fake := privilegedSubtitleWanted(t, http.MethodDelete, "/api/subtitles/blacklist/bl1", "")
	req.SetPathValue("id", "bl1")
	w = httptest.NewRecorder()
	s.handleRemoveSubtitleBlacklist(w, req)
	if w.Code != http.StatusOK || fake.unblocked != "bl1" {
		t.Fatalf("remove %d id=%q %s", w.Code, fake.unblocked, w.Body.String())
	}
}

func TestHandleListSubtitleMediaForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListSubtitleMedia(w, httptest.NewRequest(http.MethodGet, "/api/subtitles/media", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListSubtitleMediaUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/subtitles/media", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListSubtitleMedia(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Items     []any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Items == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListAndPatchSubtitleMedia(t *testing.T) {
	s, req, _ := privilegedSubtitleWanted(t, http.MethodGet, "/api/subtitles/media", "")
	w := httptest.NewRecorder()
	s.handleListSubtitleMedia(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title     string `json:"title"`
			MediaType string `json:"media_type"`
			Series    string `json:"series_name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 2 || body.Items[0].Title != "Dune" || body.Items[1].Series != "Severance" {
		t.Fatalf("%#v", body)
	}
	s, req, fake := privilegedSubtitleWanted(t, http.MethodPatch, "/api/subtitles/media/mov1", `{"language_profile_id":"lp_hi","monitored":false}`)
	req.SetPathValue("id", "mov1")
	w = httptest.NewRecorder()
	s.handlePatchSubtitleMedia(w, req)
	if w.Code != http.StatusOK || fake.profileID != "lp_hi" || !fake.setMon || fake.monitored {
		t.Fatalf("patch %d profile=%q set=%v mon=%v %s", w.Code, fake.profileID, fake.setMon, fake.monitored, w.Body.String())
	}
}

func TestHandleMassEditSubtitleMedia(t *testing.T) {
	s, req, fake := privilegedSubtitleWanted(t, http.MethodPost, "/api/subtitles/media/mass-edit", `{"media_ids":["ep1","ep2"],"language_profile_id":"lp_hi"}`)
	w := httptest.NewRecorder()
	s.handleMassEditSubtitleMedia(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.profileID != "lp_hi" || len(fake.mediaIDs) != 2 || fake.mediaIDs[0] != "ep1" {
		t.Fatalf("mass %#v", fake)
	}
	var body struct {
		OK      bool  `json:"ok"`
		Updated int32 `json:"updated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Updated != 2 {
		t.Fatalf("%#v", body)
	}
}
