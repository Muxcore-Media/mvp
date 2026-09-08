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

	introoutrov1 "github.com/Muxcore-Media/media-intro-outro/proto/gen/muxcore/introoutro/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureIntroOutro struct {
	introoutrov1.UnimplementedIntroOutroServiceServer
	setReq    *introoutrov1.SetSegmentsRequest
	deletedID string
	mediaIDs  []string
}

func (f *fixtureIntroOutro) ListMedia(context.Context, *introoutrov1.ListMediaRequest) (*introoutrov1.ListMediaResponse, error) {
	return &introoutrov1.ListMediaResponse{MediaIds: f.mediaIDs}, nil
}

func (f *fixtureIntroOutro) SetSegments(_ context.Context, req *introoutrov1.SetSegmentsRequest) (*introoutrov1.SetSegmentsResponse, error) {
	f.setReq = req
	return &introoutrov1.SetSegmentsResponse{MediaId: req.GetMediaId(), Segments: req.GetSegments()}, nil
}

func (f *fixtureIntroOutro) DeleteSegments(_ context.Context, req *introoutrov1.DeleteSegmentsRequest) (*introoutrov1.DeleteSegmentsResponse, error) {
	f.deletedID = req.GetMediaId()
	return &introoutrov1.DeleteSegmentsResponse{Success: true}, nil
}

func dialIntroOutro(t *testing.T) (*fixtureIntroOutro, introoutrov1.IntroOutroServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureIntroOutro{}
	srv := grpc.NewServer()
	introoutrov1.RegisterIntroOutroServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, introoutrov1.NewIntroOutroServiceClient(conn)
}

func privilegedSegmentRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureIntroOutro) {
	t.Helper()
	fake, client := dialIntroOutro(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{introOutro: client, sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandlePutPlaybackSegmentsForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handlePutPlaybackSegments(w, httptest.NewRequest(http.MethodPut, "/api/playback/segments", strings.NewReader(
		`{"media_id":"m1","segments":[{"kind":"intro","start_seconds":0,"end_seconds":80}]}`,
	)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlePutPlaybackSegmentsUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodPut, "/api/playback/segments", strings.NewReader(
		`{"media_id":"m1","segments":[{"kind":"intro","start_seconds":0,"end_seconds":80}]}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handlePutPlaybackSegments(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlePutPlaybackSegmentsMissingMediaID(t *testing.T) {
	s, req, _ := privilegedSegmentRequest(t, http.MethodPut, "/api/playback/segments", `{"segments":[]}`)
	w := httptest.NewRecorder()
	s.handlePutPlaybackSegments(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlePutPlaybackSegmentsSetsManualIntro(t *testing.T) {
	s, req, fake := privilegedSegmentRequest(t, http.MethodPut, "/api/playback/segments",
		`{"media_id":"m1","segments":[{"kind":"intro","start_seconds":0,"end_seconds":85,"confidence":1,"source":"manual"}]}`)
	w := httptest.NewRecorder()
	s.handlePutPlaybackSegments(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if fake.setReq == nil || fake.setReq.GetMediaId() != "m1" || len(fake.setReq.GetSegments()) != 1 {
		t.Fatalf("setReq=%+v", fake.setReq)
	}
	seg := fake.setReq.GetSegments()[0]
	if seg.GetKind() != "intro" || seg.GetEndSeconds() != 85 {
		t.Fatalf("%+v", seg)
	}
	var out playbackSegmentsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || out.MediaID != "m1" || len(out.Segments) != 1 || out.Segments[0].Kind != "intro" {
		t.Fatalf("%+v", out)
	}
}

func TestHandleDeletePlaybackSegmentsForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleDeletePlaybackSegments(w, httptest.NewRequest(http.MethodDelete, "/api/playback/segments?media_id=m1", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleDeletePlaybackSegmentsClears(t *testing.T) {
	s, req, fake := privilegedSegmentRequest(t, http.MethodDelete, "/api/playback/segments?media_id=ep1", "")
	w := httptest.NewRecorder()
	s.handleDeletePlaybackSegments(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if fake.deletedID != "ep1" {
		t.Fatalf("deleted %q", fake.deletedID)
	}
	var out playbackSegmentsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || out.MediaID != "ep1" || len(out.Segments) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestHandleListPlaybackSegmentMediaUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleListPlaybackSegmentMedia(w, httptest.NewRequest(http.MethodGet, "/api/playback/segments/media", nil))
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

func TestHandleListPlaybackSegmentMedia(t *testing.T) {
	s, req, fake := privilegedSegmentRequest(t, http.MethodGet, "/api/playback/segments/media", "")
	fake.mediaIDs = []string{"m1", " ep1 ", ""}
	w := httptest.NewRecorder()
	s.handleListPlaybackSegmentMedia(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Total     int  `json:"total"`
		Items     []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Total != 2 || body.Items[0].ID != "m1" || body.Items[1].ID != "ep1" {
		t.Fatalf("%#v", body)
	}
}
