package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureScannerImport struct {
	scannerv1.UnimplementedScannerServiceServer
	imported []string
}

func (f *fixtureScannerImport) ListImportCandidates(_ context.Context, _ *scannerv1.ListImportCandidatesRequest) (*scannerv1.ListImportCandidatesResponse, error) {
	return &scannerv1.ListImportCandidatesResponse{
		Candidates: []*scannerv1.ImportCandidate{
			{
				Path: "/downloads/Dune.2021.mkv", Name: "Dune.2021.mkv",
				Title: "Dune", Year: 2021, MediaType: "movie", Size: 1_000_000,
			},
			{
				Path: "/downloads/Severance.S01E01.mkv", Name: "Severance.S01E01.mkv",
				Title: "Severance", MediaType: "tv", SeasonNumber: 1, EpisodeNumber: 1, Size: 2_000_000,
			},
		},
	}, nil
}

type fixtureTVLookup struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	title  string
	season int32
	ep     int32
}

func (f *fixtureTVLookup) LookupEpisode(_ context.Context, req *tvmgmtv1.LookupEpisodeRequest) (*tvmgmtv1.LookupEpisodeResponse, error) {
	f.title = req.GetTitle()
	f.season = req.GetSeasonNumber()
	f.ep = req.GetEpisodeNumber()
	if req.GetTitle() != "Severance" {
		return &tvmgmtv1.LookupEpisodeResponse{Found: false}, nil
	}
	return &tvmgmtv1.LookupEpisodeResponse{
		Found: true, SeriesId: "show1", SeriesName: "Severance",
		EpisodeTitle: "Good News About Hell", SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2022-02-18",
	}, nil
}

func dialTVLookup(t *testing.T) (*fixtureTVLookup, tvmgmtv1.TvManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureTVLookup{}
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

func (f *fixtureScannerImport) ImportPath(_ context.Context, req *scannerv1.ImportPathRequest) (*scannerv1.ImportPathResponse, error) {
	f.imported = append(f.imported, req.GetPath())
	return &scannerv1.ImportPathResponse{FilesFound: 1, FilesImported: 1}, nil
}

func dialScannerImport(t *testing.T) (*fixtureScannerImport, scannerv1.ScannerServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureScannerImport{}
	srv := grpc.NewServer()
	scannerv1.RegisterScannerServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, scannerv1.NewScannerServiceClient(conn)
}

func TestHandleImportCandidatesUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleImportCandidates(w, httptest.NewRequest(http.MethodGet, "/api/import/candidates", nil))
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
		t.Fatalf("unavailable %#v", body)
	}
}

func TestHandleImportCandidatesLive(t *testing.T) {
	_, client := dialScannerImport(t)
	s := &server{scanner: client}
	w := httptest.NewRecorder()
	s.handleImportCandidates(w, httptest.NewRequest(http.MethodGet, "/api/import/candidates", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title string `json:"title"`
			Path  string `json:"path"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Dune" || body.Items[0].Path != "/downloads/Dune.2021.mkv" {
		t.Fatalf("candidates %#v", body)
	}
}

func TestHandleImportCandidatesMatchesLibraryEpisode(t *testing.T) {
	_, scanner := dialScannerImport(t)
	fakeTV, tv := dialTVLookup(t)
	s := &server{scanner: scanner, tv: tv}
	w := httptest.NewRecorder()
	s.handleImportCandidates(w, httptest.NewRequest(http.MethodGet, "/api/import/candidates", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			Title        string `json:"title"`
			Matched      bool   `json:"matched"`
			SeriesName   string `json:"series_name"`
			EpisodeTitle string `json:"episode_title"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if fakeTV.title != "Severance" || fakeTV.season != 1 || fakeTV.ep != 1 {
		t.Fatalf("lookup title=%q S%dE%d", fakeTV.title, fakeTV.season, fakeTV.ep)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items %#v", body.Items)
	}
	if body.Items[0].Matched {
		t.Fatalf("movie should not call lookup %#v", body.Items[0])
	}
	if !body.Items[1].Matched || body.Items[1].SeriesName != "Severance" || body.Items[1].EpisodeTitle != "Good News About Hell" {
		t.Fatalf("tv match %#v", body.Items[1])
	}
}

func TestHandleImportCandidatesParsesQuality(t *testing.T) {
	_, scanner := dialScannerImport(t)
	fake := &stubFormatsClient{}
	s := &server{scanner: scanner, formats: fake}
	w := httptest.NewRecorder()
	s.handleImportCandidates(w, httptest.NewRequest(http.MethodGet, "/api/import/candidates", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			Title   string `json:"title"`
			Quality struct {
				Label string `json:"label"`
			} `json:"quality"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) == 0 || body.Items[0].Quality.Label != "1080p Remux" {
		t.Fatalf("quality %#v", body.Items)
	}
	if fake.parsedTitle == "" {
		t.Fatal("expected ParseQuality on the filename")
	}
}

func TestHandleImportPath(t *testing.T) {
	fake, client := dialScannerImport(t)
	s := &server{scanner: client}
	w := httptest.NewRecorder()
	s.handleImportPath(w, httptest.NewRequest(http.MethodPost, "/api/import", bytes.NewBufferString(`{"path":"/downloads/Dune.2021.mkv"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Imported int32  `json:"imported"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Imported != 1 || len(fake.imported) != 1 {
		t.Fatalf("import %#v fake=%v", body, fake.imported)
	}
}

func TestHandleImportPathRequiresPath(t *testing.T) {
	_, client := dialScannerImport(t)
	s := &server{scanner: client}
	w := httptest.NewRecorder()
	s.handleImportPath(w, httptest.NewRequest(http.MethodPost, "/api/import", bytes.NewBufferString(`{}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}
