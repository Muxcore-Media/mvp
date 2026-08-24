package main

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Password reset requests — no self-service email in auth-local.
// Consumer posts username; admin resets via Users → Set password.
// Share path with admin-ui via MEDIA_UI_PASSWORD_RESET_FILE / ADMIN_UI_PASSWORD_RESET_FILE.

type passwordResetEntry struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"` // pending | closed
}

type passwordResetFile struct {
	Requests []passwordResetEntry `json:"requests"`
}

type passwordResetStore struct {
	mu   sync.Mutex
	path string
}

func newPasswordResetStore(path, userdataDir string) *passwordResetStore {
	if path == "" {
		if userdataDir != "" {
			path = filepath.Join(userdataDir, "password-resets.json")
		} else {
			path = filepath.Join(os.TempDir(), "muxcore-password-resets.json")
		}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	return &passwordResetStore{path: path}
}

func (s *passwordResetStore) load() passwordResetFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return passwordResetFile{}
	}
	var f passwordResetFile
	if json.Unmarshal(raw, &f) != nil {
		return passwordResetFile{}
	}
	return f
}

func (s *passwordResetStore) save(f passwordResetFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *server) handlePasswordReset(w http.ResponseWriter, r *http.Request) {
	if s.passwordResets == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "password reset disabled", "password_reset.disabled")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Username string `json:"username"`
			Note     string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid json", "password_reset.invalid_json")
			return
		}
		username := strings.TrimSpace(body.Username)
		if username == "" {
			writeAPIError(w, http.StatusBadRequest, "username required", "password_reset.username_required")
			return
		}
		if len(username) > 128 {
			writeAPIError(w, http.StatusBadRequest, "username too long", "password_reset.username_too_long")
			return
		}
		f := s.passwordResets.load()
		id := passwordResetID()
		f.Requests = append(f.Requests, passwordResetEntry{
			ID:        id,
			Username:  username,
			Note:      strings.TrimSpace(body.Note),
			CreatedAt: time.Now().UTC(),
			Status:    "pending",
		})
		if len(f.Requests) > 200 {
			f.Requests = f.Requests[len(f.Requests)-200:]
		}
		if err := s.passwordResets.save(f); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "save failed", "password_reset.save_failed")
			return
		}
		writeJSON(w, map[string]any{
			"ok":      true,
			"id":      id,
			"message": "Request recorded. An administrator can reset your password from Admin → Users. No email is sent unless SMTP is configured.",
		})
	case http.MethodGet:
		if !s.sessionHasPrivilegedRole(r) {
			writeAPIUnauthorized(w)
			return
		}
		f := s.passwordResets.load()
		pending := make([]passwordResetEntry, 0)
		for _, e := range f.Requests {
			if e.Status == "" || e.Status == "pending" {
				pending = append(pending, e)
			}
		}
		writeJSON(w, map[string]any{"requests": pending, "count": len(pending)})
	default:
		writeAPIMethodNotAllowed(w)
	}
}

func passwordResetID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}
