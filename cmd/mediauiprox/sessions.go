package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
)

func (s *server) handlePlaybackHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	userID := strings.TrimSpace(r.URL.Query().Get("userId"))
	if userID == "" {
		userID = strings.TrimSpace(r.URL.Query().Get("user_id"))
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	items, available, err := s.listPlaybackHistory(ctx, limit, userID, q)
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     len(items),
		"available": available,
	})
}

func (s *server) handleStopPlaybackSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "sessions.id_required"})
		return
	}
	monitorConnected := s.playbackMonitorHTTP != nil && strings.TrimSpace(s.playbackMonitorHTTP.String()) != ""
	if !monitorConnected && s.jellyfin == nil && s.plex == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "sessions.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	rec := map[string]any{}
	var monErr error
	if monitorConnected {
		rec, monErr = s.stopPlaybackMonitorSession(ctx, id)
		if rec == nil {
			rec = map[string]any{}
		}
	}
	serverType := strings.ToLower(firstString(rec, "serverType", "ServerType", "server_type"))
	externalID := firstString(rec, "externalSessionId", "ExternalSessionID", "external_session_id")
	sid := externalID
	if sid == "" {
		sid = id
	}
	jellyfinStopped := false
	if s.jellyfin != nil && (serverType == "" || serverType == "jellyfin" || serverType == "emby") {
		resp, jerr := s.jellyfin.TerminateSession(ctx, &jellyfinv1.TerminateSessionRequest{
			SessionId: sid,
			Reason:    "household stop",
		})
		jellyfinStopped = jerr == nil && resp.GetOk()
	}
	plexStopped := false
	if s.plex != nil && (serverType == "" || serverType == "plex") {
		resp, perr := s.plex.TerminateSession(ctx, &plexv1.TerminateSessionRequest{
			SessionId: sid,
			Reason:    "household stop",
		})
		plexStopped = perr == nil && resp.GetOk()
	}
	if monErr != nil && !jellyfinStopped && !plexStopped {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": monErr.Error(), "code": "sessions.stop_failed"})
		return
	}
	if serverType == "" {
		if jellyfinStopped {
			serverType = "jellyfin"
		} else if plexStopped {
			serverType = "plex"
		}
	}
	writeJSON(w, map[string]any{
		"stopped":         true,
		"id":              id,
		"serverType":      serverType,
		"jellyfinStopped": jellyfinStopped,
		"plexStopped":     plexStopped,
	})
}

