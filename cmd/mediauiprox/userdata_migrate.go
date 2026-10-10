package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/store"
)

// One-time local → provider userdata migration (ADR-0033 S9b rollout).
//
// Before S9b the BFF's blob proxy called /userdata, a route userdata-local never
// served, so every user's progress/favorites/prefs/playlists/queue lives only
// in the BFF's local store (MEDIA_UI_USERDATA_DIR). Now that the provider is
// authoritative, the first provider operation for a user (GET or PUT) moves that
// local blob to the provider once:
//
//   - The provider's collections (progress, favorites, playlists, queue) are
//     empty: PUT the local blob with the user's bearer through the checked
//     client. Local prefs go along only when the provider's prefs are absent or
//     still userdata-local's defaults (an unset provider blob carries default
//     prefs; admin-ui PIN writes customize them); otherwise the provider's own
//     prefs are re-sent, because a v0.1.6 PUT without prefs resets them to the
//     defaults (ParseBlob fills absent prefs, MergeBlob replaces). Provider-held
//     data is never overwritten. A marker is recorded only after a confirmed 200.
//   - The provider already holds collections: keep the provider copy, never
//     touch the local file, log once (no blob content) and record that decision.
//   - The local store holds nothing: record that there is nothing to migrate.
//   - The migration PUT fails: no migrated marker; the local copy is served for
//     that request (and is the write target for that user) and the next request
//     retries. An "attempted" intent record is written before the PUT, so a
//     retry after an uncertain outcome (applied, response lost) re-sends the
//     local blob instead of mistaking the provider for independently populated.
//
// Per-scope locks serialize concurrent first operations, so exactly one request
// migrates; the provider's merge (progress by updatedAt, keyed favorites) keeps
// an uncertain-outcome retry idempotent. This path is userdata only: it is never
// consulted for parental policy.

// providerDefaultPrefsJSON is userdata-local v0.1.6 models.DefaultPreferences as
// the provider encodes it for a user without a stored blob.
// TestRealUserdataProviderMigration checks it against the running provider.
const providerDefaultPrefsJSON = `{"display":{"theme":"dark","libraryPageSize":48,"showWatchedIndicators":true},` +
	`"home":{"showContinueWatching":true,"showFavorites":true,"showRecentRequests":true,"showNextUp":true},` +
	`"playback":{"autoplayNext":false,"rememberPosition":true,"skipIntroSec":0},` +
	`"subtitles":{"enabled":true,"language":"eng","textSize":"md"},` +
	`"controls":{"enableKeyboardShortcuts":true}}`

const (
	migrationAttempted    = "attempted" // intent record, not a decision
	migrationMigrated     = "migrated"
	migrationNothing      = "nothing_to_migrate"
	migrationProviderKept = "provider_kept"
	migrationDirName      = ".provider-migration"
	migrationLockStripes  = 64
)

// userdataMigration records per-scope migration decisions next to the local
// store and serializes first provider operations per scope.
type userdataMigration struct {
	dir   string // <local store>/.provider-migration
	locks [migrationLockStripes]sync.Mutex
}

func migrationScopeKey(scope store.Scope) string {
	return scope.TenantID + "\x00" + scope.UserID
}

func (m *userdataMigration) lock(scope store.Scope) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(migrationScopeKey(scope)))
	mu := &m.locks[h.Sum32()%migrationLockStripes]
	mu.Lock()
	return mu.Unlock
}

func (m *userdataMigration) markerPath(scope store.Scope) string {
	sum := sha256.Sum256([]byte(migrationScopeKey(scope)))
	return filepath.Join(m.dir, hex.EncodeToString(sum[:])+".json")
}

// state returns the recorded state for scope ("" when none or unreadable).
func (m *userdataMigration) state(scope store.Scope) string {
	raw, err := os.ReadFile(m.markerPath(scope))
	if err != nil {
		return ""
	}
	var rec struct {
		State string `json:"state"`
	}
	if json.Unmarshal(raw, &rec) != nil {
		return ""
	}
	return rec.State
}

// decided reports whether a final migration decision is recorded for scope.
func (m *userdataMigration) decided(scope store.Scope) bool {
	st := m.state(scope)
	return st != "" && st != migrationAttempted
}

// mark records the decision atomically (0600 temp file, fsync, rename).
func (m *userdataMigration) mark(scope store.Scope, state string) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"user_id": scope.UserID, "tenant_id": scope.TenantID, "state": state,
		"at": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.dir, ".marker-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(body); err != nil {
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
	return os.Rename(name, m.markerPath(scope))
}

// rawJSONEmpty treats an absent value, null, {} and [] as empty.
func rawJSONEmpty(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "{}", "[]":
		return true
	}
	return false
}

