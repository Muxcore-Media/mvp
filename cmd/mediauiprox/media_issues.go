package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// mediaIssue is a household playback/library complaint (Jellyfin/Seerr "report issue").
type mediaIssue struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	MediaType  string `json:"mediaType"`
	MediaID    string `json:"mediaId,omitempty"`
	TMDBID     int32  `json:"tmdbId,omitempty"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	ReportedBy string `json:"reportedBy"`
	CreatedAt  string `json:"createdAt"`
}

type mediaIssueStore struct {
	mu   sync.Mutex
	path string
	all  []mediaIssue
}

func newMediaIssueStore(dir string) *mediaIssueStore {
	st := &mediaIssueStore{}
	if strings.TrimSpace(dir) != "" {
		st.path = filepath.Join(dir, "media-issues.json")
		st.load()
	}
	return st
}

func (st *mediaIssueStore) load() {
	if st.path == "" {
		return
	}
	b, err := os.ReadFile(st.path)
	if err != nil {
		return
	}
	var rows []mediaIssue
	if json.Unmarshal(b, &rows) == nil {
		st.all = rows
	}
}

func (st *mediaIssueStore) persistLocked() {
	if st.path == "" {
		return
	}
	b, err := json.MarshalIndent(st.all, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(st.path, b, 0o600)
}

func (st *mediaIssueStore) list() []mediaIssue {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]mediaIssue, len(st.all))
	copy(out, st.all)
	return out
}

func (st *mediaIssueStore) add(iss mediaIssue) mediaIssue {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.all = append([]mediaIssue{iss}, st.all...)
	st.persistLocked()
	return iss
}

func normalizeIssueKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "video", "audio", "subtitles", "wrong", "other":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "other"
	}
}

func (s *server) handleMediaIssues(w http.ResponseWriter, r *http.Request) {
	if s.issues == nil {
		s.issues = newMediaIssueStore("")
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"items": s.issues.list()})
	case http.MethodPost:
		var req struct {
			Kind      string `json:"kind"`
			MediaType string `json:"mediaType"`
			MediaID   string `json:"mediaId"`
			TMDBID    int32  `json:"tmdbId"`
			Title     string `json:"title"`
			Message   string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid issue", "issues.invalid")
			return
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			writeAPIError(w, http.StatusBadRequest, "title is required", "issues.title_required")
			return
		}
		who := "household"
		if user, _, ok := s.sessionIdentity(r); ok && strings.TrimSpace(user) != "" {
			who = user
		}
		iss := s.issues.add(mediaIssue{
			ID:         "iss_" + time.Now().UTC().Format("20060102150405.000000000"),
			Kind:       normalizeIssueKind(req.Kind),
			MediaType:  strings.ToLower(strings.TrimSpace(req.MediaType)),
			MediaID:    strings.TrimSpace(req.MediaID),
			TMDBID:     req.TMDBID,
			Title:      title,
			Message:    strings.TrimSpace(req.Message),
			ReportedBy: who,
			CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		})
		writeJSONStatus(w, http.StatusCreated, iss)
	default:
		writeAPIMethodNotAllowed(w)
	}
}
