package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

func (s *passwordResetStore) pending() []passwordResetEntry {
	f := s.load()
	out := make([]passwordResetEntry, 0)
	for _, e := range f.Requests {
		if e.Status == "" || e.Status == "pending" {
			out = append(out, e)
		}
	}
	return out
}

func (s *passwordResetStore) pendingByID(id string) (passwordResetEntry, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return passwordResetEntry{}, false
	}
	for _, e := range s.pending() {
		if e.ID == id {
			return e, true
		}
	}
	return passwordResetEntry{}, false
}

func (s *passwordResetStore) setStatus(id, status string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("request id required")
	}
	f := s.load()
	found := false
	for i := range f.Requests {
		if f.Requests[i].ID != id {
			continue
		}
		if f.Requests[i].Status == "" || f.Requests[i].Status == "pending" {
			f.Requests[i].Status = status
			found = true
		}
		break
	}
	if !found {
		return errors.New("pending request not found")
	}
	return s.save(f)
}

func (s *passwordResetStore) resolveUsername(username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil
	}
	f := s.load()
	changed := false
	for i := range f.Requests {
		if f.Requests[i].Username != username {
			continue
		}
		if f.Requests[i].Status == "" || f.Requests[i].Status == "pending" {
			f.Requests[i].Status = "resolved"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save(f)
}

func publicPasswordReset(e passwordResetEntry, userID string) map[string]any {
	created := ""
	if !e.CreatedAt.IsZero() {
		created = e.CreatedAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"id":         e.ID,
		"username":   e.Username,
		"note":       e.Note,
		"created_at": created,
		"user_id":    userID,
		"user":       userID != "",
	}
}

func (s *server) passwordResetUserIDs(r *http.Request) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(s.authInternal) == "" {
		return out
	}
	raw, status, err := s.proxyAuthInvites(r, http.MethodGet, s.authInternal+"/api/users", nil)
	if err != nil || status < 200 || status >= 300 {
		return out
	}
	var payload struct {
		Users []map[string]any `json:"users"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return out
	}
	for _, u := range payload.Users {
		name := asString(u["username"])
		id := asString(u["id"])
		if name != "" && id != "" {
			out[name] = id
		}
	}
	return out
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
			"message": "Request recorded. An administrator can reset your password from Settings → Users. No email is sent unless SMTP is configured.",
		})
	case http.MethodGet:
		if !s.sessionHasPrivilegedRole(r) {
			writeAPIUnauthorized(w)
			return
		}
		users := s.passwordResetUserIDs(r)
		pending := s.passwordResets.pending()
		out := make([]map[string]any, 0, len(pending))
		for _, e := range pending {
			out = append(out, publicPasswordReset(e, users[e.Username]))
		}
		writeJSON(w, map[string]any{"available": true, "requests": out, "count": len(out)})
	default:
		writeAPIMethodNotAllowed(w)
	}
}

func (s *server) handleDismissPasswordReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "password_reset.forbidden"})
		return
	}
	if s.passwordResets == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "password reset disabled", "code": "password_reset.disabled"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := s.passwordResets.setStatus(id, "dismissed"); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": "password_reset.dismiss_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id, "status": "dismissed"})
}

func (s *server) handleSetPasswordReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "password_reset.forbidden"})
		return
	}
	if s.passwordResets == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "password reset disabled", "code": "password_reset.disabled"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	entry, ok := s.passwordResets.pendingByID(id)
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "pending request not found", "code": "password_reset.not_found"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "password_reset.invalid_json"})
		return
	}
	password := strings.TrimSpace(body.Password)
	if len(password) < 8 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "password must be at least 8 characters", "code": "password_reset.password_required"})
		return
	}
	users := s.passwordResetUserIDs(r)
	userID := users[entry.Username]
	if userID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "no matching user account", "code": "password_reset.no_user"})
		return
	}
	payload, _ := json.Marshal(map[string]any{"password": password})
	raw, status, err := s.proxyAuthInvites(r, http.MethodPost, s.authInternal+"/api/users/"+url.PathEscape(userID)+"/password", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "password_reset.auth_unavailable"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONStatus(w, status, map[string]any{"error": strings.TrimSpace(string(raw)), "code": "password_reset.set_failed"})
		return
	}
	if err := s.passwordResets.resolveUsername(entry.Username); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "password_reset.resolve_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id, "user_id": userID, "status": "resolved"})
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
