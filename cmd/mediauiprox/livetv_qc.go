package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Live TV + Quick Connect durable stores for media-ui-app until dedicated modules exist.

type liveTVChannel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Number   string `json:"number"`
	URL      string `json:"url"`
	Category string `json:"category"`
}

type liveTVRecording struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Status    string `json:"status"` // scheduled | recording | completed
	Path      string `json:"path,omitempty"`
}

type liveTVTimer struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Series    bool   `json:"series"`
}

// liveTVGuideRow is a durable EPG entry (file-backed companion; not a tuner/EPG grabber).
type liveTVGuideRow struct {
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

type liveTVFile struct {
	Channels   []liveTVChannel   `json:"channels"`
	Recordings []liveTVRecording `json:"recordings,omitempty"`
	Timers     []liveTVTimer     `json:"timers,omitempty"`
	Guide      []liveTVGuideRow  `json:"guide,omitempty"`
	Tuners     []map[string]any  `json:"tuners,omitempty"`
}

type liveTVStore struct {
	mu   sync.Mutex
	path string
}

func newLiveTVStore(path, userdataDir string) *liveTVStore {
	if path == "" {
		if userdataDir != "" {
			path = filepath.Join(userdataDir, "livetv.json")
		} else {
			path = filepath.Join(os.TempDir(), "muxcore-admin-livetv.json")
		}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	return &liveTVStore{path: path}
}

func defaultLiveTVFile() liveTVFile {
	now := time.Now().UTC()
	return liveTVFile{
		Channels: []liveTVChannel{
			{ID: "ch1", Name: "MuxCore Demo 1", Number: "1", Category: "Demo"},
			{ID: "ch2", Name: "MuxCore Demo 2", Number: "2", Category: "Demo"},
		},
		Timers: []liveTVTimer{
			{
				ID: "t1", ChannelID: "ch1", Title: "Demo series timer",
				Start: now.Add(2 * time.Hour).Format(time.RFC3339),
				End:   now.Add(3 * time.Hour).Format(time.RFC3339),
				Series: true,
			},
		},
		Guide: []liveTVGuideRow{
			{
				ChannelID: "ch1", Title: "MuxCore Demo — Morning block",
				Start: now.Add(-30 * time.Minute).Format(time.RFC3339),
				End:   now.Add(30 * time.Minute).Format(time.RFC3339),
			},
			{
				ChannelID: "ch2", Title: "MuxCore Demo 2 — Afternoon",
				Start: now.Add(-10 * time.Minute).Format(time.RFC3339),
				End:   now.Add(50 * time.Minute).Format(time.RFC3339),
			},
		},
		Recordings: []liveTVRecording{},
	}
}

func (s *liveTVStore) load() liveTVFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return defaultLiveTVFile()
	}
	var f liveTVFile
	if json.Unmarshal(raw, &f) != nil || len(f.Channels) == 0 {
		return defaultLiveTVFile()
	}
	return f
}

func (s *liveTVStore) save(f liveTVFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(s.path), 0o700)
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func guideNowPlaying(guide []liveTVGuideRow, channelID string, now time.Time) *struct {
	Title string `json:"title"`
	Start string `json:"start"`
	End   string `json:"end"`
} {
	var best *liveTVGuideRow
	var bestStart time.Time
	for i := range guide {
		row := &guide[i]
		if row.ChannelID != channelID {
			continue
		}
		start, err1 := time.Parse(time.RFC3339, row.Start)
		end, err2 := time.Parse(time.RFC3339, row.End)
		if err1 != nil || err2 != nil {
			continue
		}
		if now.Before(start) || !now.Before(end) {
			continue
		}
		if best == nil || start.After(bestStart) {
			best = row
			bestStart = start
		}
	}
	if best == nil {
		return nil
	}
	return &struct {
		Title string `json:"title"`
		Start string `json:"start"`
		End   string `json:"end"`
	}{Title: best.Title, Start: best.Start, End: best.End}
}

