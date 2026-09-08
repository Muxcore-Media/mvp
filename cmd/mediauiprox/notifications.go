package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	notifyv1 "github.com/Muxcore-Media/contracts-notification/muxcore/notification/v1"
)

func notifyChannelID(ch notifyv1.Channel) string {
	switch ch {
	case notifyv1.Channel_CHANNEL_DISCORD:
		return "discord"
	case notifyv1.Channel_CHANNEL_SLACK:
		return "slack"
	case notifyv1.Channel_CHANNEL_WEBHOOK:
		return "webhook"
	case notifyv1.Channel_CHANNEL_EMAIL:
		return "email"
	case notifyv1.Channel_CHANNEL_APPRISE:
		return "apprise"
	default:
		return ""
	}
}

func parseNotifyChannel(raw string) (notifyv1.Channel, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "discord":
		return notifyv1.Channel_CHANNEL_DISCORD, true
	case "slack":
		return notifyv1.Channel_CHANNEL_SLACK, true
	case "webhook":
		return notifyv1.Channel_CHANNEL_WEBHOOK, true
	case "email":
		return notifyv1.Channel_CHANNEL_EMAIL, true
	default:
		return notifyv1.Channel_CHANNEL_UNSPECIFIED, false
	}
}

func publicNotifyChannel(st *notifyv1.ChannelStatus) map[string]any {
	if st == nil {
		return map[string]any{}
	}
	id := notifyChannelID(st.GetChannel())
	if id == "" {
		return map[string]any{}
	}
	out := map[string]any{
		"id":          id,
		"enabled":     st.GetEnabled(),
		"description": st.GetDescription(),
		"last_error":  st.GetLastError(),
	}
	if ts := st.GetLastSuccessAt(); ts != nil && ts.IsValid() {
		out["last_success_at"] = ts.AsTime().UTC().Format(time.RFC3339)
	}
	return out
}

func (s *server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "notify.forbidden"})
		return
	}
	if s.notify == nil {
		writeJSON(w, map[string]any{"available": false, "channels": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.notify.Status(ctx, &notifyv1.StatusRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "channels": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetChannels()))
	for _, st := range resp.GetChannels() {
		row := publicNotifyChannel(st)
		if row["id"] == nil || row["id"] == "" {
			continue
		}
		out = append(out, row)
	}
	writeJSON(w, map[string]any{"available": true, "channels": out})
}

func (s *server) handleConfigureNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "notify.forbidden"})
		return
	}
	if s.notify == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Notifications are unavailable — start notification-default.",
			"code":  "notify.unavailable",
		})
		return
	}
	var body struct {
		Channel    string `json:"channel"`
		WebhookURL string `json:"webhook_url"`
		SMTPHost   string `json:"smtp_host"`
		SMTPPort   string `json:"smtp_port"`
		SMTPUser   string `json:"smtp_user"`
		SMTPPass   string `json:"smtp_pass"`
		SMTPFrom   string `json:"smtp_from"`
		To         string `json:"to"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "notify.invalid_json"})
		return
	}
	ch, ok := parseNotifyChannel(body.Channel)
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "channel must be discord, slack, webhook, or email", "code": "notify.channel_invalid"})
		return
	}
	settings := map[string]string{}
	if ch == notifyv1.Channel_CHANNEL_EMAIL {
		if host := strings.TrimSpace(body.SMTPHost); host != "" {
			settings["smtp_host"] = host
		}
		if port := strings.TrimSpace(body.SMTPPort); port != "" {
			settings["smtp_port"] = port
		}
		if user := strings.TrimSpace(body.SMTPUser); user != "" {
			settings["smtp_user"] = user
		}
		if pass := strings.TrimSpace(body.SMTPPass); pass != "" && pass != "********" {
			settings["smtp_pass"] = pass
		}
		if from := strings.TrimSpace(body.SMTPFrom); from != "" {
			settings["smtp_from"] = from
		}
		if to := strings.TrimSpace(body.To); to != "" {
			settings["to"] = to
		}
		if settings["smtp_host"] == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "smtp_host required", "code": "notify.smtp_host_required"})
			return
		}
	} else {
		url := strings.TrimSpace(body.WebhookURL)
		if url == "********" {
			writeJSON(w, map[string]any{"configured": true, "channel": notifyChannelID(ch)})
			return
		}
		settings["webhook_url"] = url
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.notify.Configure(ctx, &notifyv1.ConfigureRequest{
		Channel:  ch,
		Settings: settings,
		Enabled:  enabled,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "notify.configure_failed"})
		return
	}
	writeJSON(w, map[string]any{"configured": resp.GetConfigured(), "channel": notifyChannelID(ch)})
}

func (s *server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "notify.forbidden"})
		return
	}
	if s.notify == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Notifications are unavailable — start notification-default.",
			"code":  "notify.unavailable",
		})
		return
	}
	var body struct {
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "notify.invalid_json"})
		return
	}
	ch, ok := parseNotifyChannel(body.Channel)
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "channel must be discord, slack, webhook, or email", "code": "notify.channel_invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	resp, err := s.notify.Notify(ctx, &notifyv1.NotifyRequest{
		Title:        "MuxCore test",
		Message:      "Household Connect test from Settings.",
		Severity:     notifyv1.Severity_SEVERITY_INFO,
		SourceModule: "media-ui",
		Channels:     []notifyv1.Channel{ch},
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "notify.test_failed"})
		return
	}
	success := false
	errMsg := ""
	for _, row := range resp.GetResults() {
		if row == nil || row.GetChannel() != ch {
			continue
		}
		success = row.GetSuccess()
		errMsg = row.GetError()
		break
	}
	if !success && errMsg == "" && len(resp.GetResults()) == 0 {
		errMsg = "no channel result"
	}
	out := map[string]any{"ok": success, "channel": notifyChannelID(ch)}
	if errMsg != "" {
		out["error"] = errMsg
	}
	if !success {
		writeJSONStatus(w, http.StatusBadGateway, out)
		return
	}
	writeJSON(w, out)
}
