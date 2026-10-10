package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	"google.golang.org/grpc"
)

// ADR-0035 personal-data owner for the media-ui BFF (roadmap T-M4-07, slice E8).
//
// The BFF keeps these stores that carry a user id (ADR-0035 §3, "BFF"):
//
//   - sessions.json         every session of the user is deleted, including
//     bearer-less Quick Connect sessions;
//   - quickconnect.json     the codes the user approved are deleted;
//   - <dir>/<id>.json and tenants/<t>/<id>.json   the local fallback userdata
//     blobs are deleted, with the user's .provider-migration markers;
//   - watch-together.json   rooms hosted by the user are deleted;
//   - media-issues.json     the reporter of the user's issues is anonymised
//     ("deleted-user"); the issue rows stay.
//
// password-resets.json is NOT touched here: admin-ui is its single eraser
// (ADR-0035 §3). The BFF only records the resolved user id on new requests
// (password_reset.go) so admin-ui can erase by id.
//
// Erasure is keyed by the tombstone's user id, never a username. The one place
// a username is consulted is media-issues.json: entries written before the
// reporter id was stored carry only a username, and are anonymised when it is
// the erased user's own username (read from their sessions before those go) or
// no longer resolves to a live account.
//
// # Atomicity
//
// The stores are separate files with separate in-memory copies, so one
// transaction is not available. Apply instead:
//
//  1. reads every input that could fail first (the applied record, the Quick
//     Connect file, the live user directory when legacy rows need it) and
//     changes nothing if one cannot be read;
//  2. then applies each store's deletion, each by temp file + rename with the
//     in-memory copy swapped only after the rename;
//  3. and records the erasure id LAST, after all of them persisted.
//
// Every step is an idempotent deletion of that user's rows, so a failure part
// way leaves the id unrecorded and the next sweep repeats the finished steps
// as no-ops. The record is never written ahead of the data it vouches for.

const (
	erasureAppliedFileName      = "erasure-applied.json"
	erasureAppliedFormatVersion = 1
	// userIDLookupTTL bounds how often the anonymous password-reset endpoint
	// asks the identity provider for its user list.
	userIDLookupTTL     = 30 * time.Second
	userIDLookupTimeout = 3 * time.Second
	// erasureShutdownGrace bounds how long a stop waits for a running sweep.
	erasureShutdownGrace = 5 * time.Second
)

// Detail codes acknowledged to the provider on a failed application. They are
// shown on admin-ui /users, so they never carry personal data.
const (
	erasureDetailAppliedRead  = "applied_record_unreadable"
	erasureDetailAppliedWrite = "applied_record_persist_failed"
	erasureDetailUsers        = "user_list_unavailable"
	erasureDetailQuickConnect = "quickconnect_persist_failed"
	erasureDetailSessions     = "sessions_persist_failed"
	erasureDetailUserdata     = "userdata_erase_failed"
	erasureDetailRooms        = "watch_together_persist_failed"
	erasureDetailIssues       = "media_issues_persist_failed"
	erasureDetailInvalid      = "invalid_tombstone"
)

// erasureUserLister lists the identity provider's live accounts.
type erasureUserLister interface {
	ListUsers(ctx context.Context) ([]*authv1.UserInfo, error)
}

// providerUsers lists accounts through the verified provider connection of the
// erasure dialer (mesh mTLS, certificate CN equal to the discovered provider).
type providerUsers struct {
	dialer   *erasure.ProviderDialer
	moduleID string
}

