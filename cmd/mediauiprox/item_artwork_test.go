package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureItemArtwork struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
	itemID   string
	replaced string
	kind     mediaadminv1.ArtworkType
}

func (f *fixtureItemArtwork) ReplaceArtwork(stream mediaadminv1.MediaAdminService_ReplaceArtworkServer) error {
	var itemID string
	var kind mediaadminv1.ArtworkType
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if msg.GetItemId() != "" {
			itemID = msg.GetItemId()
		}
		if msg.GetArtworkType() != mediaadminv1.ArtworkType_ARTWORK_TYPE_UNSPECIFIED {
			kind = msg.GetArtworkType()
		}
	}
	f.replaced = itemID
	f.kind = kind
	return stream.SendAndClose(&mediaadminv1.ReplaceArtworkResponse{
		Artwork: &mediaadminv1.ArtworkInfo{
			Id: itemID + "_poster", ItemId: itemID,
			Type: kind, Url: "http://127.0.0.1:18080/images/" + itemID + "/poster.jpg",
		},
	})
}

func (f *fixtureItemArtwork) ListArtwork(_ context.Context, req *mediaadminv1.ListArtworkRequest) (*mediaadminv1.ListArtworkResponse, error) {
	f.itemID = req.GetId()
	return &mediaadminv1.ListArtworkResponse{
		Artwork: []*mediaadminv1.ArtworkInfo{{
			Id: req.GetId() + "_poster", ItemId: req.GetId(),
			Type: mediaadminv1.ArtworkType_ARTWORK_TYPE_POSTER,
			Url:  "http://127.0.0.1:18080/images/" + req.GetId() + "/poster.jpg",
		}},
	}, nil
}

func dialItemArtwork(t *testing.T) (*fixtureItemArtwork, mediaadminv1.MediaAdminServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureItemArtwork{}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, mediaadminv1.NewMediaAdminServiceClient(conn)
}

func TestHouseholdArtworkURL(t *testing.T) {
	got := householdArtworkURL("movies", "http://127.0.0.1:9/images/m1/poster.jpg")
	if got != "/images/movies/m1/poster.jpg" {
		t.Fatalf("got %q", got)
	}
	got = householdArtworkURL("tv", "/images/tv/s1/fanart.jpg")
	if got != "/images/tv/s1/fanart.jpg" {
		t.Fatalf("got %q", got)
	}
}

func TestHandleListMovieArtworkUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/artwork", nil)
	req.SetPathValue("id", "m1")
	s.handleListMovieArtwork(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["available"] != false {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleListMovieArtwork(t *testing.T) {
	fake, client := dialItemArtwork(t)
	s := &server{moviesAdmin: client}
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/artwork", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleListMovieArtwork(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.itemID != "m1" {
		t.Fatalf("item=%q", fake.itemID)
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 || body.Items[0].Type != "poster" || body.Items[0].URL != "/images/movies/m1/poster.jpg" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListTVArtwork(t *testing.T) {
	fake, client := dialItemArtwork(t)
	s := &server{tvAdmin: client}
	req := httptest.NewRequest(http.MethodGet, "/api/tv/s1/artwork", nil)
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleListTVArtwork(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.itemID != "s1" {
		t.Fatalf("item=%q", fake.itemID)
	}
}

func TestHandleReplaceMovieArtwork(t *testing.T) {
	fake, client := dialItemArtwork(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{moviesAdmin: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/movies/m1/artwork", strings.NewReader(`{"type":"poster","filename":"poster.jpg","data":"iVBORw0KGgo="}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleReplaceMovieArtwork(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.replaced != "m1" || fake.kind != mediaadminv1.ArtworkType_ARTWORK_TYPE_POSTER {
		t.Fatalf("replaced=%q kind=%v", fake.replaced, fake.kind)
	}
	var body struct {
		OK      bool `json:"ok"`
		Artwork struct {
			URL string `json:"url"`
		} `json:"artwork"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Artwork.URL != "/images/movies/m1/poster.jpg" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleReplaceMovieArtworkForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleReplaceMovieArtwork(w, httptest.NewRequest(http.MethodPost, "/api/movies/m1/artwork", strings.NewReader(`{}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}
