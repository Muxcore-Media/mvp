package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure/erasuretest"
	"github.com/Muxcore-Media/userdata-local/store"
)

const (
	erasureTestVictim    = "u-victim-0123456789abcdef"
	erasureTestBystander = "u-bystander-0123456789abcdef"
	erasureTestVictimTok = "victimuser"
	erasureTestOtherName = "bystanderuser"
)

// fakeUsers is the identity provider's user directory.
type fakeUsers struct {
	mu    sync.Mutex
	users []*authv1.UserInfo
	err   error
	calls int
}

func (f *fakeUsers) ListUsers(context.Context) ([]*authv1.UserInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.users, f.err
}

func (f *fakeUsers) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// erasureFixture wires the BFF's real file-backed stores under one directory.
type erasureFixture struct {
	t     *testing.T
	dir   string
	s     *server
	users *fakeUsers
	owner *bffErasureOwner
}

func newErasureFixture(t *testing.T) *erasureFixture {
	t.Helper()
	dir := t.TempDir()
	sessions, err := newPersistentSessionStore(filepath.Join(dir, sessionFileName), time.Hour, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := &server{
		sessions:       sessions,
		quickconnect:   newQuickConnectStore(dir),
		userdata:       newServerUserdata(dir, nil),
		issues:         newMediaIssueStore(dir),
		together:       newWatchTogetherStore(dir),
		passwordResets: newPasswordResetStore("", dir),
	}
	users := &fakeUsers{users: []*authv1.UserInfo{
		{Id: erasureTestBystander, Username: erasureTestOtherName},
		{Id: "u-admin", Username: "admin"},
	}}
	return &erasureFixture{t: t, dir: dir, s: s, users: users, owner: s.newBFFErasureOwner("media-ui", dir, users)}
}

// seed gives the victim and a bystander one row in every BFF store and returns
// the bystander's session token.
func (f *erasureFixture) seed() (bystanderTok string) {
	f.t.Helper()
	mustTok := func(tok string, err error) string {
		f.t.Helper()
		if err != nil {
			f.t.Fatal(err)
		}
		return tok
	}
	// Sessions: a bearer-backed one and a Quick Connect one for the victim.
	mustTok(f.s.sessions.CreateWithAuth(erasureTestVictim, erasureTestVictimTok, "", []string{"user"}, "victim-bearer"))
	mustTok(f.s.sessions.CreateWithTenant(erasureTestVictim, erasureTestVictimTok, ""))
	bystanderTok = mustTok(f.s.sessions.CreateWithAuth(erasureTestBystander, erasureTestOtherName, "", []string{"user"}, "other-bearer"))

	// Quick Connect codes.
	if err := f.s.quickconnect.save(map[string]qcEntry{
		"111111": {Code: "111111", UserID: erasureTestVictim, Username: erasureTestVictimTok, CreatedAt: time.Now().UTC(), Approved: true},
		"222222": {Code: "222222", UserID: erasureTestBystander, Username: erasureTestOtherName, CreatedAt: time.Now().UTC(), Approved: true},
		"333333": {Code: "333333", CreatedAt: time.Now().UTC()}, // unapproved: no user
	}); err != nil {
		f.t.Fatal(err)
	}

	// Fallback blobs (household file and TENANT_MODE tenant dir) and markers.
	blob := store.Blob{Favorites: map[string]json.RawMessage{"m1": json.RawMessage(`{"updatedAt":"2026-01-01"}`)}}
	for _, scope := range []store.Scope{
		{UserID: erasureTestVictim}, {TenantID: "default", UserID: erasureTestVictim},
		{UserID: erasureTestBystander}, {TenantID: "default", UserID: erasureTestBystander},
	} {
		if _, err := f.s.userdata.store.Put(scope, blob); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, uid := range []string{erasureTestVictim, erasureTestBystander} {
		if err := f.s.userdata.migration.mark(store.Scope{UserID: uid}, migrationMigrated); err != nil {
			f.t.Fatal(err)
		}
	}

	// Watch-together rooms.
	f.s.together.create(watchTogetherRoom{Host: erasureTestVictim, Src: "/stream/a", Title: "victim room"})
	f.s.together.create(watchTogetherRoom{Host: erasureTestBystander, Src: "/stream/b", Title: "other room"})

	// Issues: by id, legacy by the victim's username, legacy by a live user,
	// legacy by an account that no longer exists, and anonymous.
	for _, iss := range []mediaIssue{
		{ID: "iss_id", Title: "by id", ReportedBy: erasureTestVictimTok, ReporterID: erasureTestVictim},
		{ID: "iss_legacy_victim", Title: "legacy victim", ReportedBy: erasureTestVictimTok},
		{ID: "iss_other", Title: "other", ReportedBy: erasureTestOtherName, ReporterID: erasureTestBystander},
		{ID: "iss_legacy_live", Title: "legacy live", ReportedBy: erasureTestOtherName},
		{ID: "iss_legacy_gone", Title: "legacy gone", ReportedBy: "long-gone-user"},
		{ID: "iss_household", Title: "anonymous", ReportedBy: "household"},
	} {
		f.s.issues.add(iss)
	}

	// admin-ui's file: this owner must leave it alone.
	f.writePasswordResets()
	return bystanderTok
}

func (f *erasureFixture) writePasswordResets() {
	f.t.Helper()
	raw, _ := json.MarshalIndent(passwordResetFile{Requests: []passwordResetEntry{
		{ID: "r1", Username: erasureTestVictimTok, UserID: erasureTestVictim, Status: "pending"},
		{ID: "r2", Username: erasureTestVictimTok, Status: "pending"},
		{ID: "r3", Username: erasureTestOtherName, UserID: erasureTestBystander, Status: "pending"},
	}}, "", "  ")
	if err := os.WriteFile(filepath.Join(f.dir, "password-resets.json"), raw, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *erasureFixture) file(name string) []byte {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return raw
}

func (f *erasureFixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.dir, rel))
	return err == nil
}

func (f *erasureFixture) issueByID(id string) (mediaIssue, bool) {
	for _, iss := range f.s.issues.all {
		if iss.ID == id {
			return iss, true
		}
	}
	return mediaIssue{}, false
}

func (f *erasureFixture) tomb(erasureID string) erasure.Tombstone {
	return erasure.Tombstone{ErasureID: erasureID, UserID: erasureTestVictim, DeletedAt: time.Now().UTC()}
}

// requireVictimGoneBystanderIntact is the disposition's positive and negative
// check in one: every victim row removed or anonymised, every other row intact.
func (f *erasureFixture) requireVictimGoneBystanderIntact(bystanderTok string) {
	f.t.Helper()
	t := f.t
	// Sessions, in memory and on disk.
	if n := f.s.sessions.countUser(erasureTestVictim, ""); n != 0 {
		t.Errorf("victim sessions remaining = %d", n)
	}
	if _, _, ok := f.s.sessions.Lookup(bystanderTok); !ok {
		t.Error("bystander session was removed")
	}
	if strings.Contains(string(f.file(sessionFileName)), erasureTestVictim) {
		t.Error("sessions.json still names the victim")
	}
	if !strings.Contains(string(f.file(sessionFileName)), erasureTestBystander) {
		t.Error("sessions.json lost the bystander")
	}
	// Quick Connect codes.
	qc := f.s.quickconnect.load()
	if _, ok := qc["111111"]; ok {
		t.Error("victim Quick Connect code remains")
	}
	if _, ok := qc["222222"]; !ok {
		t.Error("bystander Quick Connect code was removed")
	}
	if _, ok := qc["333333"]; !ok {
		t.Error("unapproved Quick Connect code was removed")
	}
	// Local blobs and markers.
	for _, rel := range []string{erasureTestVictim + ".json", "tenants/default/" + erasureTestVictim + ".json"} {
		if f.exists(rel) {
			t.Errorf("victim blob %s remains", rel)
		}
	}
	for _, rel := range []string{erasureTestBystander + ".json", "tenants/default/" + erasureTestBystander + ".json"} {
		if !f.exists(rel) {
			t.Errorf("bystander blob %s was removed", rel)
		}
	}
	if f.s.userdata.migration.state(store.Scope{UserID: erasureTestVictim}) != "" {
		t.Error("victim migration marker remains")
	}
	if f.s.userdata.migration.state(store.Scope{UserID: erasureTestBystander}) != migrationMigrated {
		t.Error("bystander migration marker was removed")
	}
	// Rooms.
	if n := f.s.together.countHost(erasureTestVictim); n != 0 {
		t.Errorf("victim rooms remaining = %d", n)
	}
	if n := f.s.together.countHost(erasureTestBystander); n != 1 {
		t.Errorf("bystander rooms = %d, want 1", n)
	}
	// Issues: rows stay; the victim's reporter is anonymised.
	if len(f.s.issues.all) != 6 {
		t.Errorf("issue rows = %d, want 6 (rows are never deleted)", len(f.s.issues.all))
	}
	for _, id := range []string{"iss_id", "iss_legacy_victim", "iss_legacy_gone"} {
		iss, ok := f.issueByID(id)
		if !ok || iss.ReportedBy != erasedUserLabel || iss.ReporterID != "" {
			t.Errorf("issue %s = %+v, want anonymised", id, iss)
		}
	}
	for id, want := range map[string]string{"iss_other": erasureTestOtherName, "iss_legacy_live": erasureTestOtherName, "iss_household": "household"} {
		iss, ok := f.issueByID(id)
		if !ok || iss.ReportedBy != want {
			t.Errorf("issue %s = %+v, want reportedBy %q untouched", id, iss, want)
		}
	}
	if iss, _ := f.issueByID("iss_other"); iss.ReporterID != erasureTestBystander {
		t.Errorf("bystander issue lost its reporter id: %+v", iss)
	}
	if strings.Contains(string(f.file("media-issues.json")), erasureTestVictimTok) {
		t.Error("media-issues.json still names the victim's username")
	}
}

func TestEraseDeletesVictimRowsAndKeepsBystanders(t *testing.T) {
	f := newErasureFixture(t)
	bystanderTok := f.seed()
	resetsBefore := f.file("password-resets.json")

	counts, err := f.owner.Apply(context.Background(), f.tomb("er-1"))
	if err != nil {
		t.Fatal(err)
	}
	want := erasure.Counts{
		"sessions": 2, "quickconnect_codes": 1, "userdata_blobs": 2, "migration_markers": 1,
		"watch_together_rooms": 1, "media_issues_anonymised": 3,
	}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("counts[%s] = %d, want %d (counts %v)", k, counts[k], v, counts)
		}
	}
	for k := range counts {
		if !erasure.ValidDetailCode(k) {
			t.Errorf("counts key %q is not an acknowledgeable key", k)
		}
	}
	f.requireVictimGoneBystanderIntact(bystanderTok)

	if left, err := f.owner.Verify(context.Background(), f.tomb("er-1")); err != nil || left != 0 {
		t.Errorf("Verify = %d, %v; want 0 remaining", left, err)
	}
	if ok, err := f.owner.Applied(context.Background(), "er-1"); err != nil || !ok {
		t.Errorf("Applied = %v, %v; want true", ok, err)
	}
	// admin-ui is the single eraser of password-resets.json (ADR-0035 §3).
	if !bytes.Equal(resetsBefore, f.file("password-resets.json")) {
		t.Errorf("password-resets.json changed:\n%s", f.file("password-resets.json"))
	}
	// The record holds opaque ids and times only.
	rec := string(f.file(erasureAppliedFileName))
	if !strings.Contains(rec, "er-1") || strings.Contains(rec, erasureTestVictim) || strings.Contains(rec, erasureTestVictimTok) {
		t.Errorf("applied record = %s", rec)
	}
	if info, err := os.Stat(filepath.Join(f.dir, erasureAppliedFileName)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("applied record mode = %v, %v; want 0600", info, err)
	}
}

