package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
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
				Start:  now.Add(2 * time.Hour).Format(time.RFC3339),
				End:    now.Add(3 * time.Hour).Format(time.RFC3339),
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
		writeAPIMethodNotAllowed(w)
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
		writeAPIError(w, http.StatusServiceUnavailable, "livetv disabled", "livetv.disabled")
		return
	}
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	var body liveTVTimer
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "livetv.invalid_json")
		return
	}
	if body.ChannelID == "" || body.Title == "" {
		writeAPIError(w, http.StatusBadRequest, "channel_id and title required", "livetv.fields_required")
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
		writeAPIError(w, http.StatusInternalServerError, "save failed", "livetv.save_failed")
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
	// erased reports ids present in the last-seen erasure ledger (ADR-0035
	// §3). Quick Connect mints a session for a user id without that user's
	// bearer, so an erased id is refused here, under mu, which eraseUser also
	// takes. nil = no ledger.
	erased func(userID string) bool
}

// mutate runs fn on the current codes under the store lock and saves the map
// when fn reports a change. Load, change and save are one critical section, so
// an erasure cannot interleave between them.
func (q *quickConnectStore) mutate(fn func(m map[string]qcEntry) (save bool)) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	m := map[string]qcEntry{}
	if raw, err := os.ReadFile(q.path); err == nil {
		var got map[string]qcEntry
		if json.Unmarshal(raw, &got) == nil && got != nil {
			m = got
		}
	}
	if !fn(m) {
		return nil
	}
	return q.saveLocked(m)
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

func (s *server) handleQuickConnect(w http.ResponseWriter, r *http.Request) {
	if s.quickconnect == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "quick connect disabled", "quickconnect.disabled")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
			Code   string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid json", "quickconnect.invalid_json")
			return
		}
		if strings.EqualFold(strings.TrimSpace(body.Action), "register") {
			s.handleQuickConnectRegister(w, r, strings.TrimSpace(body.Code))
			return
		}
		code := strings.TrimSpace(body.Code)
		if len(code) < 4 {
			writeAPIError(w, http.StatusBadRequest, "code too short", "quickconnect.code_too_short")
			return
		}
		// Approval requires a cookie, as before. Device registration/polling
		// stays public and newly minted Quick Connect sessions stay local-only.
		if c, err := r.Cookie("session"); err != nil || c.Value == "" {
			writeAPIError(w, http.StatusUnauthorized, "login required to approve device", "quickconnect.login_required")
			return
		}
		checked, ok := s.validateRequestSession(w, r)
		if !ok {
			return
		}
		r = checked
		userID, username, tenantID, _, _ := s.sessionPrincipal(r)
		if userID == "" {
			writeAPIError(w, http.StatusUnauthorized, "login required to approve device", "quickconnect.login_required")
			return
		}
		if s.userErased(userID) {
			writeAPIError(w, http.StatusUnauthorized, "login required to approve device", "quickconnect.login_required")
			return
		}
		found, refused := false, false
		err := s.quickconnect.mutate(func(m map[string]qcEntry) bool {
			e, exists := m[code]
			if exists && quickConnectExpired(e) {
				delete(m, code)
				exists = false
			}
			if !exists {
				return false
			}
			if s.quickconnect.erased != nil && s.quickconnect.erased(userID) {
				refused = true
				return false
			}
			found = true
			m[code] = qcEntry{
				Code:      code,
				UserID:    userID,
				Username:  username,
				TenantID:  tenantID,
				CreatedAt: e.CreatedAt,
				Approved:  true,
				Consumed:  false,
			}
			return true
		})
		switch {
		case err != nil:
			writeAPIError(w, http.StatusInternalServerError, "save failed", "quickconnect.save_failed")
			return
		case refused:
			writeAPIError(w, http.StatusUnauthorized, "login required to approve device", "quickconnect.login_required")
			return
		case !found:
			writeAPIError(w, http.StatusNotFound, "code not found or expired", "quickconnect.code_not_found")
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
			writeAPIError(w, http.StatusBadRequest, "code required", "quickconnect.code_required")
			return
		}
		var resp map[string]any
		var failure string
		bestEffort := false // dropping an expired or refused code is not worth a 500
		err := s.quickconnect.mutate(func(m map[string]qcEntry) bool {
			e, ok := m[code]
			if !ok || quickConnectExpired(e) {
				resp = map[string]any{"approved": false, "code": code}
				if ok {
					bestEffort = true
					delete(m, code)
					return true
				}
				return false
			}
			// ADR-0035 §3: this mints a session for e.UserID without that
			// user's bearer. An id in the erasure ledger gets none, and its
			// code goes (the erasure sweep removes the rest).
			if e.UserID != "" && s.quickconnect.erased != nil && s.quickconnect.erased(e.UserID) {
				resp = map[string]any{"approved": false, "code": code}
				bestEffort = true
				delete(m, code)
				return true
			}
			resp = map[string]any{
				"approved":   e.Approved,
				"code":       e.Code,
				"username":   e.Username,
				"user_id":    e.UserID,
				"created_at": e.CreatedAt,
			}
			if e.Approved && e.UserID != "" && !e.Consumed && s.sessions != nil {
				sess, err := s.sessions.CreateWithTenant(e.UserID, e.Username, e.TenantID)
				if err != nil {
					if errors.Is(err, errUserErased) {
						resp = map[string]any{"approved": false, "code": code}
						bestEffort = true
						delete(m, code)
						return true
					}
					failure = "quickconnect.session_error"
					return false
				}
				e.Consumed = true
				m[code] = e
				resp["session_token"] = sess
				resp["consumed"] = true
				return true
			}
			return false
		})
		if failure != "" {
			writeAPIError(w, http.StatusInternalServerError, "session error", failure)
			return
		}
		if err != nil && !bestEffort {
			writeAPIError(w, http.StatusInternalServerError, "save failed", "quickconnect.save_failed")
			return
		}
		writeJSON(w, resp)
	default:
		writeAPIMethodNotAllowed(w)
	}
}

func (s *server) handleQuickConnectRegister(w http.ResponseWriter, r *http.Request, code string) {
	if code == "" {
		code = generateQuickConnectCode()
	}
	if len(code) < 4 {
		writeAPIError(w, http.StatusBadRequest, "code too short", "quickconnect.code_too_short")
		return
	}
	var existing *qcEntry
	err := s.quickconnect.mutate(func(m map[string]qcEntry) bool {
		if e, ok := m[code]; ok && !quickConnectExpired(e) {
			existing = &e
			return false
		}
		m[code] = qcEntry{
			Code:      code,
			CreatedAt: time.Now().UTC(),
			Approved:  false,
		}
		return true
	})
	if existing != nil {
		writeJSON(w, map[string]any{
			"code":     code,
			"approved": existing.Approved,
			"pending":  !existing.Approved,
		})
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "save failed", "quickconnect.save_failed")
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
		writeAPIMethodNotAllowed(w)
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
		writeAPIError(w, http.StatusBadGateway, err.Error(), "music.gateway_error")
		return
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "music.gateway_error")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
