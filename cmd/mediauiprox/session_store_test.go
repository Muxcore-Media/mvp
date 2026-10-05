package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// NFR-REL-003: a session created by one BFF instance is valid in a new
// instance on the same userdata dir; tokens are stored hashed and the
// auth-local token encrypted (file 0600).
func TestSessionStorePersistsAcrossRestart(t *testing.T) {
	t.Setenv(envSessionKey, "")
	dir := t.TempDir()
	a := newSessionStoreForDir(dir, time.Hour)
	if a.path == "" {
		t.Fatal("expected a persistent store")
	}
	tok, err := a.CreateWithAuth("u1", "alice", "home", []string{"admin"}, "auth-local-secret")
	if err != nil {
		t.Fatal(err)
	}

	b := newSessionStoreForDir(dir, time.Hour) // "restart"
	if !b.Valid(tok) {
		t.Fatal("session not valid after restart")
	}
	uid, name, tenant, roles, ok := b.LookupRoles(tok)
	if !ok || uid != "u1" || name != "alice" || tenant != "home" || len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("lookup after restart: %q %q %q %v %v", uid, name, tenant, roles, ok)
	}
	if got := b.LookupAuthToken(tok); got != "auth-local-secret" {
		t.Fatalf("auth token after restart %q", got)
	}

	path := filepath.Join(dir, sessionFileName)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("sessions file mode %v", st.Mode().Perm())
	}
	if kst, err := os.Stat(filepath.Join(dir, sessionKeyFileName)); err != nil || kst.Mode().Perm() != 0o600 {
		t.Fatalf("session key file: %v %v", kst, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), tok) || strings.Contains(string(raw), "auth-local-secret") {
		t.Fatal("raw session token or auth-local token stored in plaintext")
	}
	var f persistedBFFSessions
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Sessions[sessionID(tok)]; !ok {
		t.Fatalf("expected entry keyed by sessionID, got %v", f.Sessions)
	}

	// Logout in the new instance is durable too.
	b.Delete(tok)
	c := newSessionStoreForDir(dir, time.Hour)
	if c.Valid(tok) {
		t.Fatal("deleted session revived after restart")
	}
}

func TestSessionStoreDropsExpiredAndForeignKeyEntries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envSessionKey, "key-one")
	a := newSessionStoreForDir(dir, time.Hour)
	tok, err := a.CreateWithAuth("u1", "alice", "", nil, "tkn")
	if err != nil {
		t.Fatal(err)
	}
	// A different key cannot decrypt the auth token: the session is dropped.
	t.Setenv(envSessionKey, "key-two")
	if newSessionStoreForDir(dir, time.Hour).Valid(tok) {
		t.Fatal("session survived key rotation")
	}

	t.Setenv(envSessionKey, "key-one")
	short := newSessionStoreForDir(t.TempDir(), time.Hour)
	tok2, _ := short.Create("u2", "bob")
	orig := timeNow
	t.Cleanup(func() { timeNow = orig })
	timeNow = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if newSessionStoreForDir(filepath.Dir(short.path), time.Hour).Valid(tok2) {
		t.Fatal("expired session loaded")
	}
}

func TestSessionStoreMemoryOnlyWithoutDir(t *testing.T) {
	s := newSessionStoreForDir("", time.Hour)
	if s.path != "" {
		t.Fatal("expected memory-only store")
	}
	tok, err := s.Create("u", "n")
	if err != nil || !s.Valid(tok) {
		t.Fatal("memory store broken")
	}
}