func TestEraseAppliedTwiceIsNoOp(t *testing.T) {
	f := newErasureFixture(t)
	f.seed()
	if _, err := f.owner.Apply(context.Background(), f.tomb("er-1")); err != nil {
		t.Fatal(err)
	}
	// A row that appears after the first application (a restore that did not
	// carry the record is a different case) must survive a replay of the same
	// erasure id: replaying a tombstone is a no-op, not a second sweep.
	if _, err := f.s.userdata.store.Put(store.Scope{UserID: erasureTestVictim}, store.Blob{Prefs: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	snap := map[string][]byte{}
	for _, n := range []string{sessionFileName, "quickconnect.json", "media-issues.json", "watch-together.json", erasureAppliedFileName, erasureTestVictim + ".json"} {
		snap[n] = f.file(n)
	}
	counts, err := f.owner.Apply(context.Background(), f.tomb("er-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Errorf("second Apply counts = %v, want none", counts)
	}
	for n, before := range snap {
		if !bytes.Equal(before, f.file(n)) {
			t.Errorf("%s changed on a replayed tombstone", n)
		}
	}
	// A different erasure id for the same user is a normal application.
	if _, err := f.owner.Apply(context.Background(), f.tomb("er-2")); err != nil {
		t.Fatal(err)
	}
	if f.exists(erasureTestVictim + ".json") {
		t.Error("a new erasure id did not remove the re-created blob")
	}
}

func TestEraseFailedWriteIsNotRecordedAndRetries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		block  string // a directory placed where the store's temp/target file goes
		detail string
	}{
		{"media-issues", "media-issues.json", erasureDetailIssues},
		{"watch-together", "watch-together.json", erasureDetailRooms},
		{"quickconnect", "quickconnect.json", erasureDetailQuickConnect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newErasureFixture(t)
			bystanderTok := f.seed()
			path := filepath.Join(f.dir, tc.block)
			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// A directory cannot be renamed over by a file: the write fails.
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err = f.owner.Apply(context.Background(), f.tomb("er-1"))
			if err == nil {
				t.Fatal("Apply succeeded although a store could not be written")
			}
			if !strings.Contains(err.Error(), tc.block) {
				t.Errorf("error = %v, want it to name %s", err, tc.block)
			}
			if ok, _ := f.owner.Applied(context.Background(), "er-1"); ok {
				t.Fatal("a failed write recorded the erasure as applied")
			}
			if f.exists(erasureAppliedFileName) {
				t.Fatal("applied record file exists after a failed application")
			}

			// Heal the store; the next sweep completes and then records.
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, saved, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.owner.Apply(context.Background(), f.tomb("er-1")); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if ok, _ := f.owner.Applied(context.Background(), "er-1"); !ok {
				t.Error("completed retry was not recorded")
			}
			f.requireVictimGoneBystanderIntact(bystanderTok)
			if left, err := f.owner.Verify(context.Background(), f.tomb("er-1")); err != nil || left != 0 {
				t.Errorf("Verify after retry = %d, %v", left, err)
			}
		})
	}
}

