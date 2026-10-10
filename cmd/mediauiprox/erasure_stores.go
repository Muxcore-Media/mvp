package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ADR-0035 §3: per-store dispositions for the BFF's personal state. Every
// method is keyed by the tombstone's user id (never a username), is idempotent
// (a second call finds nothing), and changes its in-memory copy only after the
// file on disk was replaced (temp file + rename), so a failed write leaves the
// store exactly as it was. The BFF owner (erasure.go) sequences them and
// records the erasure last.

// erasedUserLabel replaces a username that identified an erased user. It is the
// same sentinel the other ADR-0035 owners write.
const erasedUserLabel = "deleted-user"

// writeFileAtomic writes data to path through a same-directory temp file that
// is fsynced and renamed over path, so readers see the old or the new content,
// never a torn file. The file is 0600 and its directory is created 0700.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// tenantEq reports whether a row's provider-assigned tenant is the tombstone's.
// Both come from the identity provider for the same user, so they are compared
// exactly (empty means the single household).
func tenantEq(rowTenant, tombstoneTenant string) bool {
	return strings.TrimSpace(rowTenant) == strings.TrimSpace(tombstoneTenant)
}

// scopeTenantMatches reports whether a userdata scope tenant belongs to the
// tombstone's tenant. Outside TENANT_MODE the scope tenant is always empty (the
// store is not partitioned, so the user id alone identifies the row); inside it
// a tenantless session is scoped "default" (userdata.go scopeFromRequest).
func scopeTenantMatches(scopeTenant, tombstoneTenant string) bool {
	st, tt := strings.TrimSpace(scopeTenant), strings.TrimSpace(tombstoneTenant)
	switch {
	case st == "":
		return true
	case tt == "":
		return st == "default"
	}
	return st == tt
}

// ---- sessions.json (including Quick Connect sessions) ----------------------

// fileHasUser reports whether the persisted sessions file holds a row for the
// user. A missing file has none; an unreadable one is reported as having rows,
// because rewriting it from memory is what heals it.
func (s *sessionStore) fileHasUser(userID, tenantID string) bool {
	if s.path == "" {
		return false
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	var f persistedBFFSessions
	if json.Unmarshal(raw, &f) != nil {
		return true
	}
	for _, p := range f.Sessions {
		if p != nil && p.UserID == userID && tenantEq(p.TenantID, tenantID) {
			return true
		}
	}
	return false
}

// eraseUser deletes every session of the user (cookie sessions, bearer-backed
// sessions and bearer-less Quick Connect sessions) and returns how many it
// removed. Memory changes only after the sessions file was rewritten.
func (s *sessionStore) eraseUser(userID, tenantID string) (int, error) {
	if s == nil || userID == "" {
		return 0, nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	var drop []string
	keep := map[string]sessionEntry{}
	now := timeNow()
	s.mu.Lock()
	for id, e := range s.byID {
		switch {
		case e.userID == userID && tenantEq(e.tenantID, tenantID):
			drop = append(drop, id)
		case !now.After(e.expiry):
			keep[id] = e
		}
	}
	s.mu.Unlock()

	if len(drop) == 0 && !s.fileHasUser(userID, tenantID) {
		return 0, nil
	}
	if s.path != "" && s.aead != nil {
		if err := s.writeFileLocked(keep); err != nil {
			return 0, err
		}
	}
	s.mu.Lock()
	for _, id := range drop {
		delete(s.byID, id)
	}
	s.mu.Unlock()
	return len(drop), nil
}

// countUser counts sessions still carrying the id, in memory and on disk.
func (s *sessionStore) countUser(userID, tenantID string) int {
	if s == nil || userID == "" {
		return 0
	}
	n := 0
	s.mu.Lock()
	for _, e := range s.byID {
		if e.userID == userID && tenantEq(e.tenantID, tenantID) {
			n++
		}
	}
	s.mu.Unlock()
	if s.fileHasUser(userID, tenantID) {
		n++
	}
	return n
}

// usernamesFor returns the usernames the user's live sessions were created
// with, so legacy username-keyed rows can be recognised before the sessions go.
func (s *sessionStore) usernamesFor(userID, tenantID string) []string {
	if s == nil || userID == "" {
		return nil
	}
	var out []string
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.byID {
		if e.userID == userID && tenantEq(e.tenantID, tenantID) && strings.TrimSpace(e.username) != "" {
			out = append(out, strings.TrimSpace(e.username))
		}
	}
	return out
}

// ---- quickconnect.json -------------------------------------------------------

// loadStrict reads the Quick Connect file. A missing file is empty; one that
// exists but cannot be read or parsed is an error (load() treats that as empty,
// which an erasure must not).
func (q *quickConnectStore) loadStrict() (map[string]qcEntry, error) {
	raw, err := os.ReadFile(q.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]qcEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]qcEntry
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("quickconnect.json is not valid JSON: %w", err)
	}
	if m == nil {
		m = map[string]qcEntry{}
	}
	return m, nil
}

