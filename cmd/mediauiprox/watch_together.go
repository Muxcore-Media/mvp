package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const watchTogetherTTL = 24 * time.Hour

type watchTogetherRoom struct {
	ID              string  `json:"id"`
	Host            string  `json:"host"`
	HostToken       string  `json:"hostToken,omitempty"`
	MediaID         string  `json:"mediaId"`
	Src             string  `json:"src"`
	Title           string  `json:"title"`
	PositionSeconds float64 `json:"positionSeconds"`
	Playing         bool    `json:"playing"`
	UpdatedAt       string  `json:"updatedAt"`
	YouAreHost      bool    `json:"youAreHost,omitempty"`
}

type watchTogetherStore struct {
	mu    sync.Mutex
	path  string
	rooms map[string]watchTogetherRoom
}

func newWatchTogetherStore(dir string) *watchTogetherStore {
	st := &watchTogetherStore{rooms: map[string]watchTogetherRoom{}}
	if strings.TrimSpace(dir) != "" {
		st.path = filepath.Join(dir, "watch-together.json")
		st.load()
	}
	return st
}

func (st *watchTogetherStore) load() {
	if st.path == "" {
		return
	}
	b, err := os.ReadFile(st.path)
	if err != nil {
		return
	}
	var rooms map[string]watchTogetherRoom
	if json.Unmarshal(b, &rooms) == nil && rooms != nil {
		st.rooms = rooms
	}
}

func (st *watchTogetherStore) persistLocked() {
	if st.path == "" {
		return
	}
	b, err := json.MarshalIndent(st.rooms, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(st.path, b, 0o600)
}

func (st *watchTogetherStore) pruneLocked(now time.Time) {
	for id, room := range st.rooms {
		t, err := time.Parse(time.RFC3339, room.UpdatedAt)
		if err != nil || now.Sub(t) > watchTogetherTTL {
			delete(st.rooms, id)
		}
	}
}

func randomWatchTogetherID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return prefix + time.Now().UTC().Format("150405.000000000")
	}
	return prefix + hex.EncodeToString(b)
}

func (st *watchTogetherStore) create(room watchTogetherRoom) watchTogetherRoom {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.pruneLocked(time.Now())
	if room.ID == "" {
		room.ID = randomWatchTogetherID("wt_")
	}
	if room.HostToken == "" {
		room.HostToken = randomWatchTogetherID("h_")
	}
	room.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	st.rooms[room.ID] = room
	st.persistLocked()
	return room
}

func (st *watchTogetherStore) get(id string) (watchTogetherRoom, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	room, ok := st.rooms[id]
	return room, ok
}

func (st *watchTogetherStore) sync(id string, pos float64, playing bool) (watchTogetherRoom, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	room, ok := st.rooms[id]
	if !ok {
		return watchTogetherRoom{}, false
	}
	room.PositionSeconds = pos
	room.Playing = playing
	room.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	st.rooms[id] = room
	st.persistLocked()
	return room, true
}

func watchTogetherCaller(s *server, r *http.Request) string {
	userID, username, _, _, ok := s.sessionPrincipal(r)
	if ok && strings.TrimSpace(userID) != "" {
		return strings.TrimSpace(userID)
	}
	if ok && strings.TrimSpace(username) != "" {
		return strings.TrimSpace(username)
	}
	if user, _, idOK := s.sessionIdentity(r); idOK && strings.TrimSpace(user) != "" {
		return strings.TrimSpace(user)
	}
	return "household"
}

func publicWatchTogether(room watchTogetherRoom, youAreHost bool) watchTogetherRoom {
	room.HostToken = ""
	room.YouAreHost = youAreHost
	return room
}

func (s *server) isWatchTogetherHost(r *http.Request, room watchTogetherRoom) bool {
	token := strings.TrimSpace(r.Header.Get("X-Watch-Together-Host"))
	if token != "" && token == room.HostToken {
		return true
	}
	return watchTogetherCaller(s, r) == room.Host && room.Host != "household"
}

func (s *server) handleWatchTogether(w http.ResponseWriter, r *http.Request) {
	if s.together == nil {
		s.together = newWatchTogetherStore("")
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/watch-together")
	rest = strings.Trim(rest, "/")

	if rest == "" {
		if r.Method != http.MethodPost {
			writeAPIMethodNotAllowed(w)
			return
		}
		var req struct {
			MediaID         string  `json:"mediaId"`
			Src             string  `json:"src"`
			Title           string  `json:"title"`
			PositionSeconds float64 `json:"positionSeconds"`
			Playing         bool    `json:"playing"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid watch-together", "together.invalid")
			return
		}
		src := strings.TrimSpace(req.Src)
		if src == "" {
			writeAPIError(w, http.StatusBadRequest, "src is required", "together.src_required")
			return
		}
		room := s.together.create(watchTogetherRoom{
			Host:            watchTogetherCaller(s, r),
			MediaID:         strings.TrimSpace(req.MediaID),
			Src:             src,
			Title:           strings.TrimSpace(req.Title),
			PositionSeconds: req.PositionSeconds,
			Playing:         req.Playing,
		})
		room.YouAreHost = true
		writeJSONStatus(w, http.StatusCreated, room)
		return
	}

	id := rest
	if strings.Contains(rest, "/") {
		writeAPIError(w, http.StatusNotFound, "not found", "together.not_found")
		return
	}
	room, ok := s.together.get(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "watch-together room not found", "together.not_found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, publicWatchTogether(room, s.isWatchTogetherHost(r, room)))
	case http.MethodPost:
		if !s.isWatchTogetherHost(r, room) {
			writeAPIError(w, http.StatusForbidden, "only the host can sync this room", "together.not_host")
			return
		}
		var req struct {
			PositionSeconds float64 `json:"positionSeconds"`
			Playing         bool    `json:"playing"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid sync", "together.invalid")
			return
		}
		updated, ok := s.together.sync(id, req.PositionSeconds, req.Playing)
		if !ok {
			writeAPIError(w, http.StatusNotFound, "watch-together room not found", "together.not_found")
			return
		}
		writeJSON(w, publicWatchTogether(updated, true))
	default:
		writeAPIMethodNotAllowed(w)
	}
}