func (s *server) handleLiveTV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f := liveTVFile{}
	if s.livetv != nil {
		f = s.livetv.load()
	}
	now := time.Now().UTC()
	type prog struct {
		Title string `json:"title"`
		Start string `json:"start"`
		End   string `json:"end"`
	}
	type row struct {
		liveTVChannel
		NowPlaying *prog `json:"now_playing,omitempty"`
	}
	out := make([]row, 0, len(f.Channels))
	for _, ch := range f.Channels {
		rch := row{liveTVChannel: ch}
		if np := guideNowPlaying(f.Guide, ch.ID, now); np != nil {
			rch.NowPlaying = &prog{Title: np.Title, Start: np.Start, End: np.End}
		} else if len(f.Guide) == 0 {
			// Soft placeholder only when no durable guide rows exist yet.
			rch.NowPlaying = &prog{
				Title: ch.Name + " — live",
				Start: now.Add(-20 * time.Minute).Format(time.RFC3339),
				End:   now.Add(40 * time.Minute).Format(time.RFC3339),
			}
		}
		out = append(out, rch)
	}
	writeJSON(w, map[string]any{
		"channels":   out,
		"recordings": f.Recordings,
		"timers":     f.Timers,
		"guide":      f.Guide,
		"available":  true,
		"source":     "MEDIA_UI_LIVETV_FILE",
	})
}

func (s *server) handleLiveTVTimer(w http.ResponseWriter, r *http.Request) {
	if s.livetv == nil {
		http.Error(w, `{"error":"livetv disabled"}`, http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body liveTVTimer
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" || body.Title == "" {
		http.Error(w, `{"error":"channel_id and title required"}`, http.StatusBadRequest)
		return
	}
	f := s.livetv.load()
	if body.ID == "" {
		body.ID = "t-" + strconvNow()
	}
	if body.Start == "" {
		body.Start = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	}
	if body.End == "" {
		body.End = time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	}
	f.Timers = append(f.Timers, body)
	if err := s.livetv.save(f); err != nil {
		http.Error(w, `{"error":"save failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "timer": body})
}

func strconvNow() string {
	return strings.ReplaceAll(time.Now().UTC().Format("20060102T150405"), "T", "")
}

type qcEntry struct {
	Code      string    `json:"code"`
	UserID    string    `json:"user_id,omitempty"`
	Username  string    `json:"username,omitempty"`
	TenantID  string    `json:"tenant_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Approved  bool      `json:"approved"`
	Consumed  bool      `json:"consumed,omitempty"`
}

const quickConnectTTL = 15 * time.Minute

func quickConnectExpired(e qcEntry) bool {
	if e.CreatedAt.IsZero() {
		return false
	}
	return time.Since(e.CreatedAt) > quickConnectTTL
}

func generateQuickConnectCode() string {
	var b [3]byte
	rand.Read(b[:])
	n := (int(b[0])<<16 | int(b[1])<<8 | int(b[2])) % 1000000
	return fmt.Sprintf("%06d", n)
}

type quickConnectStore struct {
	mu   sync.Mutex
	path string
}

func newQuickConnectStore(dir string) *quickConnectStore {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "muxcore-media-userdata")
	}
	_ = os.MkdirAll(dir, 0o700)
	return &quickConnectStore{path: filepath.Join(dir, "quickconnect.json")}
}

func (q *quickConnectStore) load() map[string]qcEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	raw, err := os.ReadFile(q.path)
	if err != nil {
		return map[string]qcEntry{}
	}
	var m map[string]qcEntry
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return map[string]qcEntry{}
	}
	return m
}

func (q *quickConnectStore) save(m map[string]qcEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := q.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, q.path)
}

func (s *sessionStore) Lookup(tok string) (userID, username string, ok bool) {
	userID, username, _, ok = s.LookupTenant(tok)
	return userID, username, ok
}