func TestEraseFailedSessionPersistLeavesMemoryAndRecordUntouched(t *testing.T) {
	f := newErasureFixture(t)
	f.seed()
	victimTok, err := f.s.sessions.CreateWithTenant(erasureTestVictim, erasureTestVictimTok, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dir, sessionFileName)
	// A directory cannot be renamed over by a file: the write fails.
	raw, _ := os.ReadFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = f.owner.Apply(context.Background(), f.tomb("er-1"))
	if err == nil {
		t.Fatal("Apply succeeded although sessions.json could not be written")
	}
	if _, _, ok := f.s.sessions.Lookup(victimTok); !ok {
		t.Error("memory changed although the file was not written")
	}
	if ok, _ := f.owner.Applied(context.Background(), "er-1"); ok {
		t.Error("failed application recorded")
	}
	_ = os.Remove(path)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEraseLegacyIssuesResolveThroughLiveUsers(t *testing.T) {
	t.Run("lookup failure fails closed and changes nothing", func(t *testing.T) {
		f := newErasureFixture(t)
		f.seed()
		f.users.err = errors.New("provider unreachable")
		_, err := f.owner.Apply(context.Background(), f.tomb("er-1"))
		if err == nil {
			t.Fatal("Apply succeeded without a user directory")
		}
		if n := f.s.sessions.countUser(erasureTestVictim, ""); n == 0 {
			t.Error("sessions were erased although the lookup failed first")
		}
		if iss, _ := f.issueByID("iss_id"); iss.ReportedBy == erasedUserLabel {
			t.Error("issues were anonymised although the lookup failed first")
		}
		if ok, _ := f.owner.Applied(context.Background(), "er-1"); ok {
			t.Error("failed application recorded")
		}
	})
	t.Run("an empty directory is refused", func(t *testing.T) {
		f := newErasureFixture(t)
		f.seed()
		f.users.users = nil
		if _, err := f.owner.Apply(context.Background(), f.tomb("er-1")); err == nil {
			t.Fatal("Apply acted on an empty user directory (it would anonymise every legacy issue)")
		}
		if iss, _ := f.issueByID("iss_legacy_live"); iss.ReportedBy != erasureTestOtherName {
			t.Errorf("legacy issue of a live user changed: %+v", iss)
		}
	})
	t.Run("no legacy rows needs no lookup", func(t *testing.T) {
		f := newErasureFixture(t)
		if _, err := f.s.sessions.CreateWithAuth(erasureTestVictim, erasureTestVictimTok, "", nil, "b"); err != nil {
			t.Fatal(err)
		}
		f.s.issues.add(mediaIssue{ID: "iss_new", Title: "new", ReportedBy: erasureTestVictimTok, ReporterID: erasureTestVictim})
		f.users.err = errors.New("must not be asked")
		if _, err := f.owner.Apply(context.Background(), f.tomb("er-1")); err != nil {
			t.Fatalf("Apply = %v; id-keyed rows must not depend on the user directory", err)
		}
		if f.users.callCount() != 0 {
			t.Error("user directory was consulted although no legacy row needed it")
		}
	})
}

func TestEraseTenantMismatchLeavesRows(t *testing.T) {
	f := newErasureFixture(t)
	if _, err := f.s.sessions.CreateWithAuth(erasureTestVictim, erasureTestVictimTok, "tenant-b", nil, "b"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.quickconnect.save(map[string]qcEntry{
		"444444": {Code: "444444", UserID: erasureTestVictim, TenantID: "tenant-b", CreatedAt: time.Now().UTC(), Approved: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.userdata.store.Put(store.Scope{TenantID: "tenant-b", UserID: erasureTestVictim}, store.Blob{Prefs: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	tomb := f.tomb("er-1")
	tomb.TenantID = "tenant-a"
	counts, err := f.owner.Apply(context.Background(), tomb)
	if err != nil {
		t.Fatal(err)
	}
	if counts["sessions"] != 0 || counts["quickconnect_codes"] != 0 || counts["userdata_blobs"] != 0 {
		t.Errorf("another tenant's rows were erased: %v", counts)
	}
	if !f.exists("tenants/tenant-b/" + erasureTestVictim + ".json") {
		t.Error("tenant-b blob removed by a tenant-a tombstone")
	}
	// The matching tenant erases them.
	tomb = f.tomb("er-2")
	tomb.TenantID = "tenant-b"
	counts, err = f.owner.Apply(context.Background(), tomb)
	if err != nil {
		t.Fatal(err)
	}
	if counts["sessions"] != 1 || counts["quickconnect_codes"] != 1 || counts["userdata_blobs"] != 1 {
		t.Errorf("tenant-b erasure counts = %v", counts)
	}
}

// TestEraseUserdataBlobPathMatchesStore pins localBlobName to the file store
// the BFF writes through, for ids a sanitiser could mangle.
func TestEraseUserdataBlobPathMatchesStore(t *testing.T) {
	// Tombstone ids never have surrounding space (the SDK rejects them).
	for _, id := range []string{erasureTestVictim, "oidc|sub/with/slash", `back\slash`, "dots..inside"} {
		for _, tenant := range []string{"", "household", "ten/ant"} {
			dir := t.TempDir()
			u := newServerUserdata(dir, nil)
			scope := store.Scope{UserID: id}
			if tenant != "" {
				scope.TenantID = tenant
			}
			if _, err := u.store.Put(scope, store.Blob{Prefs: json.RawMessage(`{"a":1}`)}); err != nil {
				t.Fatal(err)
			}
			if n, err := u.countUser(id, tenant); err != nil || n != 1 {
				t.Errorf("id %q tenant %q: countUser = %d, %v; the store's file was not found", id, tenant, n, err)
			}
			blobs, _, err := u.eraseUser(id, tenant)
			if err != nil || blobs != 1 {
				t.Errorf("id %q tenant %q: eraseUser = %d, %v", id, tenant, blobs, err)
			}
			if got := u.store.Get(scope); len(got.Prefs) != 0 {
				t.Errorf("id %q tenant %q: blob still readable after erasure", id, tenant)
			}
		}
	}
}

func TestEraseBlobOfAnotherUserWithCollidingNameIsKept(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir, nil)
	// "a/b" and "a_b" share a file name; the file belongs to a_b.
	if _, err := u.store.Put(store.Scope{UserID: "a_b"}, store.Blob{Prefs: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	blobs, _, err := u.eraseUser("a/b", "")
	if err != nil || blobs != 0 {
		t.Fatalf("eraseUser(a/b) = %d, %v; must not delete a_b's blob", blobs, err)
	}
	if n, _ := u.countUser("a/b", ""); n != 0 {
		t.Errorf("countUser(a/b) = %d; another user's blob is not a victim row", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "a_b.json")); err != nil {
		t.Errorf("a_b's blob was removed: %v", err)
	}
}

func TestErasedUserCannotBeWrittenFor(t *testing.T) {
	f := newErasureFixture(t)
	erased := map[string]bool{erasureTestVictim: true}
	f.s.setErasedCheck(func(id string) bool { return erased[id] })

	// sessions: no session is minted for an erased id.
	if _, err := f.s.sessions.CreateWithTenant(erasureTestVictim, "x", ""); !errors.Is(err, errUserErased) {
		t.Errorf("session for erased id: err = %v, want errUserErased", err)
	}
	if _, err := f.s.sessions.CreateWithTenant(erasureTestBystander, "y", ""); err != nil {
		t.Errorf("session for another id: %v", err)
	}
	// local blob store: an admin override cannot re-create an erased user's blob.
	if _, err := f.s.userdata.localPut(store.Scope{UserID: erasureTestVictim}, store.Blob{Prefs: json.RawMessage(`{}`)}); err == nil {
		t.Error("local blob written for an erased id")
	}
	if f.exists(erasureTestVictim + ".json") {
		t.Error("blob file exists for an erased id")
	}
	// markers are not re-created either.
	f.s.userdata.recordMigration(store.Scope{UserID: erasureTestVictim}, migrationNothing)
	if f.s.userdata.migration.state(store.Scope{UserID: erasureTestVictim}) != "" {
		t.Error("migration marker recorded for an erased id")
	}
	// password-reset requests do not store an erased id.
	f.s.resetUsers = fakeResolver{erasureTestVictimTok: erasureTestVictim, erasureTestOtherName: erasureTestBystander}
	if got := f.s.resolveResetUserID(context.Background(), erasureTestVictimTok); got != "" {
		t.Errorf("resolveResetUserID(erased) = %q, want none", got)
	}
	if got := f.s.resolveResetUserID(context.Background(), erasureTestOtherName); got != erasureTestBystander {
		t.Errorf("resolveResetUserID(bystander) = %q", got)
	}
}

type fakeResolver map[string]string

func (r fakeResolver) ResolveUserID(_ context.Context, username string) string { return r[username] }

func TestQuickConnectIngestRefusesErasedID(t *testing.T) {
	f := newErasureFixture(t)
	erased := map[string]bool{}
	f.s.setErasedCheck(func(id string) bool { return erased[id] })
	if err := f.s.quickconnect.save(map[string]qcEntry{
		"555555": {Code: "555555", UserID: erasureTestVictim, Username: erasureTestVictimTok, CreatedAt: time.Now().UTC(), Approved: true},
		"666666": {Code: "666666", UserID: erasureTestBystander, Username: erasureTestOtherName, CreatedAt: time.Now().UTC(), Approved: true},
	}); err != nil {
		t.Fatal(err)
	}
	erased[erasureTestVictim] = true

	poll := func(code string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		f.s.handleQuickConnect(w, httptest.NewRequest(http.MethodGet, "/api/quickconnect?code="+code, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("poll %s: status %d body %s", code, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	got := poll("555555")
	if got["approved"] == true || got["session_token"] != nil {
		t.Fatalf("erased id's code minted a session: %v", got)
	}
	if n := f.s.sessions.countUser(erasureTestVictim, ""); n != 0 {
		t.Errorf("sessions for erased id = %d", n)
	}
	if _, still := f.s.quickconnect.load()["555555"]; still {
		t.Error("erased id's code was not dropped")
	}
	// Another user's code still works.
	got = poll("666666")
	if got["session_token"] == nil {
		t.Errorf("bystander's code did not mint a session: %v", got)
	}
}

func TestQuickConnectApprovalByErasedSessionIsRefused(t *testing.T) {
	f := newErasureFixture(t)
	erased := map[string]bool{}
	f.s.setErasedCheck(func(id string) bool { return erased[id] })
	tok, err := f.s.sessions.CreateWithTenant(erasureTestVictim, erasureTestVictimTok, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.quickconnect.save(map[string]qcEntry{
		"777777": {Code: "777777", CreatedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	erased[erasureTestVictim] = true
	req := httptest.NewRequest(http.MethodPost, "/api/quickconnect", strings.NewReader(`{"code":"777777"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	f.s.handleQuickConnect(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body %s, want 401", w.Code, w.Body.String())
	}
	if e := f.s.quickconnect.load()["777777"]; e.Approved || e.UserID != "" {
		t.Errorf("code approved on behalf of an erased id: %+v", e)
	}
}

func TestBearerlessSessionOfErasedIDIsRejected(t *testing.T) {
	f := newErasureFixture(t)
	erased := map[string]bool{}
	f.s.setErasedCheck(func(id string) bool { return erased[id] })
	tok, err := f.s.sessions.CreateWithTenant(erasureTestVictim, erasureTestVictimTok, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.s.sessions.CreateWithTenant(erasureTestBystander, erasureTestOtherName, "")
	if err != nil {
		t.Fatal(err)
	}
	check := func(tok string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		if _, ok := f.s.validateRequestSession(w, req); ok {
			return http.StatusOK
		}
		return w.Code
	}
	if code := check(tok); code != http.StatusOK {
		t.Fatalf("session before erasure: %d", code)
	}
	erased[erasureTestVictim] = true
	if code := check(tok); code != http.StatusUnauthorized {
		t.Errorf("Quick Connect session of an erased id: %d, want 401", code)
	}
	if f.s.sessions.Valid(tok) {
		t.Error("rejected session is still stored")
	}
	if code := check(other); code != http.StatusOK {
		t.Errorf("bystander session: %d", code)
	}
}

func TestNewIssueStoresReporterIDAndHidesIt(t *testing.T) {
	f := newErasureFixture(t)
	tok, err := f.s.sessions.CreateWithTenant(erasureTestVictim, erasureTestVictimTok, "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/media-issues", strings.NewReader(`{"title":"no audio","kind":"audio"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	f.s.handleMediaIssues(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), erasureTestVictim) {
		t.Errorf("response exposes the reporter id: %s", w.Body.String())
	}
	if got := f.s.issues.all[0]; got.ReporterID != erasureTestVictim || got.ReportedBy != erasureTestVictimTok {
		t.Errorf("stored issue = %+v", got)
	}
	if !strings.Contains(string(f.file("media-issues.json")), `"reporterId"`) {
		t.Error("reporter id was not persisted")
	}
	lw := httptest.NewRecorder()
	f.s.handleMediaIssues(lw, httptest.NewRequest(http.MethodGet, "/api/media-issues", nil))
	if strings.Contains(lw.Body.String(), erasureTestVictim) || strings.Contains(lw.Body.String(), "reporterId") {
		t.Errorf("list exposes the reporter id: %s", lw.Body.String())
	}
	// Anonymous reports keep no id.
	req = httptest.NewRequest(http.MethodPost, "/api/media-issues", strings.NewReader(`{"title":"anon"}`))
	w = httptest.NewRecorder()
	f.s.handleMediaIssues(w, req)
	if got := f.s.issues.all[0]; got.ReportedBy != "household" || got.ReporterID != "" {
		t.Errorf("anonymous issue = %+v", got)
	}
}

func TestPasswordResetRequestStoresResolvedUserID(t *testing.T) {
	post := func(f *erasureFixture, username string) passwordResetEntry {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"username": username})
		w := httptest.NewRecorder()
		f.s.handlePasswordReset(w, httptest.NewRequest(http.MethodPost, "/api/password-reset", bytes.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		reqs := f.s.passwordResets.load().Requests
		return reqs[len(reqs)-1]
	}
	f := newErasureFixture(t)
	f.s.resetUsers = fakeResolver{erasureTestVictimTok: erasureTestVictim}
	if e := post(f, erasureTestVictimTok); e.UserID != erasureTestVictim || e.Username != erasureTestVictimTok {
		t.Errorf("entry = %+v, want the resolved user id stored", e)
	}
	if e := post(f, "nobody"); e.UserID != "" {
		t.Errorf("unknown username stored id %q", e.UserID)
	}
	f.s.resetUsers = nil
	if e := post(f, erasureTestVictimTok); e.UserID != "" {
		t.Errorf("no provider connection stored id %q", e.UserID)
	}
	if !strings.Contains(string(f.file("password-resets.json")), `"user_id": "`+erasureTestVictim+`"`) {
		t.Errorf("file does not carry the id: %s", f.file("password-resets.json"))
	}
}

// TestPasswordResetRewritesPreserveUserID: this module's own rewrites of the
// shared file (dismiss, resolve) keep the id admin-ui erases by.
func TestPasswordResetRewritesPreserveUserID(t *testing.T) {
	f := newErasureFixture(t)
	f.writePasswordResets()
	if err := f.s.passwordResets.setStatus("r1", "dismissed"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.passwordResets.resolveUsername(erasureTestOtherName); err != nil {
		t.Fatal(err)
	}
	got := f.s.passwordResets.load().Requests
	if len(got) != 3 || got[0].UserID != erasureTestVictim || got[2].UserID != erasureTestBystander {
		t.Errorf("rows after this module's rewrites = %+v", got)
	}
}

func TestCachedUserIDsBoundsProviderCalls(t *testing.T) {
	users := &fakeUsers{users: []*authv1.UserInfo{
		{Id: "u1", Username: "alice"}, {Id: "u2", Username: "twin"}, {Id: "u3", Username: "twin"},
	}}
	now := time.Unix(1_700_000_000, 0)
	c := newCachedUserIDs(users)
	c.now = func() time.Time { return now }
	ctx := context.Background()
	if got := c.ResolveUserID(ctx, "alice"); got != "u1" {
		t.Errorf("alice = %q", got)
	}
	if got := c.ResolveUserID(ctx, "twin"); got != "" {
		t.Errorf("a name two accounts share must not resolve, got %q", got)
	}
	for i := 0; i < 50; i++ {
		c.ResolveUserID(ctx, "alice")
	}
	if users.callCount() != 1 {
		t.Errorf("provider calls = %d within the ttl, want 1", users.callCount())
	}
	now = now.Add(userIDLookupTTL + time.Second)
	c.ResolveUserID(ctx, "alice")
	if users.callCount() != 2 {
		t.Errorf("provider calls = %d after the ttl, want 2", users.callCount())
	}
	// A failing provider is also asked once per ttl and resolves nothing.
	users.mu.Lock()
	users.err = errors.New("down")
	users.mu.Unlock()
	now = now.Add(userIDLookupTTL + time.Second)
	for i := 0; i < 10; i++ {
		if got := c.ResolveUserID(ctx, "alice"); got != "" {
			t.Errorf("resolved %q from a failed lookup", got)
		}
	}
	if users.callCount() != 3 {
		t.Errorf("provider calls = %d while down, want 3", users.callCount())
	}
}

// TestNoSecondErasureTrustPath: the BFF's only erasure input is the SDK
// reconciler reading the identity provider's ledger. No source file here
// subscribes to the event bus or names a deletion event.
func TestNoSecondErasureTrustPath(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		for _, banned := range []string{"identity.user.deleted", "identity.erasure", "eventsv1", "EventService", ".Subscribe("} {
			if strings.Contains(src, banned) {
				t.Errorf("%s references %q: erasure must come only from the ledger reconciler", name, banned)
			}
		}
	}
}

// ---- reconciler wiring (SDK fake provider over real mTLS) ------------------------

const (
	erasureTestProviderID = "auth-local"
	erasureTestOwnerID    = "media-ui"
)

// authWithUsers is the SDK's fake ledger provider plus ListUsers.
type authWithUsers struct {
	*erasuretest.Provider
	users []*authv1.UserInfo
}

func (a *authWithUsers) ListUsers(context.Context, *authv1.ListUsersRequest) (*authv1.ListUsersResponse, error) {
	return &authv1.ListUsersResponse{Users: a.users}, nil
}

func clearErasureMeshEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MUXCORE_INSECURE_DISABLE_TLS", "MUXCORE_DEV_TLS_SKIP", "MUXCORE_GRPC_INSECURE",
		"MUXCORE_TLS_CERT", "MUXCORE_TLS_KEY", "MUXCORE_TLS_CA", "MUXCORE_TLS_SERVER_NAME",
		"MUXCORE_PROFILE", "MUXCORE_MESH_DIAL_LOCAL", "ERASURE_SWEEP_INTERVAL"} {
		t.Setenv(k, "")
	}
}

type reconcilerHarness struct {
	f        *erasureFixture
	pki      *erasuretest.PKI
	provider *erasuretest.Provider
	disc     *erasuretest.Discovery
	env      map[string]string
	rec      *erasure.Reconciler
}

// newReconcilerHarness serves a fake auth-local with serverCN as its
// certificate CN, registers it as the identity provider and builds the BFF's
// reconciler (through setupErasure, as main does) with media-ui's certificate.
func newReconcilerHarness(t *testing.T, serverCN string) *reconcilerHarness {
	t.Helper()
	clearErasureMeshEnv(t)
	f := newErasureFixture(t)
	h := &reconcilerHarness{f: f, pki: erasuretest.NewPKI(t), provider: erasuretest.NewProvider()}
	h.provider.Allowed = map[string]bool{erasureTestOwnerID: true}
	// The server certificate always carries the provider id as a SAN, so only
	// the CN check can tell an impostor apart.
	sc, sk := h.pki.Issue(t, serverCN, erasureTestProviderID, "localhost")
	srv := &authWithUsers{Provider: h.provider, users: f.users.users}
	addr := erasuretest.ServeTLS(t, srv, sc, sk, h.pki.CAFile)
	h.disc = erasuretest.NewDiscovery(erasuretest.Module(erasureTestProviderID, addr))
	cc, ck := h.pki.Issue(t, erasureTestOwnerID)
	h.env = map[string]string{
		"MUXCORE_GRPC_ADDR": "core:9090", "MUXCORE_PROFILE": "household",
		"MUXCORE_TLS_CERT": cc, "MUXCORE_TLS_KEY": ck, "MUXCORE_TLS_CA": h.pki.CAFile,
	}
	rec, err := f.s.setupErasure(erasureSetup{
		Getenv:      func(k string) string { return h.env[k] },
		ModuleID:    erasureTestOwnerID,
		UserdataDir: f.dir,
		Finder:      h.disc,
		Interval:    time.Minute,
		Tune: func(c *erasure.Config) {
			c.AckBackoff, c.RetryBackoff, c.CallTimeout = time.Millisecond, 10*time.Millisecond, 5*time.Second
		},
	})
	if err != nil || rec == nil {
		t.Fatalf("setupErasure = %v, %v", rec, err)
	}
	h.rec = rec
	return h
}

func (h *reconcilerHarness) sweep(t *testing.T) (erasure.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return h.rec.SweepOnce(ctx)
}

func TestReconcilerErasesFromLedgerAndAcknowledges(t *testing.T) {
	h := newReconcilerHarness(t, erasureTestProviderID)
	bystanderTok := h.f.seed()
	erasureID := h.provider.AddErasure(erasureTestVictim, "")

	res, err := h.sweep(t)
	if err != nil {
		t.Fatalf("sweep: %v (%+v)", err, res)
	}
	if res.Applied != 1 || res.Acked != 1 {
		t.Errorf("result = %+v", res)
	}
	ack, ok := h.provider.Latest(erasureID, erasureTestOwnerID)
	if !ok || ack.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		t.Fatalf("ack = %+v, %v; want OK from %s", ack, ok, erasureTestOwnerID)
	}
	if ack.Counts["sessions"] != 2 || ack.Counts["media_issues_anonymised"] != 3 {
		t.Errorf("ack counts = %v", ack.Counts)
	}
	h.f.requireVictimGoneBystanderIntact(bystanderTok)
	// The ledger now lists the id: nothing may be written on its behalf.
	if !h.rec.Erased(erasureTestVictim) {
		t.Error("reconciler does not report the listed id as erased")
	}
	if _, err := h.f.s.sessions.CreateWithTenant(erasureTestVictim, "x", ""); !errors.Is(err, errUserErased) {
		t.Errorf("session for a ledger id: %v", err)
	}
	// A second sweep is a no-op.
	res, err = h.sweep(t)
	if err != nil || res.Applied != 0 || res.Skipped != 1 {
		t.Errorf("second sweep = %+v, %v", res, err)
	}
}

func TestReconcilerIgnoresLedgerFromWrongCN(t *testing.T) {
	h := newReconcilerHarness(t, "intruder")
	h.f.seed()
	h.provider.AddErasure(erasureTestVictim, "")
	before := map[string][]byte{}
	for _, n := range []string{sessionFileName, "quickconnect.json", "media-issues.json", "watch-together.json", erasureTestVictim + ".json"} {
		before[n] = h.f.file(n)
	}

	res, err := h.sweep(t)
	if err == nil {
		t.Fatalf("sweep accepted a ledger from a certificate with the wrong CN (%+v)", res)
	}
	if res.Seen != 0 || res.Applied != 0 {
		t.Errorf("result = %+v, want nothing read or applied", res)
	}
	if list, ack := h.provider.Calls(); list != 0 || ack != 0 {
		t.Errorf("fake provider saw %d list / %d ack calls from a wrong-CN handshake", list, ack)
	}
	for n, b := range before {
		if !bytes.Equal(b, h.f.file(n)) {
			t.Errorf("%s changed", n)
		}
	}
	if h.rec.Erased(erasureTestVictim) {
		t.Error("an unverified ledger marked an id erased")
	}
}

func TestReconcilerProviderDownErasesNothing(t *testing.T) {
	h := newReconcilerHarness(t, erasureTestProviderID)
	h.f.seed()
	h.provider.AddErasure(erasureTestVictim, "")
	h.provider.SetListError(errors.New("db locked"))
	res, err := h.sweep(t)
	if err == nil {
		t.Fatalf("sweep succeeded with the ledger unreadable (%+v)", res)
	}
	if n := h.f.s.sessions.countUser(erasureTestVictim, ""); n == 0 {
		t.Error("sessions erased without a readable ledger")
	}
}

func TestReconcilerFailedApplyAcksFailedThenRetries(t *testing.T) {
	h := newReconcilerHarness(t, erasureTestProviderID)
	h.f.seed()
	erasureID := h.provider.AddErasure(erasureTestVictim, "")
	// Break media-issues.json's target: a directory cannot be renamed over.
	path := filepath.Join(h.f.dir, "media-issues.json")
	saved := h.f.file("media-issues.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sweep(t); err == nil {
		t.Fatal("sweep reported success with a store unwritable")
	}
	ack, ok := h.provider.Latest(erasureID, erasureTestOwnerID)
	if !ok || ack.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED || ack.Detail != erasureDetailIssues {
		t.Fatalf("ack = %+v, %v; want FAILED/%s", ack, ok, erasureDetailIssues)
	}
	if applied, _ := h.f.owner.Applied(context.Background(), erasureID); applied {
		t.Error("failed application is recorded")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := h.sweep(t); err != nil || res.Applied != 1 {
		t.Fatalf("retry sweep = %+v, %v", res, err)
	}
	if ack, _ := h.provider.Latest(erasureID, erasureTestOwnerID); ack.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		t.Errorf("ack after retry = %+v", ack)
	}
}

func TestSetupErasureHouseholdWithoutCoreAddressFailsStartup(t *testing.T) {
	for _, profile := range []string{"household", "staging", " Household "} {
		t.Run(profile, func(t *testing.T) {
			clearErasureMeshEnv(t)
			f := newErasureFixture(t)
			env := map[string]string{"MUXCORE_PROFILE": profile}
			rec, err := f.s.setupErasure(erasureSetup{Getenv: func(k string) string { return env[k] }, ModuleID: "media-ui", UserdataDir: f.dir})
			if err == nil || rec != nil {
				t.Fatalf("setupErasure = %v, %v; want a startup error", rec, err)
			}
			if !strings.Contains(err.Error(), "MUXCORE_GRPC_ADDR") {
				t.Errorf("error does not say how to fix it: %v", err)
			}
			// An address without a finder (no core connection) is the same.
			env["MUXCORE_GRPC_ADDR"] = "core:9090"
			if rec, err := f.s.setupErasure(erasureSetup{Getenv: func(k string) string { return env[k] }, ModuleID: "media-ui", UserdataDir: f.dir}); err == nil || rec != nil {
				t.Fatalf("address without a connection: %v, %v", rec, err)
			}
		})
	}
}

func TestSetupErasureDevWithoutCoreWarnsAndDisables(t *testing.T) {
	clearErasureMeshEnv(t)
	f := newErasureFixture(t)
	for _, env := range []map[string]string{{}, {"MUXCORE_PROFILE": "dev"}} {
		rec, err := f.s.setupErasure(erasureSetup{Getenv: func(k string) string { return env[k] }, ModuleID: "media-ui", UserdataDir: f.dir})
		if err != nil || rec != nil {
			t.Errorf("env %v: setupErasure = %v, %v; want disabled without error", env, rec, err)
		}
	}
	if f.s.userErased(erasureTestVictim) {
		t.Error("a disabled reconciler reports ids as erased")
	}
}

func TestErasureRefusesPlaintextMeshInHouseholdAndStaging(t *testing.T) {
	env := func(profile string) func(string) string {
		return func(k string) string {
			if k == "MUXCORE_PROFILE" {
				return profile
			}
			return ""
		}
	}
	for _, profile := range []string{"household", "staging", "HOUSEHOLD"} {
		if err := erasureTransportCheck(env(profile), true); err == nil {
			t.Errorf("profile %q: plaintext mesh accepted", profile)
		}
		if err := erasureTransportCheck(env(profile), false); err != nil {
			t.Errorf("profile %q: mTLS refused: %v", profile, err)
		}
	}
	// Explicit insecure dev stays possible; nothing else enables it.
	for _, profile := range []string{"dev", ""} {
		if err := erasureTransportCheck(env(profile), true); err != nil {
			t.Errorf("profile %q: explicit insecure dev refused: %v", profile, err)
		}
	}
}

func TestMeshModuleIDIsTheOwnerID(t *testing.T) {
	// The acknowledgement is attributed to the certificate CN, which meshid
	// enrolls as meshModuleID; the owner must use the same value.
	if got := meshModuleID(func(string) string { return "" }); got != "media-ui" {
		t.Errorf("default mesh id = %q, want media-ui", got)
	}
	if got := meshModuleID(func(k string) string {
		if k == "MUXCORE_MODULE_ID" {
			return "custom-ui"
		}
		return ""
	}); got != "custom-ui" {
		t.Errorf("override mesh id = %q", got)
	}
}
