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
	// ReporterID is the signed-in reporter's user id (ADR-0035 §3: erasure
	// keys on ids, never usernames). Entries written before it existed have
	// none. It is stored for erasure and never returned to clients.
	ReporterID string `json:"reporterId,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

// public is the client view of an issue: the stored reporter id stays server-side.
func (iss mediaIssue) public() mediaIssue {
	iss.ReporterID = ""
	return iss
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
	_ = st.writeLocked(st.all)
}

// writeLocked atomically writes rows as the issues file (nothing for a
// memory-only store). The caller holds st.mu.
func (st *mediaIssueStore) writeLocked(rows []mediaIssue) error {
	if st.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(st.path, b)
}

func (st *mediaIssueStore) list() []mediaIssue {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]mediaIssue, len(st.all))
	for i, iss := range st.all {
		out[i] = iss.public()
	}
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
		who, reporterID := "household", ""
		if id, user, _, _, ok := s.sessionPrincipal(r); ok {
			if s.userErased(id) {
				writeAPIError(w, http.StatusForbidden, "user has been erased", "issues.user_erased")
				return
			}
			if strings.TrimSpace(user) != "" {
				who = user
			}
			reporterID = strings.TrimSpace(id)
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
			ReporterID: reporterID,
			CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		})
		writeJSONStatus(w, http.StatusCreated, iss.public())
	default:
		writeAPIMethodNotAllowed(w)
	}
}
