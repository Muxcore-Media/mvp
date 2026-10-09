package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/store"
)

// muxcoreUserIDHeader is the household user id used as the parental PIN salt
// (admin-ui sync + userdata-local auth). Same value as JSON user_id.
const muxcoreUserIDHeader = "X-MuxCore-User-Id"

// serverUserdata is the BFF durable store for Jellyfin-style userdata
// (progress/favorites/prefs/queue). Scoped per session user (+ tenant when
// TENANT_MODE=1).
//
// Provider preference: with a checked userdata-local client
// (userdata_provider.go, ADR-0033) GET/PUT go to the provider's canonical
// /api/userdata route with the session's bearer and X-MuxCore-User-Id
// (providerAllowed decides per request; never in TENANT_MODE, see there).
// The first provider operation per user migrates the pre-upgrade local blob
// once (userdata_migrate.go). A provider outage (transport, module admission,
// redirect, unreadable response, 5xx/408/429) falls back to the local file
// store; a provider 4xx is returned to the client and nothing is written
// locally. The local store is an ordinary availability fallback for blobs only
// — it is never consulted for parental policy, and a fallback is never
// reported as provider success (degraded transitions are logged). Set
// USERDATA_PREFER_MESH=0 to use local files only. After a successful PUT,
// optionally notifies the Jellyfin bridge so companion UI progress can push
// into Jellyfin UserData (JELLYFIN_USERDATA_PUSH_URL).
type serverUserdata struct {
	store     *store.Store
	provider  userdataProviderClient // nil = local files only
	pushURL   string                 // jellyfin bridge /userdata/from-muxcore
	migration *userdataMigration
	// degraded records that the last provider call failed, so the fallback is
	// logged once per outage rather than per request.
	degraded atomic.Bool
}

// userdataRequest is one blob operation: the scope, the session's auth-local
// bearer and whether the provider may be used for it (providerAllowed).
type userdataRequest struct {
	scope    store.Scope
	bearer   string
	provider bool
}

func newServerUserdata(dir string, provider userdataProviderClient) *serverUserdata {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "muxcore-media-userdata")
	}
	st, err := store.New(dir)
	if err != nil {
		// Fall back to empty store in temp — Init always succeeds for fixtures.
		dir = filepath.Join(os.TempDir(), "muxcore-media-userdata-fallback")
		st, _ = store.New(dir)
	}
	if os.Getenv("USERDATA_PREFER_MESH") == "0" {
		provider = nil
	}
	push := strings.TrimRight(strings.TrimSpace(os.Getenv("JELLYFIN_USERDATA_PUSH_URL")), "/")
	return &serverUserdata{
		store:     st,
		provider:  provider,
		pushURL:   push,
		migration: &userdataMigration{dir: filepath.Join(dir, migrationDirName)},
	}
}

// scopeFromRequest derives userdata scope from the authenticated session.
// Client user_id / X-MuxCore-User-Id / tenant_id / X-Tenant-ID are ignored
// unless allowClientOverride is true (admin/manager principals). Header
// X-MuxCore-User-Id matches admin-ui → userdata-local sync.
func (u *serverUserdata) scopeFromRequest(r *http.Request, sessions *sessionStore, allowClientOverride bool) store.Scope {
	userID := "anonymous"
	sessionTenant := ""
	if e, ok := requestSession(r, sessions); ok && e.userID != "" {
		userID = e.userID
		sessionTenant = e.tenantID
	}
	if allowClientOverride {
		if h := strings.TrimSpace(r.Header.Get(muxcoreUserIDHeader)); h != "" {
			userID = h
		} else if q := strings.TrimSpace(r.URL.Query().Get("user_id")); q != "" {
			userID = q
		}
	}
	tenantID := ""
	if os.Getenv("TENANT_MODE") == "1" {
		tenantID = strings.TrimSpace(sessionTenant)
		if allowClientOverride {
			if tenantID == "" {
				tenantID = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
			}
			if tenantID == "" {
				tenantID = strings.TrimSpace(r.URL.Query().Get("tenant_id"))
			}
		}
		if tenantID == "" {
			tenantID = "default"
		}
	}
	return store.Scope{TenantID: tenantID, UserID: userID}
}

// providerAllowed decides whether a blob operation may use the provider.
// Never without a bearer (the provider would refuse it; Quick Connect and
// auth-less dev keep the local store). Never in TENANT_MODE: userdata-local
// v0.1.6 keys blobs by user ID only and its admin check ignores tenants, so a
// tenant-A admin could read or write a tenant-B user's blob through it; the
// tenant-scoped local store stays authoritative until the provider is
// tenant-aware. The target is the verified session user unless the session is
// privileged (admin/manager override in scopeFromRequest).
func (u *serverUserdata) providerAllowed(scope store.Scope, bearer, sessionUser string, privileged bool) bool {
	if u.provider == nil || strings.TrimSpace(bearer) == "" {
		return false
	}
	if os.Getenv("TENANT_MODE") == "1" {
		return false
	}
	return scope.UserID == sessionUser || privileged
}