func (p providerUsers) ListUsers(ctx context.Context) ([]*authv1.UserInfo, error) {
	conn, err := p.dialer.Dial(ctx, p.moduleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	resp, err := conn.Client.ListUsers(ctx, &authv1.ListUsersRequest{})
	if err != nil {
		return nil, fmt.Errorf("list users from %s: %w", conn.Provider.ModuleID, err)
	}
	return resp.GetUsers(), nil
}

// userIDResolver resolves a username to a user id ("" when unknown or
// ambiguous).
type userIDResolver interface {
	ResolveUserID(ctx context.Context, username string) string
}

// cachedUserIDs resolves usernames from one cached copy of the provider's user
// list, refreshed at most once per ttl (also after a failure), so the anonymous
// POST /api/password-reset cannot turn into a stream of provider calls.
type cachedUserIDs struct {
	lister erasureUserLister
	ttl    time.Duration
	now    func() time.Time

	mu  sync.Mutex
	at  time.Time
	ids map[string]string // username -> id; "" marks a name two accounts share
}

func newCachedUserIDs(l erasureUserLister) *cachedUserIDs {
	return &cachedUserIDs{lister: l, ttl: userIDLookupTTL, now: time.Now}
}

func (c *cachedUserIDs) ResolveUserID(ctx context.Context, username string) string {
	username = strings.TrimSpace(username)
	if c == nil || c.lister == nil || username == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at.IsZero() || c.now().Sub(c.at) >= c.ttl {
		c.at = c.now()
		c.ids = nil
		lctx, cancel := context.WithTimeout(ctx, userIDLookupTimeout)
		users, err := c.lister.ListUsers(lctx)
		cancel()
		if err != nil {
			log.Printf("warn: password-reset user lookup unavailable: %v", err)
			return ""
		}
		ids := make(map[string]string, len(users))
		for _, u := range users {
			name, id := strings.TrimSpace(u.GetUsername()), strings.TrimSpace(u.GetId())
			if name == "" || id == "" {
				continue
			}
			if prev, dup := ids[name]; dup && prev != id {
				ids[name] = ""
				continue
			}
			ids[name] = id
		}
		c.ids = ids
	}
	return c.ids[username]
}

// resolveResetUserID is the user id stored on a new password-reset request. An
// id the erasure ledger lists is never stored.
func (s *server) resolveResetUserID(ctx context.Context, username string) string {
	if s.resetUsers == nil {
		return ""
	}
	id := strings.TrimSpace(s.resetUsers.ResolveUserID(ctx, username))
	if id == "" || s.userErased(id) {
		return ""
	}
	return id
}

// userErased reports whether id is in the last-seen erasure ledger.
func (s *server) userErased(userID string) bool {
	userID = strings.TrimSpace(userID)
	return userID != "" && s.erased != nil && s.erased(userID)
}

// setErasedCheck installs the ledger check on every store that writes on
// behalf of a user id. Call it before the server accepts requests.
func (s *server) setErasedCheck(fn func(userID string) bool) {
	s.erased = fn
	if s.sessions != nil {
		s.sessions.erased = fn
	}
	if s.quickconnect != nil {
		s.quickconnect.erased = fn
	}
	if s.userdata != nil {
		s.userdata.erased = fn
	}
}

type erasureAppliedFile struct {
	Version int `json:"version"`
	// Applied maps erasure id -> RFC 3339 UTC time it was applied.
	Applied map[string]string `json:"applied"`
}

// bffErasureOwner implements erasure.Owner and erasure.Verifier.
type bffErasureOwner struct {
	id           string
	dir          string // directory of erasure-applied.json; "" = in memory
	sessions     *sessionStore
	quickconnect *quickConnectStore
	userdata     *serverUserdata
	issues       *mediaIssueStore
	together     *watchTogetherStore
	users        erasureUserLister // nil = legacy usernames cannot be resolved
	now          func() time.Time

	mu      sync.Mutex // serialises Apply and the applied record
	applied map[string]string
}

var (
	_ erasure.Owner    = (*bffErasureOwner)(nil)
	_ erasure.Verifier = (*bffErasureOwner)(nil)
)

// newBFFErasureOwner builds the owner over the server's stores. moduleID must
// equal the CN of the BFF's mesh certificate: the provider attributes
// acknowledgements to the verified CN.
func (s *server) newBFFErasureOwner(moduleID, dir string, users erasureUserLister) *bffErasureOwner {
	return &bffErasureOwner{
		id: moduleID, dir: strings.TrimSpace(dir),
		sessions: s.sessions, quickconnect: s.quickconnect, userdata: s.userdata,
		issues: s.issues, together: s.together,
		users: users, now: time.Now,
		applied: map[string]string{},
	}
}

// ModuleID implements erasure.Owner.
func (o *bffErasureOwner) ModuleID() string { return o.id }

func (o *bffErasureOwner) appliedPath() string {
	if o.dir == "" {
		return ""
	}
	return filepath.Join(o.dir, erasureAppliedFileName)
}

// readApplied loads the applied record. A missing file is an empty record; an
// unreadable or malformed one is an error, so a damaged record is never
// replaced by an empty one. Callers hold o.mu.
func (o *bffErasureOwner) readApplied() (erasureAppliedFile, error) {
	f := erasureAppliedFile{Version: erasureAppliedFormatVersion, Applied: map[string]string{}}
	path := o.appliedPath()
	if path == "" {
		for k, v := range o.applied {
			f.Applied[k] = v
		}
		return f, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // operator data dir
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return erasureAppliedFile{}, fmt.Errorf("read %s: %w", erasureAppliedFileName, err)
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return erasureAppliedFile{}, fmt.Errorf("%s is not valid JSON: %w", erasureAppliedFileName, err)
	}
	if f.Applied == nil {
		f.Applied = map[string]string{}
	}
	return f, nil
}

func (o *bffErasureOwner) writeApplied(f erasureAppliedFile) error {
	path := o.appliedPath()
	if path == "" {
		o.applied = f.Applied
		return nil
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw)
}

// Applied implements erasure.Owner.
func (o *bffErasureOwner) Applied(_ context.Context, erasureID string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f, err := o.readApplied()
	if err != nil {
		return false, err
	}
	_, ok := f.Applied[erasureID]
	return ok, nil
}

// liveUsernames returns the set of usernames that still resolve to an account.
// An empty directory is refused: a household always has at least the
// administrator that performed the deletion, so an empty answer is a provider
// fault, and acting on it would anonymise every legacy issue.
func (o *bffErasureOwner) liveUsernames(ctx context.Context) (map[string]struct{}, error) {
	if o.users == nil {
		return nil, errors.New("no user directory configured")
	}
	users, err := o.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]struct{}, len(users))
	for _, u := range users {
		if name := strings.TrimSpace(u.GetUsername()); name != "" {
			live[name] = struct{}{}
		}
	}
	if len(live) == 0 {
		return nil, errors.New("identity provider returned no users")
	}
	return live, nil
}

