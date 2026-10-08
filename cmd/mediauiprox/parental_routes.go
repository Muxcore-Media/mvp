package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Route classes (ADR-0031 §1.2). Every pattern registered by registerRoutes
// (including registerLibraryRoutes and registerRequestMediaRoutes) must appear
// in parentalRouteClasses under the exact string passed to Handle/HandleFunc;
// parentalRegistrar panics at startup on an unclassified pattern and
// routes_inventory_test.go fails on one.
//
// All content-bearing classes enforce the authoritative policy. Browse handlers
// evaluate inline classification; playback resolves it from the owning module.
type routeClass string

const (
	classList   routeClass = "C-LIST"   // filter items; policy failure fails the response
	classItem   routeClass = "C-ITEM"   // check one item; deny the whole response
	classPlay   routeClass = "C-PLAY"   // check the item behind src or the path
	classDeny   routeClass = "C-DENY"   // no trusted classification: restricted principals get 403
	classExempt routeClass = "C-EXEMPT" // no catalogue content, or already admin/manager-gated
)

// parentalRoute is one row of the route-class table.
type parentalRoute struct {
	class routeClass
	// Detail handlers evaluate the actual response fields instead of a preflight lookup.
	itemInResponse bool
	// item locates the catalogue item behind a C-PLAY request.
	item func(*http.Request) parentalItem
	// hlsAsset marks GET /stream/hls/{key}/{file}: the item comes from the
	// session's key binding, not from the request.
	hlsAsset bool
	// blockedAlias replaces parental.blocked in the response code (resolve).
	blockedAlias string
	// roleGated marks C-EXEMPT operator routes that reject sessions without
	// the admin/manager (or admin) role, in the handler or through
	// requirePrivileged; parental_routes_test.go proves the gate fires for
	// every one of them.
	roleGated bool
	// requirePrivileged makes the route table itself reject sessions without
	// the admin or manager role (sessionHasPrivilegedRole) with 403
	// operator.forbidden. It runs before the parental gate of the route's
	// class and before the handler (T-M5-12, C-30; operator_routes.go).
	requirePrivileged bool
	// rootPathAdmin additionally requires the admin role
	// (sessionHasAdminRole) when the JSON body names root_folder_path:
	// root-folder changes are admin-only (RULE-AUTH-3). Only meaningful with
	// requirePrivileged.
	rootPathAdmin bool
}

var (
	cList   = parentalRoute{class: classList}
	cItem   = parentalRoute{class: classItem}
	cDeny   = parentalRoute{class: classDeny}
	cExempt = parentalRoute{class: classExempt}
	// cOperator is C-EXEMPT because the handler already rejects sessions
	// without the admin/manager (or admin) role before touching catalogue data.
	cOperator = parentalRoute{class: classExempt, roleGated: true}
	// cOperatorGate is an operator route whose admin/manager check is
	// enforced by the route table (requirePrivileged), not by the handler.
	cOperatorGate = parentalRoute{class: classExempt, roleGated: true, requirePrivileged: true}
	// cOperatorRootPath is cOperatorGate for an item PATCH that forwards
	// root_folder_path: a body naming it also needs admin.
	cOperatorRootPath = parentalRoute{class: classExempt, roleGated: true, requirePrivileged: true, rootPathAdmin: true}
	// cDenyOperator keeps C-DENY for restricted principals and is reachable
	// only by admin/manager.
	cDenyOperator = parentalRoute{class: classDeny, requirePrivileged: true}
	// cSelf is C-EXEMPT: the signed-in user's own account or session data.
	cSelf = parentalRoute{class: classExempt}
	// cPlaySrc checks the item named by the ?src=/stream/{movies|tv}/<id> query.
	cPlaySrc = parentalRoute{class: classPlay, item: parentalItemFromSrcQuery}
	// cPlayPath checks the item named by the request path itself.
	cPlayPath = parentalRoute{class: classPlay, item: parentalItemFromRequestPath}
	// cPlayUnknown cannot name an item from the request (classification unavailable).
	cPlayUnknown = parentalRoute{class: classPlay, item: func(*http.Request) parentalItem { return parentalItem{} }}
)

