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

	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMovieTags struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	tags     []*mgmntv1.Tag
	itemTags map[string][]string
	created  string
	deleted  string
	setOn    string
}

func (f *fixtureMovieTags) ListTags(context.Context, *mgmntv1.ListTagsRequest) (*mgmntv1.ListTagsResponse, error) {
	return &mgmntv1.ListTagsResponse{Tags: f.tags}, nil
}

func (f *fixtureMovieTags) CreateTag(_ context.Context, req *mgmntv1.CreateTagRequest) (*mgmntv1.CreateTagResponse, error) {
	f.created = req.GetLabel()
	return &mgmntv1.CreateTagResponse{TagId: "tag-new"}, nil
}

func (f *fixtureMovieTags) DeleteTag(_ context.Context, req *mgmntv1.DeleteTagRequest) (*mgmntv1.DeleteTagResponse, error) {
	f.deleted = req.GetTagId()
	return &mgmntv1.DeleteTagResponse{}, nil
}

func (f *fixtureMovieTags) GetItemTags(_ context.Context, req *mgmntv1.GetItemTagsRequest) (*mgmntv1.GetItemTagsResponse, error) {
	ids := f.itemTags[req.GetItemId()]
	out := make([]*mgmntv1.Tag, 0, len(ids))
	for _, id := range ids {
		out = append(out, &mgmntv1.Tag{Id: id, Label: id})
	}
	return &mgmntv1.GetItemTagsResponse{Tags: out}, nil
}

func (f *fixtureMovieTags) SetItemTags(_ context.Context, req *mgmntv1.SetItemTagsRequest) (*mgmntv1.SetItemTagsResponse, error) {
	f.setOn = req.GetItemId()
	if f.itemTags == nil {
		f.itemTags = map[string][]string{}
	}
	f.itemTags[req.GetItemId()] = append([]string{}, req.GetTagIds()...)
	return &mgmntv1.SetItemTagsResponse{}, nil
}

type fixtureTVTags struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	tags    []*tvmgmtv1.Tag
	created string
	setOn   string
	setIDs  []string
}

func (f *fixtureTVTags) ListTags(context.Context, *tvmgmtv1.ListTagsRequest) (*tvmgmtv1.ListTagsResponse, error) {
	return &tvmgmtv1.ListTagsResponse{Tags: f.tags}, nil
}

func (f *fixtureTVTags) CreateTag(_ context.Context, req *tvmgmtv1.CreateTagRequest) (*tvmgmtv1.CreateTagResponse, error) {
	f.created = req.GetLabel()
	return &tvmgmtv1.CreateTagResponse{TagId: "tv-tag-new"}, nil
}

func (f *fixtureTVTags) GetItemTags(_ context.Context, req *tvmgmtv1.GetItemTagsRequest) (*tvmgmtv1.GetItemTagsResponse, error) {
	return &tvmgmtv1.GetItemTagsResponse{Tags: []*tvmgmtv1.Tag{{Id: "tv1", Label: "anime"}}}, nil
}

func (f *fixtureTVTags) SetItemTags(_ context.Context, req *tvmgmtv1.SetItemTagsRequest) (*tvmgmtv1.SetItemTagsResponse, error) {
	f.setOn = req.GetItemId()
	f.setIDs = append([]string{}, req.GetTagIds()...)
	return &tvmgmtv1.SetItemTagsResponse{}, nil
}

type fixtureMusicTags struct {
	musicv1.UnimplementedMusicManagementServiceServer
	tags     []*musicv1.Tag
	itemTags map[string][]string
	created  string
	deleted  string
	setOn    string
}

func (f *fixtureMusicTags) ListTags(context.Context, *musicv1.ListTagsRequest) (*musicv1.ListTagsResponse, error) {
	return &musicv1.ListTagsResponse{Tags: f.tags}, nil
}

func (f *fixtureMusicTags) CreateTag(_ context.Context, req *musicv1.CreateTagRequest) (*musicv1.CreateTagResponse, error) {
	f.created = req.GetLabel()
	return &musicv1.CreateTagResponse{TagId: "mu-tag-new"}, nil
}