// Apply implements erasure.Owner. Applying an erasure id that is already
// recorded changes nothing.
func (o *bffErasureOwner) Apply(ctx context.Context, t erasure.Tombstone) (erasure.Counts, error) {
	if t.ErasureID == "" || t.UserID == "" {
		return nil, erasure.WithDetail(erasureDetailInvalid, errors.New("tombstone without erasure id or user id"))
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	rec, err := o.readApplied()
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailAppliedRead, err)
	}
	if _, done := rec.Applied[t.ErasureID]; done {
		return erasure.Counts{}, nil
	}

	// Phase 1: read what could fail, change nothing.
	known := map[string]struct{}{}
	for _, name := range o.sessions.usernamesFor(t.UserID, t.TenantID) {
		known[name] = struct{}{}
	}
	qcNames, err := o.quickconnect.usernamesFor(t.UserID, t.TenantID)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailQuickConnect, fmt.Errorf("read quickconnect.json: %w", err))
	}
	for _, name := range qcNames {
		known[name] = struct{}{}
	}
	var live map[string]struct{}
	if o.issues != nil && len(o.issues.legacyReportersUnaccounted(known)) > 0 {
		// Legacy issues name a person only by username; whether that person
		// still exists decides if the row is anonymised. Fail closed.
		live, err = o.liveUsernames(ctx)
		if err != nil {
			return nil, erasure.WithDetail(erasureDetailUsers, err)
		}
	}

	// Phase 2: the deletions, the applied record last.
	counts := erasure.Counts{}
	n, err := o.sessions.eraseUser(t.UserID, t.TenantID)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailSessions, fmt.Errorf("persist sessions.json: %w", err))
	}
	counts["sessions"] = int64(n)

	n, err = o.quickconnect.eraseUser(t.UserID, t.TenantID)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailQuickConnect, fmt.Errorf("persist quickconnect.json: %w", err))
	}
	counts["quickconnect_codes"] = int64(n)

	blobs, markers, err := o.userdata.eraseUser(t.UserID, t.TenantID)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailUserdata, fmt.Errorf("erase local userdata: %w", err))
	}
	counts["userdata_blobs"] = int64(blobs)
	counts["migration_markers"] = int64(markers)

	n, err = o.together.eraseHost(t.UserID)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailRooms, fmt.Errorf("persist watch-together.json: %w", err))
	}
	counts["watch_together_rooms"] = int64(n)

	n, err = o.issues.anonymiseReporter(t.UserID, known, live)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailIssues, fmt.Errorf("persist media-issues.json: %w", err))
	}
	counts["media_issues_anonymised"] = int64(n)

	rec.Applied[t.ErasureID] = o.now().UTC().Format(time.RFC3339)
	rec.Version = erasureAppliedFormatVersion
	if err := o.writeApplied(rec); err != nil {
		return nil, erasure.WithDetail(erasureDetailAppliedWrite, fmt.Errorf("persist %s: %w", erasureAppliedFileName, err))
	}
	return counts, nil
}

