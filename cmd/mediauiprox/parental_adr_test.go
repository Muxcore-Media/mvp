package main

import (
	"net/http"
	"testing"
)

// Independent expectations for the route-class table. These lists are written
// out from ADR-0031 §1.2 (and the S5a additions recorded in the PR), not
// derived from parentalRouteClasses, so editing the table and BFF-API.md
// together cannot silently weaken enforcement: a pattern listed here must keep
// its class, and the enforced classes are proved by request below.

// adrRouteClasses: ADR-0031 §1.2 C-LIST/C-ITEM/C-PLAY/C-DENY, as registered.
var adrRouteClasses = map[string]routeClass{
	// C-LIST
	"/api/movies":               classList,
	"/api/tv":                   classList,
	"GET /api/collections":      classList,
	"GET /api/collections/{id}": classList,
	"GET /api/collections/":     classList,
	// C-ITEM
	"/api/movies/":                   classItem,
	"/api/tv/":                       classItem,
	"GET /api/movies/{id}/tags":      classItem,
	"GET /api/tv/{id}/tags":          classItem,
	"GET /api/movies/{id}/titles":    classItem,
	"GET /api/tv/{id}/titles":        classItem,
	"GET /api/movies/{id}/history":   classItem,
	"GET /api/tv/{id}/history":       classItem,
	"GET /api/movies/{id}/artwork":   classItem,
	"GET /api/tv/{id}/artwork":       classItem,
	"GET /api/movies/{id}/files":     classItem,
	"GET /api/movies/{id}/subtitles": classItem,
	"GET /api/tv/{id}/subtitles":     classItem,
	"GET /api/episodes/{id}/file":    classItem,
	// C-PLAY
	"GET /api/playback/resolve":        classPlay,
	"GET /stream/transcode":            classPlay,
	"GET /stream/hls":                  classPlay,
	"GET /stream/hls/{key}/{file}":     classPlay,
	"GET /stream/trickplay":            classPlay,
	"/stream/movies/":                  classPlay,
	"/stream/tv/":                      classPlay,
	"GET /api/playback/subtitles":      classPlay,
	"GET /api/playback/subtitles/{id}": classPlay,
	"GET /api/playback/segments":       classPlay,
	"GET /api/playback/chapters":       classPlay,
	"GET /api/playback/analysis":       classPlay,
	// C-DENY: external catalogue and players
	"/api/discover/":         classDeny,
	"/api/search":            classDeny,
	"/api/request":           classDeny,
	"/api/requests/":         classDeny,
	"/api/requests":          classDeny,
	"/api/request-policy":    classDeny,
	"GET /api/graph/related": classDeny,
	"/api/watchlist":         classDeny,
	"/api/jellyfin/play":     classDeny,
	"/api/plex/play":         classDeny,
	"GET /api/debrid/vfs":    classDeny,
	"GET /api/debrid/stream": classDeny,
	"GET /api/livetv":        classDeny,
	// C-DENY: non-video libraries
	"GET /api/music":                    classDeny,
	"GET /api/books":                    classDeny,
	"GET /api/comics":                   classDeny,
	"GET /api/audiobooks":               classDeny,
	"GET /api/music/":                   classDeny,
	"GET /api/books/":                   classDeny,
	"GET /api/comics/":                  classDeny,
	"GET /api/audiobooks/":              classDeny,
	"GET /stream/music/":                classDeny,
	"GET /stream/books/":                classDeny,
	"GET /stream/comics/":               classDeny,
	"GET /stream/audiobooks/":           classDeny,
	"GET /api/music/tracks/{id}/lyrics": classDeny,
	// C-DENY: title-revealing feeds
	"GET /api/calendar":                classDeny,
	"GET /api/activity":                classDeny,
	"GET /api/wanted":                  classDeny,
	"GET /api/missing":                 classDeny,
	"GET /api/history":                 classDeny,
	"GET /api/sessions/events":         classDeny,
	"GET /api/sessions":                classDeny,
	"GET /api/watch-stats/item":        classDeny,
	"/api/watch-together":              classDeny,
	"/api/watch-together/":             classDeny,
	"/api/media-issues":                classDeny,
	"GET /api/playback/segments/media": classDeny,
}

