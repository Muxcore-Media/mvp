package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	indexerv1 "github.com/Muxcore-Media/contracts-indexer/muxcore/indexer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureIndexer struct {
	indexerv1.UnimplementedIndexerServiceServer
	listed []*indexerv1.IndexerInfo
	caps   *indexerv1.GetCapabilitiesResponse
	err    error
}

func (f *fixtureIndexer) ListIndexers(_ context.Context, _ *indexerv1.ListIndexersRequest) (*indexerv1.ListIndexersResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &indexerv1.ListIndexersResponse{Indexers: f.listed}, nil
}

func (f *fixtureIndexer) GetCapabilities(context.Context, *indexerv1.GetCapabilitiesRequest) (*indexerv1.GetCapabilitiesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.caps == nil {
		return &indexerv1.GetCapabilitiesResponse{}, nil
	}
	return f.caps, nil
}

func indexerServer(t *testing.T, fake *fixtureIndexer) indexerv1.IndexerServiceClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	indexerv1.RegisterIndexerServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return indexerv1.NewIndexerServiceClient(conn)
}

func TestHandleListIndexersUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleListIndexers(w, httptest.NewRequest(http.MethodGet, "/api/indexers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Indexers  []any `json:"indexers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Indexers == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListIndexersLive(t *testing.T) {
	s := &server{indexer: indexerServer(t, &fixtureIndexer{listed: []*indexerv1.IndexerInfo{
		{Id: 3, Name: "Knaben", Protocol: "torrent", Language: "en", Configured: true},
	}})}
	w := httptest.NewRecorder()
	s.handleListIndexers(w, httptest.NewRequest(http.MethodGet, "/api/indexers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Indexers  []struct {
			ID         int32  `json:"id"`
			Name       string `json:"name"`
			Configured bool   `json:"configured"`
		} `json:"indexers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Indexers) != 1 || body.Indexers[0].Name != "Knaben" || !body.Indexers[0].Configured {
		t.Fatalf("%#v", body)
	}
}

func TestAcquisitionCountsTorznabIndexer(t *testing.T) {
	s := &server{indexer: indexerServer(t, &fixtureIndexer{listed: []*indexerv1.IndexerInfo{
		{Id: 3, Name: "Knaben", Protocol: "torrent", Configured: true},
	}})}
	w := httptest.NewRecorder()
	s.handleAcquisition(w, httptest.NewRequest(http.MethodGet, "/api/acquisition", nil))
	var body struct {
		HasIndexer bool `json:"hasIndexer"`
		Indexers   []struct {
			Name string `json:"name"`
		} `json:"indexers"`
		Peers []struct {
			ID   string `json:"id"`
			Live bool   `json:"live"`
		} `json:"peers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.HasIndexer || len(body.Indexers) != 1 || body.Indexers[0].Name != "Knaben" {
		t.Fatalf("%#v", body)
	}
	found := false
	for _, p := range body.Peers {
		if p.ID == "indexer-torznab" && p.Live {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected live Prowlarr peer, got %#v", body.Peers)
	}
}

func TestAcquisitionIncludesIndexerCapabilities(t *testing.T) {
	s := &server{indexer: indexerServer(t, &fixtureIndexer{
		listed: []*indexerv1.IndexerInfo{{Id: 3, Name: "Knaben", Protocol: "torrent", Configured: true}},
		caps: &indexerv1.GetCapabilitiesResponse{
			SupportsSearch: true, SupportsMovieSearch: true, SupportsTvSearch: true,
			SupportsIdSearch: true, SupportsSeasonPack: true,
			SupportedProtocols: []string{"torrent", "usenet"},
		},
	})}
	w := httptest.NewRecorder()
	s.handleAcquisition(w, httptest.NewRequest(http.MethodGet, "/api/acquisition", nil))
	var body struct {
		CapabilitiesAvailable bool `json:"capabilities_available"`
		Capabilities          struct {
			SupportsMovieSearch bool     `json:"supports_movie_search"`
			SupportsIdSearch    bool     `json:"supports_id_search"`
			SupportsSeasonPack  bool     `json:"supports_season_pack"`
			SupportedProtocols  []string `json:"supported_protocols"`
		} `json:"capabilities"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.CapabilitiesAvailable || !body.Capabilities.SupportsMovieSearch || !body.Capabilities.SupportsIdSearch || !body.Capabilities.SupportsSeasonPack {
		t.Fatalf("%#v", body)
	}
	if len(body.Capabilities.SupportedProtocols) != 2 {
		t.Fatalf("protocols %#v", body.Capabilities.SupportedProtocols)
	}
}