// load returns the scope's blob: the provider's (after the one-time migration)
// or, on a provider outage or when the provider is not allowed, the local
// store's. A provider 4xx is returned as a *userdataError.
func (u *serverUserdata) load(ctx context.Context, req userdataRequest) (store.Blob, error) {
	if !req.provider {
		return u.store.Get(req.scope), nil
	}
	if !u.migration.decided(req.scope) {
		defer u.migration.lock(req.scope)()
	}
	blob, err := u.providerGet(ctx, req)
	u.noteProvider("GET", err)
	if err != nil {
		if ae := providerApplicationError(err); ae != nil {
			return store.Blob{}, ae
		}
		return u.store.Get(req.scope), nil
	}
	if u.migration.decided(req.scope) {
		return blob, nil
	}
	blob, _ = u.migrate(ctx, req, blob)
	return blob, nil
}

func (u *serverUserdata) save(ctx context.Context, req userdataRequest, incoming store.Blob) (store.Blob, error) {
	merged, err := u.saveBlob(ctx, req, incoming)
	if err != nil {
		return store.Blob{}, err
	}
	u.notifyJellyfinPush(req.scope, merged, req.bearer)
	return merged, nil
}

func (u *serverUserdata) saveBlob(ctx context.Context, req userdataRequest, incoming store.Blob) (store.Blob, error) {
	if !req.provider {
		return u.store.Put(req.scope, incoming)
	}
	if !u.migration.decided(req.scope) {
		// Migrate before the first write, or the write would make the provider
		// non-empty and strand the pre-upgrade local copy.
		defer u.migration.lock(req.scope)()
		if !u.migration.decided(req.scope) {
			current, err := u.providerGet(ctx, req)
			u.noteProvider("GET", err)
			if err != nil {
				if ae := providerApplicationError(err); ae != nil {
					return store.Blob{}, ae
				}
				return u.store.Put(req.scope, incoming)
			}
			if _, pending := u.migrate(ctx, req, current); pending {
				// The local copy stays authoritative until it is migrated.
				return u.store.Put(req.scope, incoming)
			}
		}
	}
	blob, err := u.providerPut(ctx, req, incoming)
	u.noteProvider("PUT", err)
	if err != nil {
		if ae := providerApplicationError(err); ae != nil {
			return store.Blob{}, ae
		}
		return u.store.Put(req.scope, incoming)
	}
	// Mirror into local cache for offline/fixture paths.
	_, _ = u.store.Put(req.scope, blob)
	return blob, nil
}

// noteProvider logs provider/local-fallback transitions. A failed provider call
// is never counted as success: the caller serves the local store instead.
func (u *serverUserdata) noteProvider(op string, err error) {
	if err != nil && providerApplicationError(err) == nil {
		if !u.degraded.Swap(true) {
			log.Printf("warn: userdata provider %s failed: %s; serving the local fallback store until it recovers", op, describeProviderError(err))
		}
		return
	}
	if u.degraded.Swap(false) {
		log.Printf("userdata provider recovered (%s)", op)
	}
}

// setUserdataAuth sets the end-user bearer and the target user header that
// userdata-local / the jellyfin bridge check against the token's principal.
// Client identity headers are never forwarded (requests are built fresh).
func setUserdataAuth(h http.Header, scope store.Scope, authToken string) {
	if tok := strings.TrimSpace(authToken); tok != "" {
		h.Set("Authorization", "Bearer "+tok)
	}
	h.Set(muxcoreUserIDHeader, scope.UserID)
}

// errUserdataProviderStatus is a non-200 provider response (the body is not
// logged: it can echo user data).
type errUserdataProviderStatus int

func (e errUserdataProviderStatus) Error() string {
	return fmt.Sprintf("provider returned HTTP %d", int(e))
}

// providerGet reads the blob from GET /api/userdata. The provider keys blobs by
// the authenticated target user only; the request carries no query string.
func (u *serverUserdata) providerGet(ctx context.Context, req userdataRequest) (store.Blob, error) {
	h := http.Header{}
	h.Set("Accept", "application/json")
	setUserdataAuth(h, req.scope, req.bearer)
	resp, err := u.provider.Do(ctx, httpclient.GetUserdata, h, nil)
	if err != nil {
		return store.Blob{}, err
	}
	return decodeProviderBlob(resp)
}

