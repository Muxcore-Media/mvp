package main

import (
	"bytes"
	"context"
	"encoding/json"
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
// /api/userdata route with the session's bearer and X-MuxCore-User-Id, and
// fall back to the local file store when the provider call fails. The local
// store is an ordinary availability fallback for blobs only — it is never
// consulted for parental policy, and a fallback is never reported as provider
// success (degraded transitions are logged). Set USERDATA_PREFER_MESH=0 to
// use local files only. After a successful PUT, optionally notifies the
// Jellyfin bridge so companion UI progress can push into Jellyfin UserData
// (JELLYFIN_USERDATA_PUSH_URL).
type serverUserdata struct {
	store    *store.Store
	provider userdataProviderClient // nil = local files only
	pushURL  string                 // jellyfin bridge /userdata/from-muxcore
	// degraded records that the last provider call failed, so the fallback is
	// logged once per outage rather than per request.
	degraded atomic.Bool
}

func newServerUserdata(dir string, provider userdataProviderClient) *serverUserdata {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "muxcore-media-userdata")
	}
	st, err := store.New(dir)
	if err != nil {
		// Fall back to empty store in temp — Init always succeeds for fixtures.
		st, _ = store.New(filepath.Join(os.TempDir(), "muxcore-media-userdata-fallback"))
	}
	if os.Getenv("USERDATA_PREFER_MESH") == "0" {
		provider = nil
	}
	push := strings.TrimRight(strings.TrimSpace(os.Getenv("JELLYFIN_USERDATA_PUSH_URL")), "/")
	return &serverUserdata{
		store:    st,
		provider: provider,
		pushURL:  push,
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

// load returns the scope's blob. authToken is the signed-in user's auth-local
// token, forwarded as a bearer to userdata-local (ADR-0019).
// Without a bearer (dev sessions without auth, Quick Connect) the provider
// would refuse the request, so only the local store is used.
func (u *serverUserdata) load(ctx context.Context, scope store.Scope, authToken string) store.Blob {
	if u.useProvider(authToken) {
		blob, err := u.providerGet(ctx, scope, authToken)
		u.noteProvider("GET", err)
		if err == nil {
			return blob
		}
	}
	return u.store.Get(scope)
}

func (u *serverUserdata) save(ctx context.Context, scope store.Scope, incoming store.Blob, authToken string) (store.Blob, error) {
	var (
		merged store.Blob
		err    error
	)
	if u.useProvider(authToken) {
		blob, putErr := u.providerPut(ctx, scope, incoming, authToken)
		u.noteProvider("PUT", putErr)
		if putErr == nil {
			// Mirror into local cache for offline/fixture paths.
			_, _ = u.store.Put(scope, blob)
			merged = blob
		} else {
			merged, err = u.store.Put(scope, incoming)
		}
	} else {
		merged, err = u.store.Put(scope, incoming)
	}
	if err != nil {
		return store.Blob{}, err
	}
	u.notifyJellyfinPush(scope, merged, authToken)
	return merged, nil
}

func (u *serverUserdata) useProvider(authToken string) bool {
	return u.provider != nil && strings.TrimSpace(authToken) != ""
}

// noteProvider logs provider/local-fallback transitions. A failed provider call
// is never counted as success: the caller serves the local store instead.
func (u *serverUserdata) noteProvider(op string, err error) {
	if err != nil {
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
func (u *serverUserdata) providerGet(ctx context.Context, scope store.Scope, authToken string) (store.Blob, error) {
	h := http.Header{}
	h.Set("Accept", "application/json")
	setUserdataAuth(h, scope, authToken)
	resp, err := u.provider.Do(ctx, httpclient.GetUserdata, h, nil)
	if err != nil {
		return store.Blob{}, err
	}
	return decodeProviderBlob(resp)
}

// providerPut merges the blob through PUT /api/userdata and returns the
// provider's merged document.
func (u *serverUserdata) providerPut(ctx context.Context, scope store.Scope, incoming store.Blob, authToken string) (store.Blob, error) {
	body, err := json.Marshal(incoming)
	if err != nil {
		return store.Blob{}, err
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	setUserdataAuth(h, scope, authToken)
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
	scope := s.userdata.scopeFromRequest(r, s.sessions, s.sessionHasPrivilegedRole(r))
	writeUserdataJSON(w, scope, s.userdata.load(r.Context(), scope, s.sessionAuthToken(r)))
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
	scope := s.userdata.scopeFromRequest(r, s.sessions, s.sessionHasPrivilegedRole(r))
	merged, err := s.userdata.save(r.Context(), scope, blob, s.sessionAuthToken(r))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "userdata.save_failed")
		return
	}
	writeUserdataJSON(w, scope, merged)
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