var parentalRouteClasses = map[string]parentalRoute{
	// --- C-PLAY ---
	"GET /api/playback/resolve":        {class: classPlay, item: parentalItemFromSrcQuery, blockedAlias: playbackCodeParentalBlocked},
	"GET /stream/transcode":            cPlaySrc,
	"GET /stream/hls":                  cPlaySrc,
	"GET /stream/hls/{key}/{file}":     {class: classPlay, hlsAsset: true},
	"GET /stream/trickplay":            cPlaySrc,
	"/stream/movies/":                  cPlayPath,
	"/stream/tv/":                      cPlayPath,
	"GET /api/playback/subtitles":      cPlaySrc,
	"GET /api/playback/subtitles/{id}": cPlayUnknown, // subtitle id; the item is not named
	"GET /api/playback/segments": {class: classPlay, item: func(r *http.Request) parentalItem {
		return parentalItem{ID: strings.TrimSpace(r.URL.Query().Get("media_id"))}
	}},
	"GET /api/playback/chapters": cPlaySrc,
	"GET /api/playback/analysis": cPlaySrc,

	// --- C-LIST ---
	"/api/movies":               cList, // includes ?library=
	"/api/tv":                   cList,
	"GET /api/collections":      cList,
	"GET /api/collections/{id}": cList,
	"GET /api/collections/":     cList,

	// --- C-ITEM ---
	"/api/movies/":                   {class: classItem, itemInResponse: true},
	"/api/tv/":                       {class: classItem, itemInResponse: true}, // episodes are classified by their series
	"GET /api/movies/{id}/tags":      cItem,
	"GET /api/tv/{id}/tags":          cItem,
	"GET /api/movies/{id}/titles":    cItem,
	"GET /api/tv/{id}/titles":        cItem,
	"GET /api/movies/{id}/history":   cItem,
	"GET /api/tv/{id}/history":       cItem,
	"GET /api/movies/{id}/artwork":   cItem,
	"GET /api/tv/{id}/artwork":       cItem,
	"GET /api/movies/{id}/files":     cItem,
	"GET /api/movies/{id}/subtitles": cItem,
	"GET /api/tv/{id}/subtitles":     cItem,
	"GET /api/episodes/{id}/file":    cItem,
	// Not in ADR-0031 §1.2: the response is the full item. Operator-only
	// (T-M5-12); root_folder_path needs admin. The role gate runs first, so
	// the S5b item check only ever sees admin/manager principals.
	"PATCH /api/movies/{id}": {class: classItem, requirePrivileged: true, rootPathAdmin: true},
	"PATCH /api/tv/{id}":     {class: classItem, requirePrivileged: true, rootPathAdmin: true},

	// --- C-DENY: external catalogue ---
	"/api/discover/":               cDeny,
	"/api/search":                  cDeny,
	"/api/request":                 cDeny,
	"/api/requests/":               cDeny,
	"/api/requests":                cDeny,
	"/api/request-policy":          cDeny,
	"GET /api/graph/related":       cDeny,
	"/api/watchlist":               cDeny,
	"GET /api/subtitles/search":    cDeny,         // not in ADR §1.2: provider search by title
	"POST /api/subtitles/download": cDenyOperator, // not in ADR §1.2: provider fetch for an item
	"POST /api/wanted":             cDenyOperator, // not in ADR §1.2: acquisition request
	"POST /api/releases/grab":      cDenyOperator, // not in ADR §1.2: acquisition grab
	// --- C-DENY: external players ---
	"/api/jellyfin/play":       cDeny,
	"GET /api/jellyfin/link":   cDeny, // not in ADR §1.2: returns title and path
	"/api/plex/play":           cDeny,
	"GET /api/plex/sync-lists": cDeny, // not in ADR §1.2: returns titles
	"GET /api/debrid/vfs":      cDeny,
	"GET /api/debrid/stream":   cDeny,
	"POST /api/debrid/add":     cDenyOperator, // not in ADR §1.2: debrid acquisition
	// --- C-DENY: live TV ---
	"GET /api/livetv":         cDeny,
	"POST /api/livetv/timers": cDenyOperator, // not in ADR §1.2
	// --- C-DENY: non-video libraries ---
	"GET /api/music":                    cDeny,
	"GET /api/books":                    cDeny,
	"GET /api/comics":                   cDeny,
	"GET /api/audiobooks":               cDeny,
	"GET /api/music/":                   cDeny,
	"GET /api/books/":                   cDeny,
	"GET /api/comics/":                  cDeny,
	"GET /api/audiobooks/":              cDeny,
	"GET /stream/music/":                cDeny,
	"GET /stream/books/":                cDeny,
	"GET /stream/comics/":               cDeny,
	"GET /stream/audiobooks/":           cDeny,
	"GET /api/music/tracks/{id}/lyrics": cDeny,
	// Not in ADR §1.2: ungated per-item reads of non-video libraries.
	"GET /api/music/{id}/history":      cDeny,
	"GET /api/music/{id}/artwork":      cDeny,
	"GET /api/music/{id}/tags":         cDeny,
	"GET /api/music/{id}/files":        cDeny,
	"GET /api/books/{id}/history":      cDeny,
	"GET /api/books/{id}/artwork":      cDeny,
	"GET /api/books/{id}/tags":         cDeny,
	"GET /api/comics/{id}/artwork":     cDeny,
	"GET /api/comics/{id}/history":     cDeny,
	"GET /api/audiobooks/{id}/artwork": cDeny,
	"GET /api/audiobooks/{id}/history": cDeny,
	// --- C-DENY: title-revealing feeds with no role gate ---
	"GET /api/calendar":                cDeny,
	"GET /api/activity":                cDeny,
	"GET /api/wanted":                  cDeny,
	"GET /api/missing":                 cDeny,
	"GET /api/history":                 cDeny,
	"GET /api/sessions/events":         cDeny,
	"GET /api/sessions":                cDeny,
	"GET /api/watch-stats/item":        cDeny,
	"/api/watch-together":              cDeny,
	"/api/watch-together/":             cDeny,
	"/api/media-issues":                cDeny,
	"GET /api/playback/segments/media": cDeny,
	// Not in ADR §1.2: ungated feeds that list titles, file or release names.
	"GET /api/rename/preview":         cDeny,
	"POST /api/rename":                cDenyOperator,
	"GET /api/releases/search":        cDeny,
	"GET /api/releases/upgrades":      cDeny,
	"GET /api/blocklist":              cDeny,
	"GET /api/import/candidates":      cDeny,
	"GET /api/watch-stats":            cDeny,
	"GET /api/watch-stats/stale":      cDeny,
	"GET /api/watch-stats/duplicates": cDeny,

	// --- C-EXEMPT: service, session and auth ---
	"/healthz":                             cExempt,
	"/":                                    cExempt, // SPA static files
	"/login":                               cExempt,
	"/auth/callback":                       cExempt,
	"GET /logout":                          cExempt,
	"POST /logout":                         cExempt,
	"GET /api/capabilities":                cExempt,
	"GET /api/session":                     cSelf,
	"GET /api/me":                          cSelf,
	"/api/quickconnect":                    cExempt,
	"POST /api/tv/login":                   cExempt,
	"/api/tv/login/totp":                   cExempt,
	"GET /api/mobile/auth/login":           cExempt,
	"GET /api/mobile/auth/done":            cExempt,
	"POST /api/mobile/session":             cExempt,
	"/api/password-reset":                  cExempt,
	"GET /api/invite/peek":                 cExempt,
	"POST /api/invite/redeem":              cExempt,
	"/api/userdata":                        cSelf, // own blob; client-written titles are out of scope (ADR-0031 §6)
	"POST /api/playback/session":           cSelf, // telemetry
	"GET /api/totp":                        cSelf,
	"POST /api/totp":                       cSelf,
	"DELETE /api/totp":                     cSelf,
	"POST /api/totp/verify":                cSelf,
	"GET /api/passkeys":                    cSelf,
	"POST /api/passkeys/register/begin":    cSelf,
	"POST /api/passkeys/register/complete": cSelf,
	"DELETE /api/passkeys/{id}":            cSelf,
	// Images stay public and are not covered by ADR-0031 (§1.3, §6 residual).
	"/images/movies/":     cExempt,
	"/images/tv/":         cExempt,
	"/images/music/":      cExempt,
	"/images/books/":      cExempt,
	"/images/audiobooks/": cExempt,
	"/images/comics/":     cExempt,

	// --- C-EXEMPT: no catalogue content in the response. No BFF role gate:
	// reads any signed-in session may make, and formats score/parse, which
	// only compute. The state-changing routes that used to sit here are
	// operator routes below (T-M5-12).
	"GET /api/roots":                       cExempt,
	"GET /api/roots/pick":                  cExempt,
	"GET /api/formats":                     cExempt,
	"GET /api/formats/release-profiles":    cExempt,
	"POST /api/formats/score":              cExempt,
	"POST /api/formats/parse":              cExempt,
	"GET /api/acquisition":                 cExempt,
	"GET /api/indexers":                    cExempt,
	"GET /api/delay-profiles":              cExempt,
	"GET /api/tags":                        cExempt, // tag vocabulary, not items
	"GET /api/tv/{id}/override":            cExempt,
	"GET /api/watch-stats/storage":         cExempt, // aggregates only
	"GET /api/watch-stats/storage-history": cExempt, // aggregates only
	"GET /api/watch-stats/charts":          cExempt, // buckets by hour/user/platform

	// --- C-EXEMPT: operator routes gated to admin/manager by this table
	// (T-M5-12, C-30). Their handlers have no role check of their own. ---
	// Library removal and file deletion (delete_files=1 deletes from disk;
	// privileged like DELETE /api/movies/{id}/files/{fileId}).
	"DELETE /api/movies/{id}":        cOperatorGate,
	"DELETE /api/movies/{id}/file":   cOperatorGate,
	"DELETE /api/tv/{id}":            cOperatorGate,
	"DELETE /api/episodes/{id}/file": cOperatorGate,
	"DELETE /api/music/{id}":         cOperatorGate,
	"DELETE /api/books/{id}":         cOperatorGate,
	"DELETE /api/books/works/{id}":   cOperatorGate,
	"DELETE /api/comics/{id}":        cOperatorGate,
	"DELETE /api/comics/issues/{id}": cOperatorGate,
	"DELETE /api/audiobooks/{id}":    cOperatorGate,
	"POST /api/movies/{id}/refresh":  cOperatorGate,
	"POST /api/tv/{id}/refresh":      cOperatorGate,
	"POST /api/music/{id}/refresh":   cOperatorGate,
	// Monitoring and quality profile; root_folder_path needs admin.
	"PATCH /api/tv/seasons/{id}":    cOperatorGate,
	"PATCH /api/episodes/{id}":      cOperatorGate,
	"PATCH /api/music/{id}":         cOperatorRootPath,
	"PATCH /api/music/albums/{id}":  cOperatorGate,
	"PATCH /api/books/{id}":         cOperatorRootPath,
	"PATCH /api/books/works/{id}":   cOperatorGate,
	"PATCH /api/comics/{id}":        cOperatorRootPath,
	"PATCH /api/comics/issues/{id}": cOperatorGate,
	"PATCH /api/audiobooks/{id}":    cOperatorRootPath,
	"PUT /api/tv/{id}/override":     cOperatorGate,
	"POST /api/tv/{id}/override":    cOperatorGate,
	"DELETE /api/tv/{id}/override":  cOperatorGate,
	// Acquisition.
	"POST /api/releases/search-now": cOperatorGate,
	"POST /api/releases/block":      cOperatorGate,
	"POST /api/wanted/remove":       cOperatorGate,
	"POST /api/activity/retry":      cOperatorGate,
	"POST /api/blocklist/clear":     cOperatorGate,
	"PUT /api/delay-profiles":       cOperatorGate,
	"POST /api/delay-profiles":      cOperatorGate,
	"POST /api/formats/sync-trash":  cOperatorGate, // official and non-official
	"POST /api/import":              cOperatorGate,
	// Stops any user's session, including Jellyfin/Plex. Ownership cannot be
	// established from trusted data (BFF-API.md "Operator route roles").
	"POST /api/sessions/{id}/stop": cOperatorGate,

	// --- C-EXEMPT: operator routes already gated to admin/manager ---
	"GET /api/roots/browse":                         cOperator,
	"POST /api/roots/probe":                         cOperator,
	"POST /api/roots":                               cOperator,
	"PATCH /api/roots/{id}":                         cOperator,
	"DELETE /api/roots/{id}":                        cOperator,
	"POST /api/rename/organize":                     cOperator,
	"GET /api/rename/templates":                     cOperator,
	"POST /api/rename/templates":                    cOperator,
	"PATCH /api/rename/templates/{id}":              cOperator,
	"DELETE /api/rename/templates/{id}":             cOperator,
	"POST /api/formats/release-profiles":            cOperator,
	"PATCH /api/formats/release-profiles/{id}":      cOperator,
	"PUT /api/formats/release-profiles/{id}":        cOperator,
	"DELETE /api/formats/release-profiles/{id}":     cOperator,
	"POST /api/formats/profiles":                    cOperator,
	"PATCH /api/formats/profiles/{id}":              cOperator,
	"DELETE /api/formats/profiles/{id}":             cOperator,
	"POST /api/formats":                             cOperator,
	"PATCH /api/formats/{id}":                       cOperator,
	"DELETE /api/formats/{id}":                      cOperator,
	"POST /api/indexers":                            cOperator,
	"PATCH /api/indexers/{id}":                      cOperator,
	"DELETE /api/indexers/{id}":                     cOperator,
	"GET /api/scan/watch-dirs":                      cOperator,
	"POST /api/scan/watch-dirs":                     cOperator,
	"PATCH /api/scan/watch-dirs/{id}":               cOperator,
	"PUT /api/scan/watch-dirs/{id}":                 cOperator,
	"DELETE /api/scan/watch-dirs/{id}":              cOperator,
	"GET /api/scan":                                 cOperator,
	"POST /api/scan":                                cOperator,
	"PUT /api/movies/{id}/tags":                     cOperator,
	"POST /api/movies/{id}/tags":                    cOperator,
	"POST /api/movies/{id}/titles":                  cOperator,
	"DELETE /api/movies/{id}/titles/{titleId}":      cOperator,
	"POST /api/movies/{id}/artwork":                 cOperator,
	"POST /api/movies/{id}/subtitles":               cOperator,
	"DELETE /api/movies/{id}/files/{fileId}":        cOperator,
	"PATCH /api/collections/{id}":                   cOperator,
	"PUT /api/collections/{id}":                     cOperator,
	"POST /api/collections/{id}/sync":               cOperator,
	"PUT /api/tv/{id}/tags":                         cOperator,
	"POST /api/tv/{id}/tags":                        cOperator,
	"POST /api/tv/{id}/titles":                      cOperator,
	"DELETE /api/tv/{id}/titles/{titleId}":          cOperator,
	"POST /api/tv/{id}/artwork":                     cOperator,
	"POST /api/tv/{id}/subtitles":                   cOperator,
	"GET /api/jellyfin/status":                      cOperator,
	"POST /api/jellyfin/sync":                       cOperator,
	"POST /api/jellyfin/refresh":                    cOperator,
	"DELETE /api/jellyfin/link":                     cOperator,
	"POST /api/jellyfin/match":                      cOperator,
	"POST /api/watch-stats/import-tautulli":         cOperator,
	"POST /api/watch-stats/import-jellystat":        cOperator,
	"GET /api/guard/rules":                          cOperator,
	"PUT /api/guard/rules":                          cOperator,
	"POST /api/guard/rules":                         cOperator,
	"DELETE /api/guard/rules/{id}":                  cOperator,
	"POST /api/guard/violations/ack":                cOperator,
	"POST /api/guard/trust/reset":                   cOperator,
	"POST /api/guard/users/merge":                   cOperator,
	"GET /api/guard":                                cOperator,
	"GET /api/subtitles/blacklist":                  cOperator,
	"DELETE /api/subtitles/blacklist/{id}":          cOperator,
	"GET /api/subtitles/wanted":                     cOperator,
	"POST /api/subtitles/wanted":                    cOperator,
	"POST /api/subtitles/wanted/search":             cOperator,
	"DELETE /api/subtitles/wanted/{id}":             cOperator,
	"GET /api/subtitles/providers":                  cOperator,
	"PUT /api/subtitles/providers/{id}":             cOperator,
	"POST /api/subtitles/providers/{id}":            cOperator,
	"GET /api/subtitles/history":                    cOperator,
	"POST /api/subtitles/history/clear":             cOperator,
	"GET /api/subtitles/profiles":                   cOperator,
	"PUT /api/subtitles/profiles":                   cOperator,
	"POST /api/subtitles/profiles":                  cOperator,
	"GET /api/subtitles/languages":                  cOperator,
	"GET /api/subtitles/media":                      cOperator,
	"POST /api/subtitles/media/mass-edit":           cOperator,
	"PATCH /api/subtitles/media/{id}":               cOperator,
	"DELETE /api/subtitles/files/{id}":              cOperator,
	"GET /api/tagging":                              cOperator,
	"POST /api/tagging/tags":                        cOperator,
	"DELETE /api/tagging/tags/{id}":                 cOperator,
	"PUT /api/tagging/rules":                        cOperator,
	"POST /api/tagging/rules":                       cOperator,
	"DELETE /api/tagging/rules/{id}":                cOperator,
	"POST /api/tagging/classify":                    cOperator,
	"POST /api/tags":                                cOperator,
	"DELETE /api/tags/{id}":                         cOperator,
	"GET /api/watch-notify":                         cOperator,
	"PUT /api/watch-notify/rules":                   cOperator,
	"POST /api/watch-notify/rules":                  cOperator,
	"DELETE /api/watch-notify/rules/{id}":           cOperator,
	"PUT /api/watch-notify/destinations":            cOperator,
	"POST /api/watch-notify/destinations":           cOperator,
	"DELETE /api/watch-notify/destinations/{id}":    cOperator,
	"POST /api/watch-notify/destinations/{id}/test": cOperator,
	"GET /api/notifications":                        cOperator,
	"PUT /api/notifications":                        cOperator,
	"POST /api/notifications":                       cOperator,
	"POST /api/notifications/test":                  cOperator,
	"GET /api/maintainer":                           cOperator,
	"POST /api/maintainer/scan":                     cOperator,
	"POST /api/maintainer/act":                      cOperator,
	"POST /api/maintainer/rules":                    cOperator,
	"POST /api/maintainer/rules/preview":            cOperator,
	"GET /api/maintainer/rules/export":              cOperator,
	"POST /api/maintainer/rules/import":             cOperator,
	"POST /api/maintainer/rules/{id}/toggle":        cOperator,
	"DELETE /api/maintainer/rules/{id}":             cOperator,
	"POST /api/maintainer/protections":              cOperator,
	"DELETE /api/maintainer/protections/{id}":       cOperator,
	"POST /api/maintainer/collections":              cOperator,
	"DELETE /api/maintainer/collections/{id}":       cOperator,
	"POST /api/maintainer/exclusions":               cOperator,
	"POST /api/maintainer/exclusions/sync":          cOperator,
	"DELETE /api/maintainer/exclusions/{id}":        cOperator,
	"POST /api/maintainer/candidates/{id}/{action}": cOperator,
	"GET /api/backups":                              cOperator,
	"POST /api/backups":                             cOperator,
	"DELETE /api/backups/{id}":                      cOperator,
	"POST /api/backups/{id}/restore":                cOperator,
	"PUT /api/playback/segments":                    cOperator,
	"DELETE /api/playback/segments":                 cOperator,
	"POST /api/password-reset/{id}/dismiss":         cOperator,
	"POST /api/password-reset/{id}/password":        cOperator,
	"GET /api/invites":                              cOperator,
	"POST /api/invites":                             cOperator,
	"DELETE /api/invites/{id}":                      cOperator,
	"GET /api/users":                                cOperator,
	"POST /api/users":                               cOperator,
	"POST /api/users/{id}/password":                 cOperator,
	"PUT /api/users/{id}/password":                  cOperator,
	"PATCH /api/users/{id}":                         cOperator,
	"DELETE /api/users/{id}":                        cOperator,
	"GET /api/keys":                                 cOperator,
	"POST /api/keys":                                cOperator,
	"POST /api/keys/{id}/rotate":                    cOperator,
	"DELETE /api/keys/{id}":                         cOperator,
	"GET /api/lists/history":                        cOperator,
	"GET /api/lists/items":                          cOperator,
	"GET /api/lists":                                cOperator,
	"POST /api/lists":                               cOperator,
	"POST /api/lists/sync":                          cOperator,
	"POST /api/lists/{id}/sync":                     cOperator,
	"POST /api/lists/{id}/test":                     cOperator,
	"PATCH /api/lists/{id}":                         cOperator,
	"PUT /api/lists/{id}":                           cOperator,
	"DELETE /api/lists/{id}":                        cOperator,
	"POST /api/migrate":                             cOperator,
	"POST /api/books":                               cOperator,
	"POST /api/comics":                              cOperator,
	"POST /api/music/albums/{id}/import":            cOperator,
	"POST /api/music":                               cOperator,
	"POST /api/music/{id}/albums":                   cOperator,
	"POST /api/music/{id}/artwork":                  cOperator,
	"PUT /api/music/{id}/tags":                      cOperator,
	"POST /api/music/{id}/tags":                     cOperator,
	"DELETE /api/music/{id}/files/{fileId}":         cOperator,
	"POST /api/books/works/{id}/import":             cOperator,
	"POST /api/books/{id}/books":                    cOperator,
	"POST /api/books/{id}/artwork":                  cOperator,
	"PUT /api/books/{id}/tags":                      cOperator,
	"POST /api/books/{id}/tags":                     cOperator,
	"POST /api/comics/issues/{id}/import":           cOperator,
	"POST /api/comics/{id}/issues":                  cOperator,
	"POST /api/comics/{id}/artwork":                 cOperator,
	"POST /api/audiobooks":                          cOperator,
	"POST /api/audiobooks/{id}/import":              cOperator,
	"POST /api/audiobooks/{id}/artwork":             cOperator,
}

