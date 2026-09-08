package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// nativePlaybackSession is the household player payload for POST /api/playback/session.
type nativePlaybackSession struct {
	EventType       string `json:"event_type"`
	SessionID       string `json:"session_id"`
	MediaID         string `json:"media_id"`
	Title           string `json:"title"`
	MediaType       string `json:"media_type"`
	PositionSeconds int64  `json:"position_seconds"`
	DurationSeconds int64  `json:"duration_seconds"`
	IsPaused        bool   `json:"is_paused"`
	IsTranscode     bool   `json:"is_transcode"`
	Player          string `json:"player"`
	Platform        string `json:"platform"`
}

// monitorSessionEvent matches playback-monitor SessionEvent JSON field names
// (encoding/json default: exported Go names, no snake_case tags).
type monitorSessionEvent struct {
	EventType         string `json:"EventType"`
	SourceModule      string `json:"SourceModule"`
	ServerID          string `json:"ServerID"`
	ServerType        string `json:"ServerType"`
	ExternalSessionID string `json:"ExternalSessionID"`
	UserID            string `json:"UserID"`
	UserName          string `json:"UserName"`
	ItemID            string `json:"ItemID"`
	MuxcoreID         string `json:"MuxcoreID"`
	Title             string `json:"Title"`
	MediaType         string `json:"MediaType"`
	PositionSeconds   int64  `json:"PositionSeconds"`
	DurationSeconds   int64  `json:"DurationSeconds"`
	IsPaused          bool   `json:"IsPaused"`
	IsTranscode       bool   `json:"IsTranscode"`
	Platform          string `json:"Platform"`
	Device            string `json:"Device"`
	Player            string `json:"Player"`
	IPAddress         string `json:"IPAddress"`
}

func normalizePlaybackEventType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "started", "playback.started":
		return "playback.started"
	case "progress", "playback.progress":
		return "playback.progress"
	case "stopped", "paused", "playback.stopped":
		return "playback.stopped"
	default:
		return ""
	}
}

func normalizePlaybackMediaType(raw, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "movie", "movies":
		return "movie"
	case "episode", "tv", "show", "series":
		return "episode"
	case "music", "track", "audio":
		return "track"
	case "audiobook":
		return "audiobook"
	default:
		if fallback != "" {
			return normalizePlaybackMediaType(fallback, "")
		}
		return "movie"
	}
}

func (s *server) handlePlaybackSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	var in nativePlaybackSession
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "playback.session_invalid")
		return
	}
	eventType := normalizePlaybackEventType(in.EventType)
	if eventType == "" {
		writeAPIError(w, http.StatusBadRequest, "event_type required (started|progress|stopped)", "playback.session_event")
		return
	}
	mediaID := strings.TrimSpace(in.MediaID)
	if mediaID == "" {
		writeAPIError(w, http.StatusBadRequest, "media_id required", "playback.session_media")
		return
	}

	userID, username, _, _, _ := s.sessionPrincipal(r)
	if userID == "" {
		userID = "anonymous"
	}
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		sessionID = userID + ":" + mediaID
	}
	player := strings.TrimSpace(in.Player)
	if player == "" {
		player = "media-ui"
	}
	platform := strings.TrimSpace(in.Platform)
	if platform == "" {
		platform = "web"
	}

	ev := monitorSessionEvent{
		EventType:         eventType,
		SourceModule:      "media-ui",
		ServerID:          "muxcore-native",
		ServerType:        "native",
		ExternalSessionID: sessionID,
		UserID:            userID,
		UserName:          username,
		ItemID:            mediaID,
		MuxcoreID:         mediaID,
		Title:             strings.TrimSpace(in.Title),
		MediaType:         normalizePlaybackMediaType(in.MediaType, ""),
		PositionSeconds:   in.PositionSeconds,
		DurationSeconds:   in.DurationSeconds,
		IsPaused:          in.IsPaused || eventType == "playback.stopped",
		IsTranscode:       in.IsTranscode,
		Platform:          platform,
		Device:            "browser",
		Player:            player,
		IPAddress:         clientIP(r, s.trustedProxies),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	sessionKey, forwarded, err := s.forwardPlaybackMonitorIngest(ctx, ev)
	if err != nil {
		if errors.Is(err, errPlaybackSessionStopped) {
			writeJSON(w, map[string]any{
				"accepted":   true,
				"forwarded":  true,
				"stopped":    true,
				"session_id": sessionKey,
			})
			return
		}
		// Player resume must not fail when the optional monitor is down.
		writeJSONStatus(w, http.StatusAccepted, map[string]any{
			"accepted":  true,
			"forwarded": false,
			"error":     err.Error(),
		})
		return
	}
	if !forwarded {
		writeJSONStatus(w, http.StatusAccepted, map[string]any{
			"accepted":  true,
			"forwarded": false,
		})
		return
	}
	writeJSON(w, map[string]any{
		"accepted":   true,
		"forwarded":  true,
		"session_id": sessionKey,
	})
}

func (s *server) forwardPlaybackMonitorIngest(ctx context.Context, ev monitorSessionEvent) (sessionID string, forwarded bool, err error) {
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		return "", false, nil
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return "", false, err
	}
	u := *s.playbackMonitorHTTP
	u.Path = strings.TrimRight(s.playbackMonitorHTTP.Path, "/") + "/ingest"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(s.playbackMonitorToken); tok != "" {
		req.Header.Set("X-Playback-Monitor-Token", tok)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return "", false, errPlaybackMonitorStatus(resp.StatusCode, msg)
	}
	var out struct {
		SessionID string `json:"session_id"`
		Stopped   bool   `json:"stopped"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Stopped {
		return out.SessionID, true, errPlaybackSessionStopped
	}
	return out.SessionID, true, nil
}

var errPlaybackSessionStopped = errPlaybackMonitorStatus(http.StatusConflict, "session stopped")

type playbackMonitorStatusError struct {
	code int
	msg  string
}

func errPlaybackMonitorStatus(code int, msg string) error {
	return playbackMonitorStatusError{code: code, msg: msg}
}

func (e playbackMonitorStatusError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return http.StatusText(e.code)
}

func (s *server) playbackMonitorModuleLive(ctx context.Context) bool {
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		return false
	}
	u := *s.playbackMonitorHTTP
	u.Path = "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func clientIP(r *http.Request, trusted []net.IPNet) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip := net.ParseIP(host); ip != nil && len(trusted) > 0 {
			if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); fwd != "" {
				for _, cand := range strings.Split(fwd, ",") {
					cand = strings.TrimSpace(cand)
					if parsed := net.ParseIP(cand); parsed != nil {
						return parsed.String()
					}
				}
			}
		}
		return host
	}
	return r.RemoteAddr
}
