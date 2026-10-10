package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Session persistence (NFR-REL-003, NFR-SEC-005 pattern from admin-ui v0.1.15).
//
// Sessions are keyed by sessionID(token) = hex SHA-256 of the raw cookie /
// bearer token, so neither memory nor disk holds the token a client presents.
// The signed-in user's auth-local token is AES-256-GCM sealed with the session
// ID as associated data. The key comes from MEDIA_UI_SESSION_KEY or is
// generated once into session.key (0600) next to the sessions file.
const (
	envSessionKey             = "MEDIA_UI_SESSION_KEY"
	sessionKeyFileName        = "session.key"
	sessionFileName           = "sessions.json"
	sessionFileFormatVersion  = 1
	defaultSessionTTL         = 24 * time.Hour
	sessionPersistDirFileMode = 0o700
)

type sessionStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	byID map[string]sessionEntry // key: sessionID(raw token)

	// Persistence (empty path = memory only).
	persistMu sync.Mutex
	path      string
	aead      cipher.AEAD

	// erased reports ids present in the last-seen erasure ledger (ADR-0035 §3).
	// nil = no ledger: nothing is refused. A session is never created for an
	// erased id, whatever the caller presents.
	erased func(userID string) bool
}

// errUserErased is returned when a session would be created for a user id the
// identity provider's erasure ledger lists.
var errUserErased = errors.New("user has been erased")

type sessionEntry struct {
	userID    string
	username  string
	tenantID  string
	authToken string
	roles     []string
	expiry    time.Time
}