// parentalRegistrar wraps every registered handler according to its class.
type parentalRegistrar struct {
	s    *server
	next routeRegistrar
}

func (p parentalRegistrar) Handle(pattern string, h http.Handler) {
	p.next.Handle(pattern, p.s.parentalWrap(pattern, h))
}

func (p parentalRegistrar) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	p.next.Handle(pattern, p.s.parentalWrap(pattern, http.HandlerFunc(h)))
}

func (s *server) parentalWrap(pattern string, h http.Handler) http.Handler {
	route, ok := parentalRouteClasses[pattern]
	if !ok {
		panic(fmt.Sprintf("mediauiprox: route %q has no ADR-0031 parental route class (parental_routes.go)", pattern))
	}
	var gated http.Handler
	switch route.class {
	case classPlay:
		gated = s.parentalPlayGate(route, h)
	case classDeny:
		gated = s.parentalDenyGate(h)
	case classList, classItem:
		gated = s.parentalBrowseGate(route, h)
	default:
		gated = h
	}
	// The role gate is outermost: a member is rejected before any parental
	// policy lookup or handler runs.
	return s.operatorRoleGate(route, gated)
}

// parentalCheck resolves the request's principal and policy. active is false
// only when auth is disabled (MEDIA_UI_REQUIRE_AUTH=0) and the request has no
// session; a request with a session is always enforced.
func (s *server) parentalCheck(r *http.Request) (pol parentalPolicy, pr parentalPrincipal, active bool, perr *parentalError) {
	pr, ok := s.sessionParentalPrincipal(r)
	if !ok {
		if s.requireAuth {
			return parentalPolicy{}, pr, true, errParentalSession
		}
		return parentalPolicy{}, pr, false, nil
	}
	if pr.userID == "" || pr.bearer == "" {
		// Quick Connect and legacy sessions carry no auth-local bearer.
		return parentalPolicy{}, pr, true, errParentalUnverif
	}
	pol, perr = s.parental.policy(r.Context(), pr)
	if perr != nil {
		return parentalPolicy{}, pr, true, perr
	}
	if !pol.configured {
		return parentalPolicy{}, pr, true, errParentalUnconf
	}
	return pol, pr, true, nil
}

