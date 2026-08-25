package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/userdata-local/store"
)

// serverUserdata is the BFF durable store for Jellyfin-style userdata
// (progress/favorites/prefs/queue). Scoped per session user (+ tenant when
// TENANT_MODE=1).
//
// Mesh preference: when USERDATA_LOCAL_URL is set, GET/PUT prefer the
// userdata-local module and fall back to the local file store on mesh errors.
// Set USERDATA_PREFER_MESH=0 to force local files only (ignore URL).
// After a successful PUT, optionally notifies the Jellyfin bridge so companion
// UI progress can push into Jellyfin UserData (JELLYFIN_USERDATA_PUSH_URL).
type serverUserdata struct {
	store    *store.Store
	proxyURL string // empty = local files only
	pushURL  string // jellyfin bridge /userdata/from-muxcore
}

func newServerUserdata(dir string) *serverUserdata {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "muxcore-media-userdata")
	}
	st, err := store.New(dir)
	if err != nil {
		// Fall back to empty store in temp — Init always succeeds for fixtures.
		st, _ = store.New(filepath.Join(os.TempDir(), "muxcore-media-userdata-fallback"))
	}
	preferMesh := os.Getenv("USERDATA_PREFER_MESH") != "0"
	proxy := ""
	if preferMesh {
		proxy = strings.TrimRight(strings.TrimSpace(os.Getenv("USERDATA_LOCAL_URL")), "/")
	}
	push := strings.TrimRight(strings.TrimSpace(os.Getenv("JELLYFIN_USERDATA_PUSH_URL")), "/")
	return &serverUserdata{
		store:    st,
		proxyURL: proxy,
		pushURL:  push,
	}
}

func (u *serverUserdata) scopeFromRequest(r *http.Request, sessions *sessionStore) store.Scope {
	userID := "anonymous"
	sessionTenant := ""
	if c, err := r.Cookie("session"); err == nil && sessions != nil {
		if uid, _, tid, ok := sessions.LookupTenant(c.Value); ok && uid != "" {
			userID = uid
			sessionTenant = tid
		}
	}
	if q := strings.TrimSpace(r.URL.Query().Get("user_id")); q != "" {
		userID = q
	}
	tenantID := ""
	if os.Getenv("TENANT_MODE") == "1" {
		tenantID = strings.TrimSpace(sessionTenant)
		if tenantID == "" {
			tenantID = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
		}
		if tenantID == "" {
			tenantID = strings.TrimSpace(r.URL.Query().Get("tenant_id"))
		}
		if tenantID == "" {
			tenantID = "default"
		}
	}
	return store.Scope{TenantID: tenantID, UserID: userID}
}

func (u *serverUserdata) load(scope store.Scope) store.Blob {
	if u.proxyURL != "" {
		if blob, ok := u.proxyGet(scope); ok {
			return blob
		}
	}
	return u.store.Get(scope)
}

func (u *serverUserdata) save(scope store.Scope, incoming store.Blob) (store.Blob, error) {
	var (
		merged store.Blob
		err    error
	)
	if u.proxyURL != "" {
		if blob, putErr := u.proxyPut(scope, incoming); putErr == nil {
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
	u.notifyJellyfinPush(scope, merged)
	return merged, nil
}

func (u *serverUserdata) proxyGet(scope store.Scope) (store.Blob, bool) {
	req, err := http.NewRequest(http.MethodGet, u.proxyURL+"/userdata", nil)
	if err != nil {
		return store.Blob{}, false
	}
	q := req.URL.Query()
	q.Set("user_id", scope.UserID)
	if scope.TenantID != "" {
		q.Set("tenant_id", scope.TenantID)
	}
	req.URL.RawQuery = q.Encode()
	if scope.TenantID != "" {
		req.Header.Set("X-Tenant-ID", scope.TenantID)
	}
	req.Header.Set("X-User-ID", scope.UserID)
	resp, err := upstreamClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return store.Blob{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	var blob store.Blob
	if json.NewDecoder(resp.Body).Decode(&blob) != nil {
		return store.Blob{}, false
	}
	return blob, true
}

func (u *serverUserdata) proxyPut(scope store.Scope, incoming store.Blob) (store.Blob, error) {
	body, err := json.Marshal(incoming)
	if err != nil {
		return store.Blob{}, err
	}
	req, err := http.NewRequest(http.MethodPut, u.proxyURL+"/userdata", strings.NewReader(string(body)))
	if err != nil {
		return store.Blob{}, err
	}
	q := req.URL.Query()
	q.Set("user_id", scope.UserID)
	if scope.TenantID != "" {
		q.Set("tenant_id", scope.TenantID)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", scope.UserID)
	if scope.TenantID != "" {
		req.Header.Set("X-Tenant-ID", scope.TenantID)
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return store.Blob{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return store.Blob{}, os.ErrInvalid
	}
	var blob store.Blob
	if err := json.NewDecoder(resp.Body).Decode(&blob); err != nil {
		return store.Blob{}, err
	}
	return blob, nil
}

// notifyJellyfinPush best-effort posts merged userdata to the jellyfin bridge
// so companion UI updates can land in Jellyfin UserData (requires
// USERDATA_PUSH_TO_JELLYFIN=1 on the bridge).
func (u *serverUserdata) notifyJellyfinPush(scope store.Scope, blob store.Blob) {
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
		req.Header.Set("X-User-ID", scope.UserID)
		resp, err := upstreamClient.Do(req)
		if err != nil {
			log.Printf("jellyfin userdata push notify: %v", err)
			return
		}
		_ = resp.Body.Close()
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
	scope := s.userdata.scopeFromRequest(r, s.sessions)
	writeJSON(w, s.userdata.load(scope))
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
	scope := s.userdata.scopeFromRequest(r, s.sessions)
	merged, err := s.userdata.save(scope, blob)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "userdata.save_failed")
		return
	}
	writeJSON(w, merged)
}
