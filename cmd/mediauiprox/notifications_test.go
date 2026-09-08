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

	notifyv1 "github.com/Muxcore-Media/contracts-notification/muxcore/notification/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureNotify struct {
	notifyv1.UnimplementedNotificationServiceServer
	channels map[notifyv1.Channel]*notifyv1.ChannelStatus
	lastCfg  *notifyv1.ConfigureRequest
	lastPing *notifyv1.NotifyRequest
}

func (f *fixtureNotify) Status(_ context.Context, _ *notifyv1.StatusRequest) (*notifyv1.StatusResponse, error) {
	out := make([]*notifyv1.ChannelStatus, 0, len(f.channels))
	for _, st := range f.channels {
		out = append(out, st)
	}
	return &notifyv1.StatusResponse{Channels: out}, nil
}

func (f *fixtureNotify) Configure(_ context.Context, req *notifyv1.ConfigureRequest) (*notifyv1.ConfigureResponse, error) {
	f.lastCfg = req
	if f.channels == nil {
		f.channels = map[notifyv1.Channel]*notifyv1.ChannelStatus{}
	}
	f.channels[req.GetChannel()] = &notifyv1.ChannelStatus{
		Channel:     req.GetChannel(),
		Enabled:     req.GetEnabled(),
		Description: notifyChannelID(req.GetChannel()) + " configured",
	}
	return &notifyv1.ConfigureResponse{Configured: true}, nil
}

func (f *fixtureNotify) Notify(_ context.Context, req *notifyv1.NotifyRequest) (*notifyv1.NotifyResponse, error) {
	f.lastPing = req
	results := make([]*notifyv1.ChannelResult, 0, len(req.GetChannels()))
	for _, ch := range req.GetChannels() {
		results = append(results, &notifyv1.ChannelResult{Channel: ch, Success: true})
	}
	return &notifyv1.NotifyResponse{Results: results}, nil
}

func privilegedNotifyRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureNotify) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureNotify{channels: map[notifyv1.Channel]*notifyv1.ChannelStatus{
		notifyv1.Channel_CHANNEL_DISCORD: {Channel: notifyv1.Channel_CHANNEL_DISCORD, Enabled: true, Description: "Discord"},
	}}
	srv := grpc.NewServer()
	notifyv1.RegisterNotificationServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{notify: notifyv1.NewNotificationServiceClient(conn), sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleListNotificationsForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListNotifications(w, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListNotificationsUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListNotifications(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Channels  []any `json:"channels"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Channels == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListNotificationsLive(t *testing.T) {
	s, req, _ := privilegedNotifyRequest(t, http.MethodGet, "/api/notifications", "")
	w := httptest.NewRecorder()
	s.handleListNotifications(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Channels  []struct {
			ID          string `json:"id"`
			Enabled     bool   `json:"enabled"`
			Description string `json:"description"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Channels) != 1 || body.Channels[0].ID != "discord" {
		t.Fatalf("%#v", body)
	}
	if strings.Contains(w.Body.String(), "http") || strings.Contains(strings.ToLower(w.Body.String()), "webhook") && strings.Contains(w.Body.String(), "https") {
		t.Fatalf("must not echo webhook urls: %s", w.Body.String())
	}
}

func TestHandleConfigureNotification(t *testing.T) {
	s, req, fake := privilegedNotifyRequest(t, http.MethodPut, "/api/notifications", `{"channel":"slack","webhook_url":"https://hooks.example/slack"}`)
	w := httptest.NewRecorder()
	s.handleConfigureNotification(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.lastCfg == nil || fake.lastCfg.GetChannel() != notifyv1.Channel_CHANNEL_SLACK {
		t.Fatalf("cfg %#v", fake.lastCfg)
	}
	if fake.lastCfg.GetSettings()["webhook_url"] != "https://hooks.example/slack" {
		t.Fatalf("settings %#v", fake.lastCfg.GetSettings())
	}
	var body struct {
		Configured bool   `json:"configured"`
		Channel    string `json:"channel"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Configured || body.Channel != "slack" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleConfigureNotificationEmail(t *testing.T) {
	s, req, fake := privilegedNotifyRequest(t, http.MethodPut, "/api/notifications", `{"channel":"email","smtp_host":"smtp.example","smtp_from":"mux@home","to":"ops@home"}`)
	w := httptest.NewRecorder()
	s.handleConfigureNotification(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.lastCfg.GetSettings()["smtp_host"] != "smtp.example" || fake.lastCfg.GetSettings()["to"] != "ops@home" {
		t.Fatalf("settings %#v", fake.lastCfg.GetSettings())
	}
}

func TestHandleConfigureNotificationRejectsBadChannel(t *testing.T) {
	s, req, _ := privilegedNotifyRequest(t, http.MethodPut, "/api/notifications", `{"channel":"sms","webhook_url":"https://x"}`)
	w := httptest.NewRecorder()
	s.handleConfigureNotification(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleTestNotification(t *testing.T) {
	s, req, fake := privilegedNotifyRequest(t, http.MethodPost, "/api/notifications/test", `{"channel":"discord"}`)
	w := httptest.NewRecorder()
	s.handleTestNotification(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.lastPing == nil || fake.lastPing.GetTitle() != "MuxCore test" {
		t.Fatalf("ping %#v", fake.lastPing)
	}
	var body struct {
		OK      bool   `json:"ok"`
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Channel != "discord" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleConfigureNotificationUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodPut, "/api/notifications", strings.NewReader(`{"channel":"discord","webhook_url":"https://x"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleConfigureNotification(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}