// sessionParentalPrincipal reads the BFF session. Client identity headers, query
// parameters and tenant headers are never consulted.
func (s *server) sessionParentalPrincipal(r *http.Request) (parentalPrincipal, bool) {
	if s.sessions == nil {
		return parentalPrincipal{}, false
	}
	tok := sessionTokenFromRequest(r)
	userID, _, tenantID, _, ok := s.sessions.LookupRoles(tok)
	if !ok {
		return parentalPrincipal{}, false
	}
	return parentalPrincipal{
		sessionID: sessionID(tok),
		userID:    userID,
		tenantID:  tenantID,
		bearer:    s.sessions.LookupAuthToken(tok),
	}, true
}

// parentalDenyGate: restricted principals get 403 parental.restricted_route;
// unrestricted principals reach the handler unchanged.
func (s *server) parentalDenyGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pol, _, active, perr := s.parentalCheck(r)
		switch {
		case perr != nil:
			writeParentalError(w, perr, "")
		case !active || pol.unrestricted():
			next.ServeHTTP(w, r)
		default:
			writeParentalError(w, errParentalRoute, "")
		}
	})
}

// parentalPlayGate evaluates the item behind the request for restricted
// principals and passes the authorization to the handler as a grant.
func (s *server) parentalPlayGate(route parentalRoute, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pol, pr, active, perr := s.parentalCheck(r)
		if perr != nil {
			writeParentalError(w, perr, route.blockedAlias)
			return
		}
		if !active || pol.unrestricted() {
			next.ServeHTTP(w, r)
			return
		}
		var item parentalItem
		if route.hlsAsset {
			bound, ok := s.parental.hls.lookup(pr.sessionID, r.PathValue("key"), s.parental.now())
			if !ok {
				writeParentalError(w, errParentalBlocked, route.blockedAlias)
				return
			}
			item = bound
		} else if route.item != nil {
			item = route.item(r)
		}
		if perr := s.parental.authorizeItem(r.Context(), pol, item); perr != nil {
			writeParentalError(w, perr, route.blockedAlias)
			return
		}
		ctx := context.WithValue(r.Context(), parentalGrantKey{}, parentalGrant{sessionID: pr.sessionID, item: item})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type parentalGrantKey struct{}

// parentalGrant records that a restricted principal was authorized for item.
type parentalGrant struct {
	sessionID string
	item      parentalItem
}

func parentalGrantFrom(ctx context.Context) (parentalGrant, bool) {
	g, ok := ctx.Value(parentalGrantKey{}).(parentalGrant)
	return g, ok && g.sessionID != ""
}

func parentalItemFromSrcQuery(r *http.Request) parentalItem {
	return parentalItemFromStreamPath(strings.TrimSpace(r.URL.Query().Get("src")))
}

func parentalItemFromRequestPath(r *http.Request) parentalItem {
	return parentalItemFromStreamPath(r.URL.EscapedPath())
}

// parentalItemFromStreamPath names the item of /stream/movies/<id> or
// /stream/tv/<episode_id>. Anything else (debrid:, extra segments, encoded
// separators) is unknown and therefore unavailable.
func parentalItemFromStreamPath(p string) parentalItem {
	u := parsePlaybackSource(p)
	if u == nil {
		return parentalItem{}
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 4 {
		return parentalItem{}
	}
	switch parts[2] {
	case "movies":
		return parentalItem{Kind: "movie", ID: parts[3]}
	case "tv":
		return parentalItem{Kind: "episode", ID: parts[3]}
	}
	return parentalItem{}
}