// Verify implements erasure.Verifier: rows the disposition removes or
// anonymises that still carry the user id. Legacy username-only issues carry no
// id and are not counted. The password-reset queue is admin-ui's.
func (o *bffErasureOwner) Verify(_ context.Context, t erasure.Tombstone) (int, error) {
	remaining := o.sessions.countUser(t.UserID, t.TenantID)
	n, err := o.quickconnect.countUser(t.UserID, t.TenantID)
	if err != nil {
		return 0, fmt.Errorf("read quickconnect.json: %w", err)
	}
	remaining += n
	n, err = o.userdata.countUser(t.UserID, t.TenantID)
	if err != nil {
		return 0, fmt.Errorf("read local userdata: %w", err)
	}
	remaining += n
	remaining += o.together.countHost(t.UserID)
	remaining += o.issues.countReporter(t.UserID)
	return remaining, nil
}

// ---- reconciler wiring --------------------------------------------------------

// erasureProfileRequired reports whether the deployment profile makes the
// reconciler mandatory: household (and its alias staging) must not run without
// it.
func erasureProfileRequired(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("MUXCORE_PROFILE"))) {
	case "household", "staging":
		return true
	}
	return false
}

// discoveryClient adapts a core connection to erasure.CapabilityFinder.
type discoveryClient struct{ conn *grpc.ClientConn }

func (d discoveryClient) FindByCapability(ctx context.Context, in *discoveryv1.FindByCapabilityRequest, opts ...grpc.CallOption) (*discoveryv1.FindByCapabilityResponse, error) {
	return discoveryv1.NewDiscoveryServiceClient(d.conn).FindByCapability(ctx, in, opts...)
}

// erasureSetup is the input of setupErasure.
type erasureSetup struct {
	Getenv func(string) string
	// ModuleID must equal the CN of the BFF's mesh certificate.
	ModuleID string
	// UserdataDir holds erasure-applied.json (MEDIA_UI_USERDATA_DIR).
	UserdataDir string
	// Finder is core's DiscoveryService client. nil means no core connection.
	Finder erasure.CapabilityFinder
	// Interval overrides ERASURE_SWEEP_INTERVAL (tests).
	Interval time.Duration
	// Tune adjusts the reconciler config (tests).
	Tune func(*erasure.Config)
}

