package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureTVCalendar struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
}

func (f *fixtureTVCalendar) GetCalendar(_ context.Context, req *tvmgmtv1.GetCalendarRequest) (*tvmgmtv1.GetCalendarResponse, error) {
	if req.GetStartDate() > "2026-09-10" {
		return &tvmgmtv1.GetCalendarResponse{}, nil
	}
	return &tvmgmtv1.GetCalendarResponse{
		Items: []*tvmgmtv1.CalendarItem{{
			EpisodeId: "ep-1", SeriesId: "s-1", SeriesName: "Orbital",
			SeasonNumber: 1, EpisodeNumber: 1, EpisodeName: "Pilot",
			AirDate: "2026-09-09", Monitored: true,
		}},
	}, nil
}

func dialTVCalendar(t *testing.T) tvmgmtv1.TvManagementServiceClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	tvmgmtv1.RegisterTvManagementServiceServer(srv, &fixtureTVCalendar{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return tvmgmtv1.NewTvManagementServiceClient(conn)
}

func TestCalendarMergesTVAndMovies(t *testing.T) {
	movies := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/calendar" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []movieCalendarJSON{{
				ID: "mv-1", ParentID: "mv-1", Title: "Upcoming Film",
				Subtitle: "Theatrical / digital", Date: "2026-09-12", Monitored: true, Year: 2026,
			}},
		})
	}))
	t.Cleanup(movies.Close)
	u, _ := url.Parse(movies.URL)
	s := &server{tv: dialTVCalendar(t), moviesHTTP: u}
	w := httptest.NewRecorder()
	s.handleCalendar(w, httptest.NewRequest(http.MethodGet, "/api/calendar?start=2026-09-01&end=2026-09-30", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool
		Items     []calendarItemJSON
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 2 {
		t.Fatalf("%#v", body)
	}
	if body.Items[0].Kind != "tv" || body.Items[0].Title != "Orbital" {
		t.Fatalf("want TV first, got %#v", body.Items[0])
	}
	if body.Items[1].Kind != "movie" || body.Items[1].Href != "/movies/mv-1" {
		t.Fatalf("want movie second, got %#v", body.Items[1])
	}
}

func TestCalendarUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleCalendar(w, httptest.NewRequest(http.MethodGet, "/api/calendar", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
		Total     int  `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Total != 0 {
		t.Fatalf("%#v", body)
	}
}