type persistedBFFSession struct {
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	TenantID     string    `json:"tenant_id,omitempty"`
	Roles        []string  `json:"roles,omitempty"`
	AuthTokenEnc string    `json:"auth_token_enc,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type persistedBFFSessions struct {
	Version  int                             `json:"version"`
	Sessions map[string]*persistedBFFSession `json:"sessions"`
}

// sessionID is the stable, non-reversible identifier for a raw session token.
func sessionID(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// newSessionStore returns a memory-only store (tests, no userdata dir).
func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, byID: make(map[string]sessionEntry)}
}

// newPersistentSessionStore returns a store persisted to path, sealing
// auth-local tokens with key (32 bytes).
func newPersistentSessionStore(path string, ttl time.Duration, key []byte) (*sessionStore, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	s := newSessionStore(ttl)
	s.path = path
	s.aead = aead
	if s.load() {
		if err := s.persist(); err != nil {
			log.Printf("warn: rewrite session file %s: %v", path, err)
		}
	}
	return s, nil
}

// newSessionStoreForDir persists sessions under dir (MEDIA_UI_USERDATA_DIR).
// Without a dir, or when the key cannot be obtained, sessions are memory-only
// (never plaintext on disk).
func newSessionStoreForDir(dir string, ttl time.Duration) *sessionStore {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		log.Printf("warn: MEDIA_UI_USERDATA_DIR unset; BFF sessions are memory-only and end on restart")
		return newSessionStore(ttl)
	}
	key, err := loadOrCreateSessionKey(os.Getenv(envSessionKey), filepath.Join(dir, sessionKeyFileName))
	if err == nil {
		var s *sessionStore
		if s, err = newPersistentSessionStore(filepath.Join(dir, sessionFileName), ttl, key); err == nil {
			return s
		}
	}
	log.Printf("error: BFF session persistence disabled (%v); sessions are memory-only", err)
	return newSessionStore(ttl)
}

// parseSessionKey converts a MEDIA_UI_SESSION_KEY value into a 32-byte key:
// base64 or hex of 32 bytes, otherwise SHA-256 of the value.
func parseSessionKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, errors.New("empty session key")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(v); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	if b, err := hex.DecodeString(v); err == nil && len(b) == 32 {
		return b, nil
	}
	sum := sha256.Sum256([]byte(v))
	return sum[:], nil
}

func loadOrCreateSessionKey(envValue, keyPath string) ([]byte, error) {
	if strings.TrimSpace(envValue) != "" {
		return parseSessionKey(envValue)
	}
	raw, err := os.ReadFile(keyPath) //nolint:gosec // operator data dir
	if err == nil {
		key, perr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if perr != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid session key file %s", keyPath)
		}
		_ = os.Chmod(keyPath, 0o600)
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read session key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), sessionPersistDirFileMode); err != nil {
		return nil, fmt.Errorf("create session key dir: %w", err)
	}
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // operator data dir
	if err != nil {
		if errors.Is(err, fs.ErrExist) { // lost a race with another process
			return loadOrCreateSessionKey("", keyPath)
		}
		return nil, fmt.Errorf("write session key: %w", err)
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("write session key: %w", errors.Join(werr, cerr))
	}
	return key, nil
}

func (s *sessionStore) seal(id, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(id))), nil
}

func (s *sessionStore) open(id, enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("session ciphertext too short")
	}
	pt, err := s.aead.Open(nil, raw[:ns], raw[ns:], []byte(id))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// load reads the sessions file. Expired entries and entries whose token cannot
// be decrypted (key rotated) are dropped. Returns true when the file should be
// rewritten.
func (s *sessionStore) load() bool {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return false
	}
	var f persistedBFFSessions
	if json.Unmarshal(raw, &f) != nil || f.Version != sessionFileFormatVersion {
		log.Printf("discarding unreadable session file %s; users must sign in again", s.path)
		return true
	}
	now := timeNow()
	dropped := false
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range f.Sessions {
		if p == nil || !now.Before(p.ExpiresAt) {
			dropped = true
			continue
		}
		tok, err := s.open(id, p.AuthTokenEnc)
		if err != nil {
			dropped = true
			continue
		}
		s.byID[id] = sessionEntry{
			userID: p.UserID, username: p.Username, tenantID: p.TenantID,
			authToken: tok, roles: p.Roles, expiry: p.ExpiresAt,
		}
	}
	return dropped
}

// persist atomically writes all live sessions (file 0600, dir 0700).
func (s *sessionStore) persist() error {
	if s.path == "" || s.aead == nil {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	live := map[string]sessionEntry{}
	now := timeNow()
	s.mu.Lock()
	for id, e := range s.byID {
		if now.After(e.expiry) {
			delete(s.byID, id)
			continue
		}
		live[id] = e
	}
	s.mu.Unlock()
	return s.writeFileLocked(live)
}

// writeFileLocked seals and atomically writes entries as the sessions file.
// The caller holds persistMu.
func (s *sessionStore) writeFileLocked(entries map[string]sessionEntry) error {
	f := persistedBFFSessions{Version: sessionFileFormatVersion, Sessions: map[string]*persistedBFFSession{}}
	for id, e := range entries {
		enc, err := s.seal(id, e.authToken)
		if err != nil {
			return err
		}
		f.Sessions[id] = &persistedBFFSession{
			UserID: e.userID, Username: e.username, TenantID: e.tenantID,
			Roles: e.roles, AuthTokenEnc: enc, ExpiresAt: e.expiry,
		}
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), sessionPersistDirFileMode); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	_ = os.Remove(tmp) // fresh file so 0600 applies
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *sessionStore) persistOrWarn() {
	if err := s.persist(); err != nil {
		log.Printf("warn: persist BFF sessions: %v", err)
	}
}

func (s *sessionStore) Create(userID, username string) (string, error) {
	return s.CreateWithRoles(userID, username, "", nil)
}

func (s *sessionStore) CreateWithTenant(userID, username, tenantID string) (string, error) {
	return s.CreateWithRoles(userID, username, tenantID, nil)
}

func (s *sessionStore) CreateWithRoles(userID, username, tenantID string, roles []string) (string, error) {
	return s.CreateWithAuth(userID, username, tenantID, roles, "")
}

func (s *sessionStore) CreateWithAuth(userID, username, tenantID string, roles []string, authToken string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b[:])
	s.mu.Lock()
	// Checked under the lock eraseUser takes, so a session is either created
	// before an erasure (and erased by it) or refused after the id is listed.
	if s.erased != nil && userID != "" && s.erased(userID) {
		s.mu.Unlock()
		return "", errUserErased
	}
	s.byID[sessionID(tok)] = sessionEntry{
		userID: userID, username: username, tenantID: strings.TrimSpace(tenantID),
		authToken: strings.TrimSpace(authToken),
		roles:     append([]string(nil), roles...),
		expiry:    timeNow().Add(s.ttl),
	}
	s.mu.Unlock()
	s.persistOrWarn()
	return tok, nil
}

// get returns the live entry for a raw token, dropping it when expired.
func (s *sessionStore) get(tok string) (sessionEntry, bool) {
	if s == nil || tok == "" {
		return sessionEntry{}, false
	}
	id := sessionID(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[id]
	if !ok {
		return sessionEntry{}, false
	}
	if !timeNow().Before(e.expiry) {
		delete(s.byID, id)
		return sessionEntry{}, false
	}
	e.roles = append([]string(nil), e.roles...)
	return e, true
}

func (s *sessionStore) Valid(tok string) bool {
	_, ok := s.get(tok)
	return ok
}

func (s *sessionStore) Delete(tok string) {
	if tok == "" {
		return
	}
	s.mu.Lock()
	_, existed := s.byID[sessionID(tok)]
	delete(s.byID, sessionID(tok))
	s.mu.Unlock()
	if existed {
		s.persistOrWarn()
	}
}

func (s *sessionStore) Lookup(tok string) (userID, username string, ok bool) {
	userID, username, _, ok = s.LookupTenant(tok)
	return userID, username, ok
}

func (s *sessionStore) LookupTenant(tok string) (userID, username, tenantID string, ok bool) {
	e, ok := s.get(tok)
	if !ok {
		return "", "", "", false
	}
	return e.userID, e.username, e.tenantID, true
}

// LookupAuthToken returns the signed-in user's auth-local token for a session.
func (s *sessionStore) LookupAuthToken(tok string) string {
	e, ok := s.get(tok)
	if !ok {
		return ""
	}
	return e.authToken
}

func (s *sessionStore) LookupRoles(tok string) (userID, username, tenantID string, roles []string, ok bool) {
	e, ok := s.get(tok)
	if !ok {
		return "", "", "", nil, false
	}
	return e.userID, e.username, e.tenantID, append([]string(nil), e.roles...), true
}

// Include expiry so a replaced local session cannot inherit an in-flight result.
func sameSessionBinding(a, b sessionEntry) bool {
	return a.userID == b.userID && a.tenantID == b.tenantID && a.authToken == b.authToken && a.expiry.Equal(b.expiry)
}

// deleteIfBound atomically invalidates only the session that was checked.
func (s *sessionStore) deleteIfBound(tok string, expected sessionEntry) bool {
	s.mu.Lock()
	current, exists := s.byID[sessionID(tok)]
	matched := exists && sameSessionBinding(current, expected)
	if matched {
		delete(s.byID, sessionID(tok))
	}
	s.mu.Unlock()
	if matched {
		s.persistOrWarn()
	}
	return matched
}

// commitValidated updates public claims without extending expiry, changing the
// principal/bearer or recreating a deleted session. Persist only changed claims.
func (s *sessionStore) commitValidated(tok string, expected sessionEntry, username string, roles []string) (sessionEntry, bool) {
	s.mu.Lock()
	current, exists := s.byID[sessionID(tok)]
	if !exists || !timeNow().Before(current.expiry) || !sameSessionBinding(current, expected) {
		s.mu.Unlock()
		return sessionEntry{}, false
	}
	changed := current.username != username || !slices.Equal(current.roles, roles)
	current.username = username
	current.roles = append([]string(nil), roles...)
	s.byID[sessionID(tok)] = current
	current.roles = append([]string(nil), current.roles...)
	s.mu.Unlock()
	if changed {
		s.persistOrWarn()
	}
	return current, true
}