func (s *sessionStore) LookupTenant(tok string) (userID, username, tenantID string, ok bool) {
	if tok == "" {
		return "", "", "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[tok]
	if !ok || time.Now().After(e.expiry) {
		return "", "", "", false
	}
	return e.userID, e.username, e.tenantID, true
}

func (s *server) handleQuickConnect(w http.ResponseWriter, r *http.Request) {
	if s.quickconnect == nil {
		http.Error(w, `{"error":"quick connect disabled"}`, http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
			Code   string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		if strings.EqualFold(strings.TrimSpace(body.Action), "register") {
			s.handleQuickConnectRegister(w, r, strings.TrimSpace(body.Code))
			return
		}
		code := strings.TrimSpace(body.Code)
		if len(code) < 4 {
			http.Error(w, `{"error":"code too short"}`, http.StatusBadRequest)
			return
		}
		m := s.quickconnect.load()
		userID, username, tenantID := "", "", ""
		if c, err := r.Cookie("session"); err == nil && s.sessions != nil {
			userID, username, tenantID, _ = s.sessions.LookupTenant(c.Value)
		}
		if userID == "" {
			http.Error(w, `{"error":"login required to approve device"}`, http.StatusUnauthorized)
			return
		}
		e, exists := m[code]
		if exists && quickConnectExpired(e) {
			delete(m, code)
			exists = false
		}
		if !exists {
			http.Error(w, `{"error":"code not found or expired"}`, http.StatusNotFound)
			return
		}
		m[code] = qcEntry{
			Code:      code,
			UserID:    userID,
			Username:  username,
			TenantID:  tenantID,
			CreatedAt: e.CreatedAt,
			Approved:  true,
			Consumed:  false,
		}
		if err := s.quickconnect.save(m); err != nil {
			http.Error(w, `{"error":"save failed"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"ok":       true,
			"approved": true,
			"code":     code,
			"message":  "Device authorized. The TV can poll GET /api/quickconnect?code=…",
		})
	case http.MethodGet:
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, `{"error":"code required"}`, http.StatusBadRequest)
			return
		}
		m := s.quickconnect.load()
		e, ok := m[code]
		if !ok || quickConnectExpired(e) {
			if ok {
				delete(m, code)
				_ = s.quickconnect.save(m)
			}
			writeJSON(w, map[string]any{"approved": false, "code": code})
			return
		}
		resp := map[string]any{
			"approved":   e.Approved,
			"code":       e.Code,
			"username":   e.Username,
			"user_id":    e.UserID,
			"created_at": e.CreatedAt,
		}
		if e.Approved && e.UserID != "" && !e.Consumed && s.sessions != nil {
			sess, err := s.sessions.CreateWithTenant(e.UserID, e.Username, e.TenantID)
			if err != nil {
				http.Error(w, `{"error":"session error"}`, http.StatusInternalServerError)
				return
			}
			e.Consumed = true
			m[code] = e
			if err := s.quickconnect.save(m); err != nil {
				http.Error(w, `{"error":"save failed"}`, http.StatusInternalServerError)
				return
			}
			resp["session_token"] = sess
			resp["consumed"] = true
		}
		writeJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleQuickConnectRegister(w http.ResponseWriter, r *http.Request, code string) {
	if code == "" {
		code = generateQuickConnectCode()
	}
	if len(code) < 4 {
		http.Error(w, `{"error":"code too short"}`, http.StatusBadRequest)
		return
	}
	m := s.quickconnect.load()
	if e, ok := m[code]; ok && !quickConnectExpired(e) {
		writeJSON(w, map[string]any{
			"code":     code,
			"approved": e.Approved,
			"pending":  !e.Approved,
		})
		return
	}
	m[code] = qcEntry{
		Code:      code,
		CreatedAt: time.Now().UTC(),
		Approved:  false,
	}
	if err := s.quickconnect.save(m); err != nil {
		http.Error(w, `{"error":"save failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"code":     code,
		"approved": false,
		"pending":  true,
		"message":  "Enter this code at mux.zem.systems/quickconnect (or your server Quick Connect page).",
	})
}

func (s *server) handleTrackLyrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/music/tracks/"), "/")
	id = strings.TrimSuffix(id, "/lyrics")
	id = strings.Trim(id, "/")
	if id == "" || s.musicHTTP == nil {
		http.NotFound(w, r)
		return
	}
	u := *s.musicHTTP
	u.Path = strings.TrimRight(s.musicHTTP.Path, "/") + "/api/tracks/" + url.PathEscape(id) + "/lyrics"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
