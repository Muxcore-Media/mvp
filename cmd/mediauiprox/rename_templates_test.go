package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureRenameTemplates struct {
	renamev1.UnimplementedRenameServiceServer
	listedType string
	created    *renamev1.CreateTemplateRequest
	updated    *renamev1.UpdateTemplateRequest
	deletedID  string
}

func (f *fixtureRenameTemplates) ListTemplates(_ context.Context, req *renamev1.ListTemplatesRequest) (*renamev1.ListTemplatesResponse, error) {
	f.listedType = req.GetMediaType()
	return &renamev1.ListTemplatesResponse{Templates: []*renamev1.NamingTemplate{{
		Id: "movie_tpl", Name: "Default Movie", MediaType: "movie",
		Pattern: "{Title} ({Year})/{Title} ({Year}) [{Quality}]", IsDefault: true,
	}}}, nil
}

func (f *fixtureRenameTemplates) CreateTemplate(_ context.Context, req *renamev1.CreateTemplateRequest) (*renamev1.CreateTemplateResponse, error) {
	f.created = req
	return &renamev1.CreateTemplateResponse{Template: &renamev1.NamingTemplate{
		Id: "tpl-new", Name: req.GetName(), MediaType: req.GetMediaType(),
		Pattern: req.GetPattern(), IsDefault: req.GetIsDefault(),
	}}, nil
}

func (f *fixtureRenameTemplates) UpdateTemplate(_ context.Context, req *renamev1.UpdateTemplateRequest) (*renamev1.UpdateTemplateResponse, error) {
	f.updated = req
	return &renamev1.UpdateTemplateResponse{Template: &renamev1.NamingTemplate{
		Id: req.GetId(), Name: req.GetName(), MediaType: "movie",
		Pattern: req.GetPattern(), IsDefault: req.GetIsDefault(),
	}}, nil
}

func (f *fixtureRenameTemplates) DeleteTemplate(_ context.Context, req *renamev1.DeleteTemplateRequest) (*renamev1.DeleteTemplateResponse, error) {
	f.deletedID = req.GetId()
	return &renamev1.DeleteTemplateResponse{}, nil
}

func dialRenameTemplates(t *testing.T) (*fixtureRenameTemplates, renamev1.RenameServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureRenameTemplates{}
	srv := grpc.NewServer()
	renamev1.RegisterRenameServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, renamev1.NewRenameServiceClient(conn)
}

func privilegedRenameRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureRenameTemplates) {
	t.Helper()
	fake, client := dialRenameTemplates(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{rename: client, sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleListRenameTemplatesForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListRenameTemplates(w, httptest.NewRequest(http.MethodGet, "/api/rename/templates", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListRenameTemplatesUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/rename/templates", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListRenameTemplates(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Templates []any `json:"templates"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Templates == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListRenameTemplates(t *testing.T) {
	s, req, fake := privilegedRenameRequest(t, http.MethodGet, "/api/rename/templates?media_type=movie", "")
	w := httptest.NewRecorder()
	s.handleListRenameTemplates(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.listedType != "movie" {
		t.Fatalf("listed %q", fake.listedType)
	}
	var body struct {
		Available bool `json:"available"`
		Templates []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"is_default"`
		} `json:"templates"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Templates[0].ID != "movie_tpl" || !body.Templates[0].IsDefault {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateRenameTemplate(t *testing.T) {
	s, req, fake := privilegedRenameRequest(t, http.MethodPost, "/api/rename/templates", `{"name":"UHD","media_type":"tv","pattern":"{Title}/{Title}","is_default":true}`)
	w := httptest.NewRecorder()
	s.handleCreateRenameTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.created == nil || fake.created.GetName() != "UHD" || fake.created.GetMediaType() != "tv" || !fake.created.GetIsDefault() {
		t.Fatalf("created %#v", fake.created)
	}
	var body struct {
		Template struct {
			ID string `json:"id"`
		} `json:"template"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Template.ID != "tpl-new" {
		t.Fatalf("%#v", body)
	}
}

func TestHandlePatchRenameTemplate(t *testing.T) {
	s, req, fake := privilegedRenameRequest(t, http.MethodPatch, "/api/rename/templates/movie_tpl", `{"name":"Movies","pattern":"{Title} ({Year})","is_default":true}`)
	req.SetPathValue("id", "movie_tpl")
	w := httptest.NewRecorder()
	s.handlePatchRenameTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.updated == nil || fake.updated.GetId() != "movie_tpl" || fake.updated.GetPattern() != "{Title} ({Year})" {
		t.Fatalf("updated %#v", fake.updated)
	}
}

func TestHandleDeleteRenameTemplate(t *testing.T) {
	s, req, fake := privilegedRenameRequest(t, http.MethodDelete, "/api/rename/templates/movie_dot", "")
	req.SetPathValue("id", "movie_dot")
	w := httptest.NewRecorder()
	s.handleDeleteRenameTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deletedID != "movie_dot" {
		t.Fatalf("deleted %q", fake.deletedID)
	}
}