// providerPut merges the blob through PUT /api/userdata and returns the
// provider's merged document.
func (u *serverUserdata) providerPut(ctx context.Context, req userdataRequest, incoming store.Blob) (store.Blob, error) {
	incoming.UserID, incoming.TenantID = "", ""
	body, err := json.Marshal(incoming)
	if err != nil {
		return store.Blob{}, err
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	setUserdataAuth(h, req.scope, req.bearer)
	resp, err := u.provider.Do(ctx, httpclient.PutUserdata, h, bytes.NewReader(body))
	if err != nil {
		return store.Blob{}, err
	}
	return decodeProviderBlob(resp)
}

func decodeProviderBlob(resp *http.Response) (store.Blob, error) {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return store.Blob{}, errUserdataProviderStatus(resp.StatusCode)
	}
	var blob store.Blob
	if err := json.NewDecoder(resp.Body).Decode(&blob); err != nil {
		return store.Blob{}, fmt.Errorf("decode provider blob: %w", err)
	}
	return blob, nil
}

// notifyJellyfinPush best-effort posts merged userdata to the jellyfin bridge
// so companion UI updates can land in Jellyfin UserData (requires
// USERDATA_PUSH_TO_JELLYFIN=1 on the bridge). The bridge (v0.3.2+) requires
// the end user's bearer; authToken is captured here because the post runs in
// a goroutine after the request has returned.
func (u *serverUserdata) notifyJellyfinPush(scope store.Scope, blob store.Blob, authToken string) {
	if u.pushURL == "" {
		return
	}
	body, err := json.Marshal(blob)
	if err != nil {
		return
	}
	go func() {
		req, err := http.NewRequest(http.MethodPost, u.pushURL, bytes.NewReader(body))
		if err != nil {
			return
		}
		q := req.URL.Query()
		q.Set("user_id", scope.UserID)
		req.URL.RawQuery = q.Encode()
		req.Header.Set("Content-Type", "application/json")
		setUserdataAuth(req.Header, scope, authToken)
		resp, err := upstreamClient.Do(req)
		if err != nil {
			log.Printf("jellyfin userdata push notify: %v", err)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("jellyfin userdata push notify: HTTP %d", resp.StatusCode)
		}
	}()
}

func (s *server) handleUserdataGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.userdata == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "userdata disabled", "userdata.disabled")
		return
	}
	req := s.userdataRequest(r)
	blob, err := s.userdata.load(r.Context(), req)
	if err != nil {
		writeUserdataError(w, err)
		return
	}
	writeUserdataJSON(w, req.scope, blob)
}

func (s *server) handleUserdataPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.userdata == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "userdata disabled", "userdata.disabled")
		return
	}
	var blob store.Blob
	if err := json.NewDecoder(r.Body).Decode(&blob); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "userdata.invalid_json")
		return
	}
	req := s.userdataRequest(r)
	merged, err := s.userdata.save(r.Context(), req, blob)
	if err != nil {
		writeUserdataError(w, err)
		return
	}
	writeUserdataJSON(w, req.scope, merged)
}

// userdataRequest derives the scope, bearer and provider admission for r from
// the verified session (client identity headers count only for privileged
// sessions, via scopeFromRequest).
func (s *server) userdataRequest(r *http.Request) userdataRequest {
	privileged := s.sessionHasPrivilegedRole(r)
	scope := s.userdata.scopeFromRequest(r, s.sessions, privileged)
	e, _ := requestSession(r, s.sessions)
	return userdataRequest{scope: scope, bearer: e.authToken,
		provider: s.userdata.providerAllowed(scope, e.authToken, e.userID, privileged)}
}

// writeUserdataError writes a provider application answer (no provider body)
// or a local store failure.
func writeUserdataError(w http.ResponseWriter, err error) {
	var ue *userdataError
	if errors.As(err, &ue) {
		writeAPIError(w, ue.status, ue.message, ue.code)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, err.Error(), "userdata.save_failed")
}

// writeUserdataJSON returns the blob plus the scoped household user_id so
// media-ui hashPin can salt SHA-256(userID+":"+pin) without a separate lookup.
func writeUserdataJSON(w http.ResponseWriter, scope store.Scope, blob store.Blob) {
	raw, err := json.Marshal(blob)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "userdata.encode_failed")
		return
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "userdata.encode_failed")
		return
	}
	if out == nil {
		out = map[string]any{}
	}
	if scope.UserID != "" {
		out["user_id"] = scope.UserID
		w.Header().Set(muxcoreUserIDHeader, scope.UserID)
	}
	if scope.TenantID != "" {
		out["tenant_id"] = scope.TenantID
	}
	writeJSON(w, out)
}

// sessionAuthToken returns the signed-in user's auth-local token ("" without a
// session or when the session was created without one).
func (s *server) sessionAuthToken(r *http.Request) string {
	e, _ := requestSession(r, s.sessions)
	return e.authToken
}