func (s *server) stopPlaybackMonitorSession(ctx context.Context, id string) (map[string]any, error) {
	u := *s.playbackMonitorHTTP
	u.Path = strings.TrimRight(s.playbackMonitorHTTP.Path, "/") + "/sessions/" + url.PathEscape(id) + "/stop"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(s.playbackMonitorToken); tok != "" {
		req.Header.Set("X-Playback-Monitor-Token", tok)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, errPlaybackMonitorStatus(resp.StatusCode, msg)
	}
	var body struct {
		Session map[string]any `json:"session"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if body.Session == nil {
		body.Session = map[string]any{}
	}
	return body.Session, nil
}

// handlePlaybackSessionEvents proxies operator GET /events/streams (live session SSE).
// SPA: EventSource('/api/sessions/events') — cookies ride along; BFF adds the operator token.
func (s *server) handlePlaybackSessionEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "sessions.unavailable"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": "streaming unsupported", "code": "sessions.no_flush"})
		return
	}
	u := *s.playbackMonitorHTTP
	u.Path = strings.TrimRight(s.playbackMonitorHTTP.Path, "/") + "/events/streams"
	u.RawQuery = ""
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "sessions.events_failed"})
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	if tok := strings.TrimSpace(s.playbackMonitorToken); tok != "" {
		req.Header.Set("X-Playback-Monitor-Token", tok)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := playbackMonitorStreamClient.Do(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "sessions.events_failed"})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "sessions.events_failed"})
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/event-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

func (s *server) handlePlaybackSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	sessions, available, err := s.listPlaybackSessions(ctx)
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, map[string]any{
		"items":     sessions,
		"total":     len(sessions),
		"available": available,
	})
}

func (s *server) listPlaybackSessions(ctx context.Context) ([]map[string]any, bool, error) {
	rows, available, err := s.listPlaybackMonitorRows(ctx, "/sessions/active")
	if err != nil {
		rows = []map[string]any{}
		available = false
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if id, ok := row["id"].(string); ok && strings.TrimSpace(id) != "" {
			seen[strings.TrimSpace(id)] = true
		}
	}
	if extra, ok := s.listJellyfinBridgeSessions(ctx); ok {
		available = true
		for _, row := range extra {
			id, _ := row["id"].(string)
			id = strings.TrimSpace(id)
			if id != "" && seen[id] {
				continue
			}
			if id != "" {
				seen[id] = true
			}
			rows = append(rows, row)
		}
	}
	if extra, ok := s.listPlexBridgeSessions(ctx); ok {
		available = true
		for _, row := range extra {
			id, _ := row["id"].(string)
			id = strings.TrimSpace(id)
			if id != "" && seen[id] {
				continue
			}
			if id != "" {
				seen[id] = true
			}
			rows = append(rows, row)
		}
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, available, nil
}

func (s *server) listJellyfinBridgeSessions(ctx context.Context) ([]map[string]any, bool) {
	if s.jellyfin == nil {
		return nil, false
	}
	resp, err := s.jellyfin.ListSessions(ctx, &jellyfinv1.ListSessionsRequest{})
	if err != nil {
		return nil, false
	}
	out := make([]map[string]any, 0, len(resp.GetSessions()))
	for _, sess := range resp.GetSessions() {
		if sess == nil {
			continue
		}
		id := strings.TrimSpace(sess.GetId())
		if id == "" {
			continue
		}
		title := strings.TrimSpace(sess.GetItemTitle())
		if title == "" {
			title = "Jellyfin session"
		}
		state := "playing"
		if sess.GetPaused() {
			state = "paused"
		}
		out = append(out, map[string]any{
			"id":              id,
			"title":           title,
			"user":            strings.TrimSpace(sess.GetUserName()),
			"userId":          strings.TrimSpace(sess.GetUserId()),
			"mediaId":         strings.TrimSpace(sess.GetItemId()),
			"mediaType":       "",
			"player":          strings.TrimSpace(sess.GetClient()),
			"platform":        "",
			"device":          strings.TrimSpace(sess.GetDevice()),
			"state":           state,
			"paused":          sess.GetPaused(),
			"transcode":       false,
			"positionSeconds": float64(sess.GetPositionSeconds()),
			"durationSeconds": 0,
			"watched":         false,
			"href":            "",
			"serverType":      "jellyfin",
			"updatedAt":       "",
		})
	}
	return out, true
}

func (s *server) listPlexBridgeSessions(ctx context.Context) ([]map[string]any, bool) {
	if s.plex == nil {
		return nil, false
	}
	resp, err := s.plex.ListSessions(ctx, &plexv1.ListSessionsRequest{})
	if err != nil {
		return nil, false
	}
	out := make([]map[string]any, 0, len(resp.GetSessions()))
	for _, sess := range resp.GetSessions() {
		if sess == nil {
			continue
		}
		id := strings.TrimSpace(sess.GetSessionId())
		if id == "" {
			continue
		}
		title := strings.TrimSpace(sess.GetTitle())
		if title == "" {
			title = "Plex session"
		}
		state := "playing"
		if sess.GetPaused() {
			state = "paused"
		}
		pos := float64(sess.GetPositionSeconds())
		dur := float64(sess.GetDurationSeconds())
		out = append(out, map[string]any{
			"id":              id,
			"title":           title,
			"user":            strings.TrimSpace(sess.GetUserName()),
			"userId":          strings.TrimSpace(sess.GetUserId()),
			"mediaId":         strings.TrimSpace(sess.GetItemId()),
			"mediaType":       "",
			"player":          strings.TrimSpace(sess.GetDevice()),
			"platform":        "",
			"device":          strings.TrimSpace(sess.GetDevice()),
			"state":           state,
			"paused":          sess.GetPaused(),
			"transcode":       false,
			"positionSeconds": pos,
			"durationSeconds": dur,
			"watched":         historyWatched(pos, dur),
			"href":            "",
			"serverType":      "plex",
			"updatedAt":       "",
		})
	}
	return out, true
}

func (s *server) listPlaybackHistory(ctx context.Context, limit int, userID, q string) ([]map[string]any, bool, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	path := "/history?limit=" + strconv.Itoa(limit)
	if userID = strings.TrimSpace(userID); userID != "" {
		path += "&user_id=" + url.QueryEscape(userID)
	}
	if q = strings.TrimSpace(q); q != "" {
		path += "&q=" + url.QueryEscape(q)
	}
	return s.listPlaybackMonitorRows(ctx, path)
}

func (s *server) listPlaybackMonitorRows(ctx context.Context, path string) ([]map[string]any, bool, error) {
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		return []map[string]any{}, false, nil
	}
	u := *s.playbackMonitorHTTP
	base := strings.TrimRight(s.playbackMonitorHTTP.Path, "/")
	q := ""
	if i := strings.Index(path, "?"); i >= 0 {
		q = path[i+1:]
		path = path[:i]
	}
	u.Path = base + path
	u.RawQuery = q
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(s.playbackMonitorToken); tok != "" {
		req.Header.Set("X-Playback-Monitor-Token", tok)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, false, errPlaybackMonitorStatus(resp.StatusCode, msg)
	}
	var body struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	out := make([]map[string]any, 0, len(body.Sessions))
	for _, row := range body.Sessions {
		out = append(out, publicPlaybackSession(row))
	}
	return out, true, nil
}

func publicPlaybackSession(row map[string]any) map[string]any {
	id := firstString(row, "id", "ID", "externalSessionId", "ExternalSessionID")
	mediaID := firstString(row, "mediaId", "muxcoreId", "MuxcoreID", "itemId", "ItemID")
	mediaType := strings.ToLower(firstString(row, "mediaType", "MediaType"))
	title := firstString(row, "title", "Title")
	userID := firstString(row, "userId", "UserID", "user_id")
	user := firstString(row, "userName", "UserName", "user")
	if user == "" {
		user = userID
	}
	player := firstString(row, "player", "Player")
	platform := firstString(row, "platform", "Platform")
	device := firstString(row, "device", "Device")
	state := strings.ToLower(firstString(row, "state", "State"))
	href := playbackSessionHref(mediaType, mediaID)
	pos := floatVal(row, "positionSeconds", "PositionSeconds")
	dur := floatVal(row, "durationSeconds", "DurationSeconds")
	return map[string]any{
		"id":              id,
		"title":           title,
		"user":            user,
		"userId":          userID,
		"mediaId":         mediaID,
		"mediaType":       mediaType,
		"player":          player,
		"platform":        platform,
		"device":          device,
		"state":           state,
		"paused":          state == "paused" || boolVal(row, "isPaused", "IsPaused"),
		"transcode":       boolVal(row, "isTranscode", "IsTranscode"),
		"positionSeconds": pos,
		"durationSeconds": dur,
		"watched":         historyWatched(pos, dur),
		"href":            href,
		"serverType":      strings.ToLower(firstString(row, "serverType", "ServerType", "server_type")),
		"updatedAt":       firstString(row, "lastProgressAt", "LastProgressAt", "stoppedAt", "StoppedAt", "startedAt", "StartedAt"),
	}
}

func historyWatched(positionSeconds, durationSeconds float64) bool {
	if durationSeconds <= 0 {
		return false
	}
	if positionSeconds/durationSeconds >= 0.90 {
		return true
	}
	return durationSeconds-positionSeconds <= 60
}

func playbackSessionHref(mediaType, mediaID string) string {
	id := strings.TrimSpace(mediaID)
	if id == "" {
		return ""
	}
	switch mediaType {
	case "movie", "movies":
		return "/movies/" + id
	case "episode", "tv", "show", "series":
		return "/tv/" + id
	case "audiobook":
		return "/audiobooks/" + id
	default:
		return ""
	}
}

func firstString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := row[key]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return strings.TrimSpace(t)
				}
			}
		}
	}
	return ""
}

func boolVal(row map[string]any, keys ...string) bool {
	for _, key := range keys {
		if v, ok := row[key]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func floatVal(row map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if v, ok := row[key]; ok {
			switch t := v.(type) {
			case float64:
				return t
			case int:
				return float64(t)
			case int64:
				return float64(t)
			case json.Number:
				n, _ := t.Float64()
				return n
			}
		}
	}
	return 0
}