// setupErasure builds the BFF's reconciler and installs the ledger check on the
// server's stores. It returns (nil, nil) when the reconciler is disabled: the
// household and staging profiles refuse to start without a core connection
// (the caller exits); other profiles warn and run without it.
func (s *server) setupErasure(in erasureSetup) (*erasure.Reconciler, error) {
	if in.Getenv == nil {
		in.Getenv = os.Getenv
	}
	if strings.TrimSpace(in.Getenv("MUXCORE_GRPC_ADDR")) == "" || in.Finder == nil {
		if erasureProfileRequired(in.Getenv) {
			return nil, errors.New("erasure reconciler: the household profile requires a core connection (MUXCORE_GRPC_ADDR)")
		}
		log.Printf("warn: media-ui erasure reconciler disabled: no core connection configured (MUXCORE_GRPC_ADDR unset); user erasures from the identity ledger are not applied")
		return nil, nil
	}
	dialer := &erasure.ProviderDialer{Discovery: in.Finder, Getenv: in.Getenv}
	users := providerUsers{dialer: dialer, moduleID: in.ModuleID}
	owner := s.newBFFErasureOwner(in.ModuleID, in.UserdataDir, users)
	cfg := erasure.Config{
		Owner:    owner,
		Dialer:   dialer,
		Getenv:   in.Getenv,
		Interval: in.Interval,
	}
	if in.Tune != nil {
		in.Tune(&cfg)
	}
	rec, err := erasure.New(cfg)
	if err != nil {
		return nil, err
	}
	s.setErasedCheck(rec.Erased)
	s.resetUsers = newCachedUserIDs(users)
	return rec, nil
}

// runErasure starts rec and returns a stop function that cancels it and waits
// (bounded) for a running sweep to return.
func runErasure(rec *erasure.Reconciler) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := rec.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("error: media-ui erasure reconciler stopped: %v", err)
		}
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(erasureShutdownGrace):
			log.Printf("warn: media-ui erasure reconciler did not stop before the shutdown grace period")
		}
	}
}

// erasureTransportCheck refuses a plaintext mesh in the household and staging
// profiles: the ledger names deleted users and is read and acknowledged only
// over mesh mTLS there. meshtls.Insecure also honours the MUXCORE_DEV_TLS_SKIP
// and MUXCORE_GRPC_INSECURE aliases that the BFF's own insecure switch does not.
func erasureTransportCheck(getenv func(string) string, insecure bool) error {
	if insecure && erasureProfileRequired(getenv) {
		return errors.New("erasure reconciler: plaintext mesh is not allowed in the household and staging profiles")
	}
	return nil
}

// startErasure connects to core, builds the reconciler and starts its startup
// sweep. It is main's entry point: an error is a startup failure.
func (s *server) startErasure(userdataDir string) (stop func(), err error) {
	in := erasureSetup{Getenv: os.Getenv, ModuleID: meshModuleID(os.Getenv), UserdataDir: userdataDir}
	if err := erasureTransportCheck(os.Getenv, meshtls.Insecure()); err != nil {
		return nil, err
	}
	if in.UserdataDir == "" && erasureProfileRequired(os.Getenv) {
		log.Printf("warn: MEDIA_UI_USERDATA_DIR unset: the erasure record is kept in memory only; applied erasures are re-applied (idempotently) after a restart")
	}
	var conn *grpc.ClientConn
	if strings.TrimSpace(os.Getenv("MUXCORE_GRPC_ADDR")) != "" {
		// MUXCORE_TLS_* point at the BFF's enrolled identity (mustEnsureMeshIdentity
		// ran first). Plaintext only with the explicit insecure dev flag; the
		// SDK refuses it in the household and staging profiles.
		conn, err = modulesdk.Connect(modulesdk.ConnectConfig{Insecure: meshtls.Insecure()})
		if err != nil {
			return nil, fmt.Errorf("erasure reconciler: connect to core: %w", err)
		}
		in.Finder = discoveryClient{conn: conn}
	}
	rec, err := s.setupErasure(in)
	if err != nil || rec == nil {
		if conn != nil {
			_ = conn.Close()
		}
		return func() {}, err
	}
	stopRun := runErasure(rec)
	return func() {
		stopRun()
		if conn != nil {
			_ = conn.Close()
		}
	}, nil
}