func (f *fixtureMusicTags) DeleteTag(_ context.Context, req *musicv1.DeleteTagRequest) (*musicv1.DeleteTagResponse, error) {
	f.deleted = req.GetTagId()
	return &musicv1.DeleteTagResponse{}, nil
}

func (f *fixtureMusicTags) GetItemTags(_ context.Context, req *musicv1.GetItemTagsRequest) (*musicv1.GetItemTagsResponse, error) {
	ids := f.itemTags[req.GetItemId()]
	out := make([]*musicv1.Tag, 0, len(ids))
	for _, id := range ids {
		out = append(out, &musicv1.Tag{Id: id, Label: id})
	}
	return &musicv1.GetItemTagsResponse{Tags: out}, nil
}

func (f *fixtureMusicTags) SetItemTags(_ context.Context, req *musicv1.SetItemTagsRequest) (*musicv1.SetItemTagsResponse, error) {
	f.setOn = req.GetItemId()
	if f.itemTags == nil {
		f.itemTags = map[string][]string{}
	}
	f.itemTags[req.GetItemId()] = append([]string{}, req.GetTagIds()...)
	return &musicv1.SetItemTagsResponse{}, nil
}

func dialTagFixtures(t *testing.T) (*fixtureMovieTags, *fixtureTVTags, *fixtureMusicTags, *server) {
	t.Helper()
	movies := &fixtureMovieTags{
		tags:     []*mgmntv1.Tag{{Id: "m1", Label: "4K"}},
		itemTags: map[string][]string{"mov1": {"m1"}},
	}
	tv := &fixtureTVTags{tags: []*tvmgmtv1.Tag{{Id: "t1", Label: "anime"}}}
	music := &fixtureMusicTags{
		tags:     []*musicv1.Tag{{Id: "u1", Label: "live"}},
		itemTags: map[string][]string{"ar1": {"u1"}},
	}
	mlis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ulis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ms := grpc.NewServer()
	ts := grpc.NewServer()
	us := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(ms, movies)
	tvmgmtv1.RegisterTvManagementServiceServer(ts, tv)
	musicv1.RegisterMusicManagementServiceServer(us, music)
	go func() { _ = ms.Serve(mlis) }()
	go func() { _ = ts.Serve(tlis) }()
	go func() { _ = us.Serve(ulis) }()
	t.Cleanup(ms.Stop)
	t.Cleanup(ts.Stop)
	t.Cleanup(us.Stop)
	mconn, err := grpc.NewClient(mlis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	tconn, err := grpc.NewClient(tlis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	uconn, err := grpc.NewClient(ulis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mconn.Close(); _ = tconn.Close(); _ = uconn.Close() })
	return movies, tv, music, &server{
		movies:   mgmntv1.NewMovieManagementServiceClient(mconn),
		tv:       tvmgmtv1.NewTvManagementServiceClient(tconn),
		music:    musicv1.NewMusicManagementServiceClient(uconn),
		sessions: newSessionStore(time.Hour),
	}
}

func privilegedTagReq(t *testing.T, s *server, method, path, body string) *http.Request {
	t.Helper()
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.SetPathValue("id", strings.TrimPrefix(strings.TrimPrefix(path, "/api/tags/"), "/api/movies/"))
	if strings.Contains(path, "/api/movies/") {
		req.SetPathValue("id", "mov1")
	}
	if strings.Contains(path, "/api/tv/") {
		req.SetPathValue("id", "show1")
	}
	if strings.Contains(path, "/api/music/") {
		req.SetPathValue("id", "ar1")
	}
	if strings.HasPrefix(path, "/api/tags/") && !strings.Contains(path, "?") {
		req.SetPathValue("id", strings.TrimPrefix(path, "/api/tags/"))
	}
	if strings.Contains(path, "/api/tags/m1") {
		req.SetPathValue("id", "m1")
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return req
}

func TestHandleListTags(t *testing.T) {
	_, _, _, s := dialTagFixtures(t)
	w := httptest.NewRecorder()
	s.handleListTags(w, httptest.NewRequest(http.MethodGet, "/api/tags", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Tags      []struct {
			ID    string `json:"id"`
			Media string `json:"media"`
		} `json:"tags"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Tags) != 3 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateTagMovie(t *testing.T) {
	movies, _, _, s := dialTagFixtures(t)
	req := privilegedTagReq(t, s, http.MethodPost, "/api/tags", `{"label":"kids","media":"movie"}`)
	w := httptest.NewRecorder()
	s.handleCreateTag(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if movies.created != "kids" {
		t.Fatalf("created %q", movies.created)
	}
}

func TestHandleCreateTagTV(t *testing.T) {
	_, tv, _, s := dialTagFixtures(t)
	req := privilegedTagReq(t, s, http.MethodPost, "/api/tags", `{"label":"anime","media":"tv"}`)
	w := httptest.NewRecorder()
	s.handleCreateTag(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if tv.created != "anime" {
		t.Fatalf("created %q", tv.created)
	}
}

func TestHandleCreateTagMusic(t *testing.T) {
	_, _, music, s := dialTagFixtures(t)
	req := privilegedTagReq(t, s, http.MethodPost, "/api/tags", `{"label":"live","media":"artist"}`)
	w := httptest.NewRecorder()
	s.handleCreateTag(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if music.created != "live" {
		t.Fatalf("created %q", music.created)
	}
}

func TestHandleCreateTagForbidden(t *testing.T) {
	_, _, _, s := dialTagFixtures(t)
	w := httptest.NewRecorder()
	s.handleCreateTag(w, httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(`{"label":"x"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleDeleteTag(t *testing.T) {
	movies, _, _, s := dialTagFixtures(t)
	req := privilegedTagReq(t, s, http.MethodDelete, "/api/tags/m1", "")
	w := httptest.NewRecorder()
	s.handleDeleteTag(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if movies.deleted != "m1" {
		t.Fatalf("deleted %q", movies.deleted)
	}
}

func TestHandleGetSetMovieTags(t *testing.T) {
	movies, _, _, s := dialTagFixtures(t)
	get := privilegedTagReq(t, s, http.MethodGet, "/api/movies/mov1/tags", "")
	w := httptest.NewRecorder()
	s.handleGetMovieTags(w, get)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	put := privilegedTagReq(t, s, http.MethodPut, "/api/movies/mov1/tags", `{"tag_ids":["m1","m2"]}`)
	w = httptest.NewRecorder()
	s.handleSetMovieTags(w, put)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if movies.setOn != "mov1" || len(movies.itemTags["mov1"]) != 2 {
		t.Fatalf("%#v", movies.itemTags)
	}
}

func TestHandleSetTVTags(t *testing.T) {
	_, tv, _, s := dialTagFixtures(t)
	req := privilegedTagReq(t, s, http.MethodPut, "/api/tv/show1/tags", `{"tag_ids":["t1"]}`)
	w := httptest.NewRecorder()
	s.handleSetTVTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if tv.setOn != "show1" || tv.setIDs[0] != "t1" {
		t.Fatalf("%q %#v", tv.setOn, tv.setIDs)
	}
}

func TestHandleGetSetMusicTags(t *testing.T) {
	_, _, music, s := dialTagFixtures(t)
	get := privilegedTagReq(t, s, http.MethodGet, "/api/music/ar1/tags", "")
	w := httptest.NewRecorder()
	s.handleGetMusicTags(w, get)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	put := privilegedTagReq(t, s, http.MethodPut, "/api/music/ar1/tags", `{"tag_ids":["u1","u2"]}`)
	w = httptest.NewRecorder()
	s.handleSetMusicTags(w, put)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if music.setOn != "ar1" || len(music.itemTags["ar1"]) != 2 {
		t.Fatalf("%#v", music.itemTags)
	}
}

func TestHandleListTagsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleListTags(w, httptest.NewRequest(http.MethodGet, "/api/tags", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Tags      []any `json:"tags"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Tags == nil {
		t.Fatalf("%#v", body)
	}
}