// s5aAddedRouteClasses: routes ADR-0031 §1.2 omitted that S5a classified
// (PR #99 "Deviations"). They may only move to a stricter class.
var s5aAddedRouteClasses = map[string]routeClass{
	"PATCH /api/movies/{id}":           classItem,
	"PATCH /api/tv/{id}":               classItem,
	"GET /api/subtitles/search":        classDeny,
	"POST /api/subtitles/download":     classDeny,
	"POST /api/wanted":                 classDeny,
	"POST /api/releases/grab":          classDeny,
	"GET /api/jellyfin/link":           classDeny,
	"GET /api/plex/sync-lists":         classDeny,
	"POST /api/debrid/add":             classDeny,
	"POST /api/livetv/timers":          classDeny,
	"GET /api/music/{id}/history":      classDeny,
	"GET /api/music/{id}/artwork":      classDeny,
	"GET /api/music/{id}/tags":         classDeny,
	"GET /api/music/{id}/files":        classDeny,
	"GET /api/books/{id}/history":      classDeny,
	"GET /api/books/{id}/artwork":      classDeny,
	"GET /api/books/{id}/tags":         classDeny,
	"GET /api/comics/{id}/artwork":     classDeny,
	"GET /api/comics/{id}/history":     classDeny,
	"GET /api/audiobooks/{id}/artwork": classDeny,
	"GET /api/audiobooks/{id}/history": classDeny,
	"GET /api/rename/preview":          classDeny,
	"POST /api/rename":                 classDeny,
	"GET /api/releases/search":         classDeny,
	"GET /api/releases/upgrades":       classDeny,
	"GET /api/blocklist":               classDeny,
	"GET /api/import/candidates":       classDeny,
	"GET /api/watch-stats":             classDeny,
	"GET /api/watch-stats/stale":       classDeny,
	"GET /api/watch-stats/duplicates":  classDeny,
}

func expectedRouteClasses() map[string]routeClass {
	out := map[string]routeClass{}
	for p, c := range adrRouteClasses {
		out[p] = c
	}
	for p, c := range s5aAddedRouteClasses {
		out[p] = c
	}
	return out
}

func TestParentalRouteClassesMatchADR(t *testing.T) {
	if len(adrRouteClasses) != 69 || len(s5aAddedRouteClasses) != 30 {
		t.Fatalf("expectation lists edited: adr=%d s5a=%d", len(adrRouteClasses), len(s5aAddedRouteClasses))
	}
	for p := range s5aAddedRouteClasses {
		if _, dup := adrRouteClasses[p]; dup {
			t.Fatalf("%q listed twice", p)
		}
	}
	for p, want := range expectedRouteClasses() {
		route, ok := parentalRouteClasses[p]
		if !ok {
			t.Errorf("%s: missing from parentalRouteClasses (want %s)", p, want)
			continue
		}
		if route.class != want {
			t.Errorf("%s: class %s, ADR-0031 expects %s", p, route.class, want)
		}
	}
}

// Every C-PLAY and C-DENY pattern named by the independent lists is enforced
// for a restricted principal, whatever the table says.
func TestParentalADRRoutesEnforcedByRequest(t *testing.T) {
	h := newParentalHarness(t)
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 3, restrictedPolicyJSON) })
	kid := h.session("kid", "", "bearer-kid")
	n := 0
	for p, want := range expectedRouteClasses() {
		switch want {
		case classPlay:
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, parentalCodeBlocked)
		case classDeny:
			assertParentalError(t, p, serve(h.gated, routeRequest(p, h.tokFor(p, kid))), http.StatusForbidden, parentalCodeRestrictedRoute)
		default:
			continue
		}
		n++
	}
	if n != 12+66 {
		t.Fatalf("enforced %d independent C-PLAY/C-DENY patterns, want 78", n)
	}
	if h.upHits.Load() != 0 {
		t.Fatalf("restricted requests reached modules %d times", h.upHits.Load())
	}
}
