package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type fixturePlexStop struct {
	plexv1.UnimplementedPlexBridgeServiceServer
	sessionID string
	reason    string
}

func (f *fixturePlexStop) TerminateSession(_ context.Context, req *plexv1.TerminateSessionRequest) (*plexv1.TerminateSessionResponse, error) {
	f.sessionID = req.GetSessionId()
	f.reason = req.GetReason()
	return &plexv1.TerminateSessionResponse{Ok: true}, nil
}

func dialPlexStop(t *testing.T) (*fixturePlexStop, plexv1.PlexBridgeServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixturePlexStop{}
	srv := grpc.NewServer()
	plexv1.RegisterPlexBridgeServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, plexv1.NewPlexBridgeServiceClient(conn)
}

type fixtureJellyfinSessions struct {
	jellyfinv1.UnimplementedJellyfinBridgeServer
	stoppedID string
}

func (f *fixtureJellyfinSessions) ListSessions(context.Context, *jellyfinv1.ListSessionsRequest) (*jellyfinv1.ListSessionsResponse, error) {
	return &jellyfinv1.ListSessionsResponse{
		Sessions: []*jellyfinv1.Session{{
			Id: "jf-1", UserId: "u1", UserName: "pat", ItemTitle: "Arrival",
			PositionSeconds: 90, Device: "Living room", Client: "Jellyfin Android",
		}},
	}, nil
}

func (f *fixtureJellyfinSessions) TerminateSession(_ context.Context, req *jellyfinv1.TerminateSessionRequest) (*jellyfinv1.TerminateSessionResponse, error) {
	f.stoppedID = req.GetSessionId()
	return &jellyfinv1.TerminateSessionResponse{Ok: true}, nil
}

func dialJellyfinSessions(t *testing.T) (*fixtureJellyfinSessions, jellyfinv1.JellyfinBridgeClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureJellyfinSessions{}
	srv := grpc.NewServer()
	jellyfinv1.RegisterJellyfinBridgeServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, jellyfinv1.NewJellyfinBridgeClient(conn)
}

func TestHandlePlaybackSessionsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Total     int   `json:"total"`
		Items     []any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Total != 0 || body.Items == nil {
		t.Fatalf("unavailable %#v", body)
	}
}

func TestHandlePlaybackSessionsLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/active" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sessions": []map[string]any{
				{
					"ID":              "sess-1",
					"Title":           "Dune",
					"UserName":        "sam",
					"MuxcoreID":       "m1",
					"MediaType":       "movie",
					"Player":          "MuxCore",
					"State":           "playing",
					"IsTranscode":     false,
					"PositionSeconds": 120,
					"DurationSeconds": 9000,
				},
			},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handlePlaybackSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title string `json:"title"`
			Href  string `json:"href"`
			User  string `json:"user"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Dune" || body.Items[0].Href != "/movies/m1" || body.Items[0].User != "sam" {
		t.Fatalf("sessions %#v", body)
	}
}

func TestHandlePlaybackSessionsJellyfinBridge(t *testing.T) {
	_, client := dialJellyfinSessions(t)
	s := &server{jellyfin: client}
	w := httptest.NewRecorder()
	s.handlePlaybackSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title      string `json:"title"`
			User       string `json:"user"`
			ServerType string `json:"serverType"`
			ID         string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Arrival" || body.Items[0].User != "pat" || body.Items[0].ServerType != "jellyfin" || body.Items[0].ID != "jf-1" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleStopPlaybackSession(t *testing.T) {
	var gotPath, gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stopped": true,
			"session": map[string]any{"ID": "sess-1", "ServerType": "native", "ExternalSessionID": "web-1"},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/stop", nil)
	req.SetPathValue("id", "sess-1")
	w := httptest.NewRecorder()
	s.handleStopPlaybackSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotPath != "/sessions/sess-1/stop" || gotAuth != "Bearer house-token" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	var body struct {
		Stopped    bool   `json:"stopped"`
		ServerType string `json:"serverType"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Stopped || body.ServerType != "native" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleStopPlaybackSessionJellyfinOnly(t *testing.T) {
	fake, client := dialJellyfinSessions(t)
	s := &server{jellyfin: client}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/jf-1/stop", nil)
	req.SetPathValue("id", "jf-1")
	w := httptest.NewRecorder()
	s.handleStopPlaybackSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.stoppedID != "jf-1" {
		t.Fatalf("stopped %q", fake.stoppedID)
	}
	var body struct {
		Stopped         bool   `json:"stopped"`
		ServerType      string `json:"serverType"`
		JellyfinStopped bool   `json:"jellyfinStopped"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Stopped || body.ServerType != "jellyfin" || !body.JellyfinStopped {
		t.Fatalf("%#v", body)
	}
}

func TestHandleStopPlaybackSessionPlex(t *testing.T) {
	fake, client := dialPlexStop(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stopped": true,
			"session": map[string]any{"ID": "sess-plex", "ServerType": "plex", "ExternalSessionID": "plex-42"},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token", plex: client}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-plex/stop", nil)
	req.SetPathValue("id", "sess-plex")
	w := httptest.NewRecorder()
	s.handleStopPlaybackSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.sessionID != "plex-42" || fake.reason != "household stop" {
		t.Fatalf("plex %#v", fake)
	}
	var body struct {
		Stopped     bool   `json:"stopped"`
		ServerType  string `json:"serverType"`
		PlexStopped bool   `json:"plexStopped"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Stopped || body.ServerType != "plex" || !body.PlexStopped {
		t.Fatalf("%#v", body)
	}
}

type stopJellyfinClient struct {
	jellyfinv1.JellyfinBridgeClient
	terminate func(context.Context, *jellyfinv1.TerminateSessionRequest) (*jellyfinv1.TerminateSessionResponse, error)
}

func (c stopJellyfinClient) TerminateSession(ctx context.Context, req *jellyfinv1.TerminateSessionRequest, _ ...grpc.CallOption) (*jellyfinv1.TerminateSessionResponse, error) {
	return c.terminate(ctx, req)
}

type stopPlexClient struct {
	plexv1.PlexBridgeServiceClient
	terminate func(context.Context, *plexv1.TerminateSessionRequest) (*plexv1.TerminateSessionResponse, error)
}

func (c stopPlexClient) TerminateSession(ctx context.Context, req *plexv1.TerminateSessionRequest, _ ...grpc.CallOption) (*plexv1.TerminateSessionResponse, error) {
	return c.terminate(ctx, req)
}

// FR-ANA-003: a successful Stop response needs an acknowledgment under the
// existing monitor/bridge boundary. It does not prove physical stream termination.
func TestHandleStopPlaybackSessionAcknowledgment(t *testing.T) {
	const (
		absent = ""
		ack    = "ack"
		noAck  = "false"
		rpcErr = "rpc_error"
	)
	const bridgeDetail = "private bridge diagnostic must not reach the response"
	tests := []struct {
		name          string
		jellyfin      string
		plex          string
		monitorStatus int
		monitorBody   string
		invalidID     bool
		wantStatus    int
		wantCode      string
		wantType      string
		wantJellyfin  bool
		wantPlex      bool
		wantCalls     string
		wantBridgeID  string
	}{
		{name: "jellyfin_rpc_error_without_monitor", jellyfin: rpcErr, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "jellyfin"},
		{name: "jellyfin_false_without_monitor", jellyfin: noAck, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "jellyfin"},
		{name: "plex_rpc_error_without_monitor", plex: rpcErr, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "plex"},
		{name: "plex_false_without_monitor", plex: noAck, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "plex"},
		{name: "both_rpc_errors_without_monitor", jellyfin: rpcErr, plex: rpcErr, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "jellyfin,plex"},
		{name: "both_false_without_monitor", jellyfin: noAck, plex: noAck, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "jellyfin,plex"},
		{name: "jellyfin_error_plex_false_without_monitor", jellyfin: rpcErr, plex: noAck, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "jellyfin,plex"},
		{name: "jellyfin_false_plex_error_without_monitor", jellyfin: noAck, plex: rpcErr, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "jellyfin,plex"},
		{name: "jellyfin_ack_without_monitor", jellyfin: ack, wantStatus: 200, wantType: "jellyfin", wantJellyfin: true, wantCalls: "jellyfin"},
		{name: "plex_ack_without_monitor", plex: ack, wantStatus: 200, wantType: "plex", wantPlex: true, wantCalls: "plex"},
		{name: "both_ack_without_monitor", jellyfin: ack, plex: ack, wantStatus: 200, wantType: "jellyfin", wantJellyfin: true, wantPlex: true, wantCalls: "jellyfin,plex"},
		{name: "jellyfin_ack_plex_error_without_monitor", jellyfin: ack, plex: rpcErr, wantStatus: 200, wantType: "jellyfin", wantJellyfin: true, wantCalls: "jellyfin,plex"},
		{name: "jellyfin_error_plex_ack_without_monitor", jellyfin: rpcErr, plex: ack, wantStatus: 200, wantType: "plex", wantPlex: true, wantCalls: "jellyfin,plex"},
		{name: "monitor_error_without_bridges", monitorStatus: 503, monitorBody: "monitor unavailable", wantStatus: 502, wantCode: "sessions.stop_failed"},
		{name: "monitor_error_both_bridges_fail", monitorStatus: 503, monitorBody: "monitor unavailable", jellyfin: rpcErr, plex: noAck, wantStatus: 502, wantCode: "sessions.stop_failed", wantCalls: "jellyfin,plex"},
		{name: "monitor_error_jellyfin_ack", monitorStatus: 503, monitorBody: "monitor unavailable", jellyfin: ack, plex: noAck, wantStatus: 200, wantType: "jellyfin", wantJellyfin: true, wantCalls: "jellyfin,plex"},
		{name: "monitor_error_plex_ack", monitorStatus: 503, monitorBody: "monitor unavailable", jellyfin: noAck, plex: ack, wantStatus: 200, wantType: "plex", wantPlex: true, wantCalls: "jellyfin,plex"},
		{name: "native_monitor_ack_skips_bridges", monitorStatus: 200, monitorBody: `{"stopped":true,"session":{"ServerType":"native"}}`, jellyfin: rpcErr, plex: rpcErr, wantStatus: 200, wantType: "native"},
		{name: "jellyfin_monitor_ack_bridge_error", monitorStatus: 200, monitorBody: `{"stopped":true,"session":{"ServerType":"jellyfin","ExternalSessionID":"external-1"}}`, jellyfin: rpcErr, plex: ack, wantStatus: 200, wantType: "jellyfin", wantCalls: "jellyfin", wantBridgeID: "external-1"},
		{name: "plex_monitor_ack_bridge_false", monitorStatus: 200, monitorBody: `{"stopped":true,"session":{"ServerType":"plex","ExternalSessionID":"external-1"}}`, jellyfin: ack, plex: noAck, wantStatus: 200, wantType: "plex", wantCalls: "plex", wantBridgeID: "external-1"},
		{name: "empty_monitor_response_retains_ack", monitorStatus: 200, monitorBody: `{}`, jellyfin: rpcErr, plex: noAck, wantStatus: 200, wantCalls: "jellyfin,plex"},
		{name: "monitor_stopped_false_retains_ack", monitorStatus: 200, monitorBody: `{"stopped":false,"session":{}}`, wantStatus: 200},
		{name: "no_backends", wantStatus: 503, wantCode: "sessions.unavailable"},
		{name: "invalid_id_skips_backends", invalidID: true, monitorStatus: 200, monitorBody: `{}`, jellyfin: ack, plex: ack, wantStatus: 400, wantCode: "sessions.id_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{}
			var calls []string
			var bridgeDeadline time.Time
			recordBridgeCall := func(ctx context.Context, backend, id, reason string) {
				t.Helper()
				calls = append(calls, backend)
				wantID := tt.wantBridgeID
				if wantID == "" {
					wantID = "session-1"
				}
				if id != wantID || reason != "household stop" {
					t.Errorf("%s request: id=%q reason=%q", backend, id, reason)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 6*time.Second {
					t.Errorf("%s request lacks the bounded Stop deadline", backend)
				}
				if !bridgeDeadline.IsZero() && !deadline.Equal(bridgeDeadline) {
					t.Errorf("%s request changed the shared Stop deadline", backend)
				}
				bridgeDeadline = deadline
			}
			if tt.jellyfin != absent {
				s.jellyfin = stopJellyfinClient{terminate: func(ctx context.Context, req *jellyfinv1.TerminateSessionRequest) (*jellyfinv1.TerminateSessionResponse, error) {
					recordBridgeCall(ctx, "jellyfin", req.GetSessionId(), req.GetReason())
					if tt.jellyfin == rpcErr {
						return nil, status.Error(codes.Unavailable, bridgeDetail)
					}
					return &jellyfinv1.TerminateSessionResponse{Ok: tt.jellyfin == ack}, nil
				}}
			}
			if tt.plex != absent {
				s.plex = stopPlexClient{terminate: func(ctx context.Context, req *plexv1.TerminateSessionRequest) (*plexv1.TerminateSessionResponse, error) {
					recordBridgeCall(ctx, "plex", req.GetSessionId(), req.GetReason())
					if tt.plex == rpcErr {
						return nil, status.Error(codes.Unavailable, bridgeDetail)
					}
					return &plexv1.TerminateSessionResponse{Ok: tt.plex == ack}, nil
				}}
			}
			var monitorMu sync.Mutex
			monitorCalls := 0
			if tt.monitorStatus != 0 {
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					monitorMu.Lock()
					monitorCalls++
					monitorMu.Unlock()
					if r.Method != http.MethodPost || r.URL.Path != "/sessions/session-1/stop" || r.Header.Get("Authorization") != "Bearer house-token" {
						t.Errorf("unexpected monitor request: %s %s authorization=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
					}
					w.WriteHeader(tt.monitorStatus)
					_, _ = w.Write([]byte(tt.monitorBody))
				}))
				t.Cleanup(up.Close)
				s.playbackMonitorHTTP, _ = url.Parse(up.URL)
				s.playbackMonitorToken = "house-token"
			}
			req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/stop", nil)
			if !tt.invalidID {
				req.SetPathValue("id", "session-1")
			}
			w := httptest.NewRecorder()
			s.handleStopPlaybackSession(w, req)
			if got := strings.Join(calls, ","); got != tt.wantCalls {
				t.Errorf("bridge calls=%q, want %q (exactly one attempt per eligible bridge)", got, tt.wantCalls)
			}
			wantMonitorCalls := 0
			if tt.monitorStatus != 0 && !tt.invalidID {
				wantMonitorCalls = 1
			}
			monitorMu.Lock()
			gotMonitorCalls := monitorCalls
			monitorMu.Unlock()
			if gotMonitorCalls != wantMonitorCalls {
				t.Errorf("monitor calls=%d, want %d", gotMonitorCalls, wantMonitorCalls)
			}
			if w.Code != tt.wantStatus {
				t.Errorf("status=%d, want %d; body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
			var body struct {
				Stopped         *bool  `json:"stopped"`
				ID              string `json:"id"`
				ServerType      string `json:"serverType"`
				JellyfinStopped bool   `json:"jellyfinStopped"`
				PlexStopped     bool   `json:"plexStopped"`
				Code            string `json:"code"`
				Error           string `json:"error"`
			}
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if tt.wantCode != "" {
				if body.Code != tt.wantCode || body.Error == "" || body.Stopped != nil {
					t.Errorf("want error %q without success acknowledgment, got %+v", tt.wantCode, body)
				}
				if strings.Contains(body.Error, bridgeDetail) {
					t.Error("bridge diagnostic leaked into failure response")
				}
				if tt.monitorStatus >= 400 && body.Error != tt.monitorBody {
					t.Errorf("monitor failure message=%q, want unchanged %q", body.Error, tt.monitorBody)
				}
				return
			}
			if body.Stopped == nil || !*body.Stopped || body.ID != "session-1" || body.Code != "" || body.Error != "" || body.ServerType != tt.wantType || body.JellyfinStopped != tt.wantJellyfin || body.PlexStopped != tt.wantPlex {
				t.Errorf("unexpected acknowledgment: %+v", body)
			}
		})
	}
}

func TestHandlePlaybackSessionEventsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackSessionEvents(w, httptest.NewRequest(http.MethodGet, "/api/sessions/events", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

// syncRecorder is a ResponseRecorder safe for reading the body while a
// streaming handler is still writing to it.
type syncRecorder struct {
	mu  sync.Mutex
	rec *httptest.ResponseRecorder
}

func newSyncRecorder() *syncRecorder { return &syncRecorder{rec: httptest.NewRecorder()} }

func (r *syncRecorder) Header() http.Header { return r.rec.Header() }

func (r *syncRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.WriteHeader(code)
}

func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec.Write(p)
}

func (r *syncRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.Flush()
}

func (r *syncRecorder) bodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec.Body.String()
}

func TestHandlePlaybackSessionEvents(t *testing.T) {
	var mu sync.Mutex
	var gotAuth, gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("event: connected\ndata: {\"event\":\"connected\"}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/events", nil).WithContext(ctx)
	w := newSyncRecorder()
	done := make(chan struct{})
	go func() {
		s.handlePlaybackSessionEvents(w, req)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.bodyString(), "connected") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/events/streams" || gotAuth != "Bearer house-token" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if !strings.Contains(w.bodyString(), "connected") {
		t.Fatalf("body %q", w.bodyString())
	}
}

func TestHandlePlaybackHistoryUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackHistory(w, httptest.NewRequest(http.MethodGet, "/api/history", nil))
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

func TestHandlePlaybackHistoryLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/history" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sessions": []map[string]any{
				{
					"ID":              "h1",
					"Title":           "Dune",
					"UserName":        "sam",
					"UserID":          "u-sam",
					"MuxcoreID":       "m1",
					"MediaType":       "movie",
					"State":           "stopped",
					"PositionSeconds": 8800,
					"DurationSeconds": 9000,
					"LastProgressAt":  "2026-09-08T01:00:00Z",
				},
			},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handlePlaybackHistory(w, httptest.NewRequest(http.MethodGet, "/api/history", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title   string `json:"title"`
			Href    string `json:"href"`
			Watched bool   `json:"watched"`
			User    string `json:"user"`
			UserID  string `json:"userId"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Dune" || body.Items[0].Href != "/movies/m1" || !body.Items[0].Watched || body.Items[0].User != "sam" || body.Items[0].UserID != "u-sam" {
		t.Fatalf("history %#v", body)
	}
}

func TestHandlePlaybackHistoryFilter(t *testing.T) {
	var gotQuery url.Values
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/history" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{}})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handlePlaybackHistory(w, httptest.NewRequest(http.MethodGet, "/api/history?userId=u-sam&q=Dune", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotQuery.Get("user_id") != "u-sam" || gotQuery.Get("q") != "Dune" || gotQuery.Get("limit") != "100" {
		t.Fatalf("query %#v", gotQuery)
	}
}

func TestHistoryWatched(t *testing.T) {
	if !historyWatched(8800, 9000) {
		t.Fatal("near end is watched")
	}
	if historyWatched(120, 9000) {
		t.Fatal("early progress is not watched")
	}
}

func TestPlaybackSessionHref(t *testing.T) {
	if playbackSessionHref("movie", "m1") != "/movies/m1" {
		t.Fatal(playbackSessionHref("movie", "m1"))
	}
	if playbackSessionHref("episode", "s1") != "/tv/s1" {
		t.Fatal(playbackSessionHref("episode", "s1"))
	}
}
