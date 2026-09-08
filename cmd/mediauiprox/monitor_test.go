package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureWantedPatch struct {
	automationv1.UnimplementedAutomationServiceServer
	queueID   string
	profile   string
	monitored *bool
}

func (f *fixtureWantedPatch) UpdateQueueItem(_ context.Context, req *automationv1.UpdateQueueItemRequest) (*automationv1.UpdateQueueItemResponse, error) {
	f.queueID = req.GetQueueId()
	f.profile = req.GetQualityProfileId()
	if req.Monitored != nil {
		v := req.GetMonitored()
		f.monitored = &v
	}
	return &automationv1.UpdateQueueItemResponse{
		Item: &automationv1.QueueItem{Id: "w_movie_" + req.GetQueueId(), ItemId: req.GetQueueId(), QualityProfileId: req.GetQualityProfileId()},
	}, nil
}

type fixtureMovieMonitor struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	monitored bool
	profile   string
	root      string
}

func (f *fixtureMovieMonitor) UpdateMovie(_ context.Context, req *mgmntv1.UpdateMovieRequest) (*mgmntv1.UpdateMovieResponse, error) {
	if req.Monitored != nil {
		f.monitored = req.GetMonitored()
	}
	if req.QualityProfileId != nil {
		f.profile = req.GetQualityProfileId()
	}
	if req.RootFolderPath != nil {
		f.root = req.GetRootFolderPath()
	}
	return &mgmntv1.UpdateMovieResponse{Movie: &mgmntv1.MovieItem{
		Id: req.GetMovieId(), Title: "Dune", Monitored: f.monitored, QualityProfileId: f.profile, RootFolderPath: f.root,
	}}, nil
}

type fixtureTVMonitor struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	show, season, episode bool
	root                  string
}

func (f *fixtureTVMonitor) UpdateTVShow(_ context.Context, req *tvmgmtv1.UpdateTVShowRequest) (*tvmgmtv1.UpdateTVShowResponse, error) {
	f.show = req.GetMonitored()
	if req.RootFolderPath != nil {
		f.root = req.GetRootFolderPath()
	}
	return &tvmgmtv1.UpdateTVShowResponse{Series: &tvmgmtv1.TVSeries{Id: req.GetSeriesId(), Name: "Orbital", Monitored: f.show, RootFolderPath: f.root}}, nil
}

func (f *fixtureTVMonitor) UpdateSeasonMonitored(_ context.Context, req *tvmgmtv1.UpdateSeasonMonitoredRequest) (*tvmgmtv1.UpdateSeasonMonitoredResponse, error) {
	f.season = req.GetMonitored()
	return &tvmgmtv1.UpdateSeasonMonitoredResponse{}, nil
}

func (f *fixtureTVMonitor) UpdateEpisodeMonitored(_ context.Context, req *tvmgmtv1.UpdateEpisodeMonitoredRequest) (*tvmgmtv1.UpdateEpisodeMonitoredResponse, error) {
	f.episode = req.GetMonitored()
	return &tvmgmtv1.UpdateEpisodeMonitoredResponse{}, nil
}

func dialMovieMonitor(t *testing.T) (*fixtureMovieMonitor, mgmntv1.MovieManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMovieMonitor{monitored: true}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, mgmntv1.NewMovieManagementServiceClient(conn)
}

func dialTVMonitor(t *testing.T) (*fixtureTVMonitor, tvmgmtv1.TvManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureTVMonitor{show: true, season: true, episode: true}
	srv := grpc.NewServer()
	tvmgmtv1.RegisterTvManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, tvmgmtv1.NewTvManagementServiceClient(conn)
}

func TestHandlePatchMovieUnmonitor(t *testing.T) {
	fake, client := dialMovieMonitor(t)
	s := &server{movies: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/movies/m1", bytes.NewBufferString(`{"monitored":false}`))
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handlePatchMovie(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.monitored {
		t.Fatal("expected unmonitored")
	}
	var body struct {
		Monitored bool `json:"monitored"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Monitored {
		t.Fatalf("%#v", body)
	}
}

func TestHandlePatchTVSeasonAndEpisode(t *testing.T) {
	fake, client := dialTVMonitor(t)
	s := &server{tv: client}

	req := httptest.NewRequest(http.MethodPatch, "/api/tv/s1", bytes.NewBufferString(`{"monitored":false}`))
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handlePatchTV(w, req)
	if w.Code != http.StatusOK || fake.show {
		t.Fatalf("show %d %s mon=%v", w.Code, w.Body.String(), fake.show)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/tv/seasons/sn1", bytes.NewBufferString(`{"monitored":false}`))
	req.SetPathValue("id", "sn1")
	w = httptest.NewRecorder()
	s.handlePatchTVSeason(w, req)
	if w.Code != http.StatusOK || fake.season {
		t.Fatalf("season %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/episodes/e1", bytes.NewBufferString(`{"monitored":false}`))
	req.SetPathValue("id", "e1")
	w = httptest.NewRecorder()
	s.handlePatchEpisode(w, req)
	if w.Code != http.StatusOK || fake.episode {
		t.Fatalf("episode %d %s", w.Code, w.Body.String())
	}
}

func TestHandlePatchMovieQualityProfile(t *testing.T) {
	fake, client := dialMovieMonitor(t)
	s := &server{movies: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/movies/m1", bytes.NewBufferString(`{"quality_profile_id":"qp_uhd"}`))
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handlePatchMovie(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.profile != "qp_uhd" {
		t.Fatalf("profile %q", fake.profile)
	}
}

func TestHandlePatchMovieQualityProfileUpdatesWanted(t *testing.T) {
	_, movies := dialMovieMonitor(t)
	wanted := &fixtureWantedPatch{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	automationv1.RegisterAutomationServiceServer(srv, wanted)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s := &server{movies: movies, automation: automationv1.NewAutomationServiceClient(conn)}
	req := httptest.NewRequest(http.MethodPatch, "/api/movies/m1", bytes.NewBufferString(`{"quality_profile_id":"qp_uhd"}`))
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handlePatchMovie(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if wanted.queueID != "m1" || wanted.profile != "qp_uhd" {
		t.Fatalf("wanted queue=%q profile=%q", wanted.queueID, wanted.profile)
	}
}

func TestHandlePatchMovieRootFolder(t *testing.T) {
	fake, client := dialMovieMonitor(t)
	s := &server{movies: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/movies/m1", bytes.NewBufferString(`{"root_folder_path":"/data/uhd"}`))
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handlePatchMovie(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.root != "/data/uhd" {
		t.Fatalf("root %q", fake.root)
	}
}

func TestHandlePatchTVRootFolder(t *testing.T) {
	fake, client := dialTVMonitor(t)
	s := &server{tv: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/tv/s1", bytes.NewBufferString(`{"root_folder_path":"/data/tv"}`))
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handlePatchTV(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.root != "/data/tv" {
		t.Fatalf("root %q", fake.root)
	}
}

func TestHandlePatchMovieRequiresFlag(t *testing.T) {
	_, client := dialMovieMonitor(t)
	s := &server{movies: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/movies/m1", bytes.NewBufferString(`{}`))
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handlePatchMovie(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}