func blobHasCollections(b store.Blob) bool {
	return len(b.Progress) > 0 || len(b.Favorites) > 0 || !rawJSONEmpty(b.Playlists) || !rawJSONEmpty(b.Queue)
}

// prefsCustomized reports provider prefs other than userdata-local's defaults.
// Undecodable prefs count as customized (never overwritten).
func prefsCustomized(raw json.RawMessage) bool {
	if rawJSONEmpty(raw) {
		return false
	}
	var got, def any
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(providerDefaultPrefsJSON), &def) != nil {
		return true
	}
	return !reflect.DeepEqual(got, def)
}

// migrate decides and, when the provider holds no collections, moves the local
// blob for req.scope. Call it with the scope lock held and the provider blob
// read in the same critical section. pending reports a failed migration PUT:
// the local copy stays authoritative for this request and the next one retries.
func (u *serverUserdata) migrate(ctx context.Context, req userdataRequest, provider store.Blob) (blob store.Blob, pending bool) {
	scope := req.scope
	local := u.store.Get(scope)
	payload := store.Blob{Progress: local.Progress, Favorites: local.Favorites, Playlists: local.Playlists, Queue: local.Queue}
	if !prefsCustomized(provider.Prefs) && !rawJSONEmpty(local.Prefs) {
		payload.Prefs = local.Prefs
	}
	migratesPrefs := payload.Prefs != nil
	retry := u.migration.state(scope) == migrationAttempted
	switch {
	case !blobHasCollections(local) && rawJSONEmpty(local.Prefs):
		u.recordMigration(scope, migrationNothing)
		return provider, false
	case (blobHasCollections(provider) && !retry) || (!blobHasCollections(payload) && !migratesPrefs):
		log.Printf("userdata: provider already holds userdata for user %q; the pre-upgrade local copy was not migrated and is kept unchanged", scope.UserID)
		u.recordMigration(scope, migrationProviderKept)
		return provider, false
	}
	if !migratesPrefs {
		payload.Prefs = provider.Prefs // keep the provider's prefs (see above)
	}
	if !retry {
		u.recordMigration(scope, migrationAttempted)
	}
	merged, err := u.providerPut(ctx, req, payload)
	u.noteProvider("PUT", err)
	if err != nil {
		log.Printf("warn: userdata: migrating the local copy for user %q to the provider failed: %s; serving the local copy, retrying on the next request",
			scope.UserID, describeProviderError(err))
		return local, true
	}
	u.recordMigration(scope, migrationMigrated)
	log.Printf("userdata: migrated the pre-upgrade local copy for user %q to the provider", scope.UserID)
	return merged, false
}

// recordMigration writes the marker. A failed write only means the decision is
// taken again next time, which cannot lose data: after a migration the
// provider holds the collections, so the provider copy is kept.
func (u *serverUserdata) recordMigration(scope store.Scope, state string) {
	u.localMu.Lock()
	defer u.localMu.Unlock()
	if u.erased != nil && scope.UserID != "" && u.erased(scope.UserID) {
		return // never recreate a marker for an erased user (ADR-0035 §3)
	}
	if err := u.migration.mark(scope, state); err != nil {
		log.Printf("warn: userdata: recording the migration decision for user %q: %v", scope.UserID, err)
	}
}

// userdataError is a provider application answer returned to the client
// without the provider's body.
type userdataError struct {
	status  int
	code    string
	message string
}

func (e *userdataError) Error() string { return e.code + ": " + e.message }

// providerApplicationError maps a provider 4xx (other than the transient 408
// and 429) to the client answer; nil means the error is an outage (transport,
// module admission, redirect, unreadable response, 5xx/408/429), for which the
// ordinary blob fallback to the local store applies.
func providerApplicationError(err error) *userdataError {
	if errors.Is(err, httpclient.ErrUnavailable) {
		return nil
	}
	var st errUserdataProviderStatus
	if !errors.As(err, &st) {
		return nil
	}
	switch s := int(st); {
	case s < 400 || s >= 500 || s == http.StatusRequestTimeout || s == http.StatusTooManyRequests:
		return nil
	case s == http.StatusUnauthorized:
		return &userdataError{http.StatusUnauthorized, "userdata.session_invalid", "session is no longer valid"}
	case s == http.StatusForbidden:
		return &userdataError{http.StatusForbidden, "userdata.forbidden", "userdata access denied"}
	case s == http.StatusRequestEntityTooLarge:
		return &userdataError{http.StatusRequestEntityTooLarge, "userdata.too_large", "userdata too large"}
	case s == http.StatusBadRequest:
		return &userdataError{http.StatusBadRequest, "userdata.invalid", "invalid userdata"}
	default:
		return &userdataError{s, "userdata.provider_rejected", fmt.Sprintf("userdata request rejected (HTTP %d)", s)}
	}
}