func (q *quickConnectStore) saveLocked(m map[string]qcEntry) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(q.path, raw)
}

// eraseUser deletes the codes approved by the user.
func (q *quickConnectStore) eraseUser(userID, tenantID string) (int, error) {
	if q == nil || userID == "" {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	m, err := q.loadStrict()
	if err != nil {
		return 0, err
	}
	removed := 0
	for code, e := range m {
		if e.UserID == userID && tenantEq(e.TenantID, tenantID) {
			delete(m, code)
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}
	if err := q.saveLocked(m); err != nil {
		return 0, err
	}
	return removed, nil
}

// usernamesFor returns the usernames recorded on the user's codes (read only).
func (q *quickConnectStore) usernamesFor(userID, tenantID string) ([]string, error) {
	if q == nil || userID == "" {
		return nil, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	m, err := q.loadStrict()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range m {
		if e.UserID == userID && tenantEq(e.TenantID, tenantID) && strings.TrimSpace(e.Username) != "" {
			out = append(out, strings.TrimSpace(e.Username))
		}
	}
	return out, nil
}

// countUser counts codes still carrying the id.
func (q *quickConnectStore) countUser(userID, tenantID string) (int, error) {
	if q == nil || userID == "" {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	m, err := q.loadStrict()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range m {
		if e.UserID == userID && tenantEq(e.TenantID, tenantID) {
			n++
		}
	}
	return n, nil
}

// ---- watch-together.json -----------------------------------------------------

// eraseHost deletes the rooms hosted by the user. Hosts are stored by user id
// (watchTogetherCaller prefers the session's id), so a room is matched by id.
func (st *watchTogetherStore) eraseHost(userID string) (int, error) {
	if st == nil || userID == "" {
		return 0, nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	next := make(map[string]watchTogetherRoom, len(st.rooms))
	removed := 0
	for id, room := range st.rooms {
		if room.Host == userID {
			removed++
			continue
		}
		next[id] = room
	}
	if removed == 0 {
		return 0, nil
	}
	if err := st.writeLocked(next); err != nil {
		return 0, err
	}
	st.rooms = next
	return removed, nil
}

func (st *watchTogetherStore) countHost(userID string) int {
	if st == nil || userID == "" {
		return 0
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, room := range st.rooms {
		if room.Host == userID {
			n++
		}
	}
	return n
}

// ---- media-issues.json ---------------------------------------------------------

// isLegacyIssueReporter reports whether an issue carries only a username: it
// predates the stored reporter id and names a person rather than the shared
// "household" or the erased-user label.
func isLegacyIssueReporter(iss mediaIssue) bool {
	if iss.ReporterID != "" {
		return false
	}
	switch strings.TrimSpace(iss.ReportedBy) {
	case "", "household", erasedUserLabel:
		return false
	}
	return true
}

// legacyReportersUnaccounted returns the distinct legacy reporter usernames
// that are not in known (the erased user's own usernames).
func (st *mediaIssueStore) legacyReportersUnaccounted(known map[string]struct{}) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	seen := map[string]struct{}{}
	var out []string
	for _, iss := range st.all {
		if !isLegacyIssueReporter(iss) {
			continue
		}
		name := strings.TrimSpace(iss.ReportedBy)
		if _, ok := known[name]; ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// anonymiseReporter replaces the reporter of every issue filed by userID
// (stored reporter id), and of every legacy issue whose username is one of
// known or is absent from live (when live is non-nil), with the erased-user
// label. The issue rows themselves are kept. It returns the number changed.
func (st *mediaIssueStore) anonymiseReporter(userID string, known map[string]struct{}, live map[string]struct{}) (int, error) {
	if st == nil || userID == "" {
		return 0, nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	next := make([]mediaIssue, len(st.all))
	copy(next, st.all)
	changed := 0
	for i, iss := range next {
		hit := iss.ReporterID == userID
		if !hit && isLegacyIssueReporter(iss) {
			name := strings.TrimSpace(iss.ReportedBy)
			if _, ok := known[name]; ok {
				hit = true
			} else if live != nil {
				if _, resolves := live[name]; !resolves {
					hit = true
				}
			}
		}
		if hit {
			next[i].ReportedBy = erasedUserLabel
			next[i].ReporterID = ""
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := st.writeLocked(next); err != nil {
		return 0, err
	}
	st.all = next
	return changed, nil
}

func (st *mediaIssueStore) countReporter(userID string) int {
	if st == nil || userID == "" {
		return 0
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, iss := range st.all {
		if iss.ReporterID == userID {
			n++
		}
	}
	return n
}

// ---- local userdata fallback blobs and provider-migration markers ---------------

// localBlobName is the file name userdata-local's public file store
// (github.com/Muxcore-Media/userdata-local/store) gives a user id. It repeats
// that store's sanitiser; TestEraseUserdataBlobPathMatchesStore pins the two.
func localBlobName(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "anonymous.json"
	}
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(userID) + ".json"
}

// blobPaths lists the fallback blob files that can hold the user's data for
// the tombstone's tenant: the unpartitioned file and the tenant directory the
// scope maps to (scopeTenantMatches).
func (u *serverUserdata) blobPaths(userID, tenantID string) []string {
	name := localBlobName(userID)
	paths := []string{filepath.Join(u.dir, name)}
	tenant := strings.TrimSpace(tenantID)
	if tenant == "" {
		tenant = "default"
	}
	tenantDir := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(tenant)
	return append(paths, filepath.Join(u.dir, "tenants", tenantDir, name))
}

// blobBelongsToUser reports whether the blob file at path is the user's: it
// carries the user's id, or no id at all (files written before the store
// stamped one). A file naming another user (a sanitiser collision) is left.
func blobBelongsToUser(path, userID string) (exists, mine bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return true, false, err
	}
	var rec struct {
		UserID string `json:"user_id"`
	}
	if json.Unmarshal(raw, &rec) != nil {
		// Unparseable: the store's Get already treats it as empty; the file
		// is at the user's own path, so it is theirs to remove.
		return true, true, nil
	}
	return true, rec.UserID == "" || rec.UserID == userID, nil
}

// migrationMarkerFiles returns the marker files recording a decision for the
// user (the marker name is a hash; the user id is in its body).
func (u *serverUserdata) migrationMarkerFiles(userID, tenantID string) ([]string, error) {
	entries, err := os.ReadDir(u.migration.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(u.migration.dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var rec struct {
			UserID   string `json:"user_id"`
			TenantID string `json:"tenant_id"`
		}
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		if rec.UserID == userID && scopeTenantMatches(rec.TenantID, tenantID) {
			out = append(out, path)
		}
	}
	return out, nil
}

// eraseUser removes the user's local fallback blobs and migration markers.
// It holds localMu, the lock every local blob write and marker write takes, so
// no write recreates a file after it was removed.
func (u *serverUserdata) eraseUser(userID, tenantID string) (blobs, markers int, err error) {
	if u == nil || userID == "" {
		return 0, 0, nil
	}
	u.localMu.Lock()
	defer u.localMu.Unlock()
	for _, p := range u.blobPaths(userID, tenantID) {
		exists, mine, err := blobBelongsToUser(p, userID)
		if err != nil {
			return blobs, markers, err
		}
		if !exists || !mine {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return blobs, markers, err
		}
		blobs++
	}
	files, err := u.migrationMarkerFiles(userID, tenantID)
	if err != nil {
		return blobs, markers, err
	}
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return blobs, markers, err
		}
		markers++
	}
	return blobs, markers, nil
}

// countUser counts local blob files and markers still carrying the user.
func (u *serverUserdata) countUser(userID, tenantID string) (int, error) {
	if u == nil || userID == "" {
		return 0, nil
	}
	u.localMu.Lock()
	defer u.localMu.Unlock()
	n := 0
	for _, p := range u.blobPaths(userID, tenantID) {
		exists, mine, err := blobBelongsToUser(p, userID)
		if err != nil {
			return 0, err
		}
		if exists && mine {
			n++
		}
	}
	files, err := u.migrationMarkerFiles(userID, tenantID)
	if err != nil {
		return 0, err
	}
	return n + len(files), nil
}
