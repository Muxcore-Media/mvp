package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureSubtitleSearch struct {
	subtv1.UnimplementedSubtitleServiceServer
	query    string
	fileID   string
	language string
}

func (f *fixtureSubtitleSearch) Search(_ context.Context, req *subtv1.SearchRequest) (*subtv1.SearchResponse, error) {
	f.query = req.GetQuery()
	return &subtv1.SearchResponse{Results: []*subtv1.SubtitleCandidate{{
		FileId: "os-1", Language: "en", Format: "srt", ReleaseName: "Interstellar.2014.1080p",
		Source: "opensubtitles", Downloads: 42,
	}}}, nil
}

func (f *fixtureSubtitleSearch) DownloadSubtitle(_ context.Context, req *subtv1.DownloadSubtitleRequest) (*subtv1.DownloadSubtitleResponse, error) {
	f.fileID = req.GetFileId()
	f.language = req.GetLanguage()
	return &subtv1.DownloadSubtitleResponse{Subtitle: &subtv1.SubtitleFile{
		Id: "sub_9", Language: "en", Source: "opensubtitles",
	}}, nil
}

func dialSubtitleSearch(t *testing.T) (*fixtureSubtitleSearch, subtv1.SubtitleServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureSubtitleSearch{}
	srv := grpc.NewServer()
	subtv1.RegisterSubtitleServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, subtv1.NewSubtitleServiceClient(conn)
}

func TestHandleSubtitleSearchUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleSubtitleSearch(w, httptest.NewRequest(http.MethodGet, "/api/subtitles/search?title=Interstellar&language=en", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Results   []any `json:"results"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Results == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleSubtitleSearchLive(t *testing.T) {
	fake, client := dialSubtitleSearch(t)
	s := &server{subtitles: client}
	w := httptest.NewRecorder()
	s.handleSubtitleSearch(w, httptest.NewRequest(http.MethodGet, "/api/subtitles/search?title=Interstellar&year=2014&language=en", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.query != "Interstellar 2014" {
		t.Fatalf("query %q", fake.query)
	}
	var body struct {
		Available bool `json:"available"`
		Results   []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Title    string `json:"title"`
		} `json:"results"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Results[0].ID != "os-1" || body.Results[0].Provider != "opensubtitles" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleSubtitleDownload(t *testing.T) {
	fake, client := dialSubtitleSearch(t)
	s := &server{subtitles: client}
	w := httptest.NewRecorder()
	s.handleSubtitleDownload(w, httptest.NewRequest(http.MethodPost, "/api/subtitles/download", strings.NewReader(`{"id":"os-1","provider":"opensubtitles","language":"en"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.fileID != "os-1" {
		t.Fatalf("file %q", fake.fileID)
	}
	var body struct {
		TrackURL string `json:"track_url"`
		Language string `json:"language"`
		Label    string `json:"label"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.TrackURL != "/api/playback/subtitles/sub_9" || body.Language != "en" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleSubtitleDownloadUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleSubtitleDownload(w, httptest.NewRequest(http.MethodPost, "/api/subtitles/download", strings.NewReader(`{"id":"os-1"}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}
