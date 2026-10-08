package main

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Playback subtitle track binding (ADR-0031 §1.2, C-PLAY
// `GET /api/playback/subtitles/{id}`).
//
// A subtitle track ID names no item: a media-subtitles ID is a global row ID,
// and a sidecar ID is nothing but the base64 of a file path. Knowing one
// proves nothing about authorization, so a restricted principal may fetch only
// a track the BFF itself advertised to that same session for an item the gate
// had authorized. The list route (`GET /api/playback/subtitles?src=`) records
// every track it returns, sidecar and module-held alike, against the BFF
// session and the authorized item. The serve route reads the binding, never
// the request, to learn the item, and re-evaluates it on every fetch so a
// policy or classification change stops further fetches within the policy TTL.
//
// A binding that is missing, expired, evicted, lost with a restart or held by
// another session denies the fetch (403 parental.blocked); the client lists the
// tracks again, which re-authorizes the item. A principal that was
// unrestricted when it listed the tracks holds no binding, so it gets the same
// denial if it becomes restricted before it fetches them.

const (
	// subtitleBindingTTL is a sliding window refreshed on every authorized
	// fetch, like the HLS key bindings.
	subtitleBindingTTL = 4 * time.Hour
	// subtitleBindingMax bounds the whole table; when it is full of live
	// bindings the least recently used one is evicted.
	subtitleBindingMax = 16384
	// subtitleBindingPerSession bounds one BFF session so it cannot fill the
	// table and push other users' tracks out; its own least recently used
	// binding goes first.
	subtitleBindingPerSession = 512
	// subtitleBindingItems bounds the items one track ID may be advertised for
	// within a session. Sidecar IDs name a file, and two videos in one
	// directory can both claim the same sidecar.
	subtitleBindingItems = 8
	// subtitleTrackIDMax bounds a track ID (a sidecar ID is base64 of a path).
	subtitleTrackIDMax = 8192
)

type subtitleBinding struct {
	sessionID string
	// items are every item the session was shown this track for. The fetch
	// needs all of them authorized, so an ID claimed by two items is only as
	// visible as the more restricted one.
	items   []parentalItem
	expires time.Time // last use + ttl; also the LRU order
}

type subtitleTrackBindings struct {
	mu         sync.Mutex
	ttl        time.Duration
	max        int
	perSession int
	byID       map[string]subtitleBinding     // subtitleBindingID(sessionID, trackID)
	bySession  map[string]map[string]struct{} // sessionID → binding IDs
}

func newSubtitleTrackBindings() *subtitleTrackBindings {
	return &subtitleTrackBindings{
		ttl: subtitleBindingTTL, max: subtitleBindingMax, perSession: subtitleBindingPerSession,
		byID: map[string]subtitleBinding{}, bySession: map[string]map[string]struct{}{},
	}
}

func subtitleBindingID(sessionID, trackID string) string { return sessionID + "\x00" + trackID }

func validSubtitleTrackID(id string) bool {
	return id != "" && len(id) <= subtitleTrackIDMax && id == strings.TrimSpace(id)
}

// bind records trackID → item for one session. It refuses (false) invalid
// input and a track already claimed by subtitleBindingItems other items; the
// caller must then not advertise the track.
func (b *subtitleTrackBindings) bind(sessionID, trackID string, item parentalItem, now time.Time) bool {
	if b == nil || sessionID == "" || item.ID == "" || item.Kind == "" || !validSubtitleTrackID(trackID) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := subtitleBindingID(sessionID, trackID)
	cur, exists := b.byID[id]
	if exists && !now.Before(cur.expires) {
		b.removeLocked(id)
		cur, exists = subtitleBinding{}, false
	}
	items := cur.items
	known := false
	for _, it := range items {
		if it == item {
			known = true
		}
	}
	if !known {
		if len(items) >= subtitleBindingItems {
			return false
		}
		items = append(append([]parentalItem(nil), items...), item)
	}
	if !exists {
		if own := b.bySession[sessionID]; len(own) >= b.perSession {
			b.evictOldestLocked(own, now)
		}
		if len(b.byID) >= b.max {
			b.evictOldestLocked(nil, now)
		}
	}
	b.byID[id] = subtitleBinding{sessionID: sessionID, items: items, expires: now.Add(b.ttl)}
	if b.bySession[sessionID] == nil {
		b.bySession[sessionID] = map[string]struct{}{}
	}
	b.bySession[sessionID][id] = struct{}{}
	return true
}

// evictOldestLocked drops expired bindings among ids (nil = the whole table)
// and, if none expired, the least recently used one.
func (b *subtitleTrackBindings) evictOldestLocked(ids map[string]struct{}, now time.Time) {
	oldest, found, expired := "", false, false
	visit := func(id string) {
		e := b.byID[id]
		if !now.Before(e.expires) {
			b.removeLocked(id)
			expired = true
			return
		}
		if !found || e.expires.Before(b.byID[oldest].expires) {
			oldest, found = id, true
		}
	}
	if ids != nil {
		for id := range ids {
			visit(id)
		}
	} else {
		for id := range b.byID {
			visit(id)
		}
	}
	if !expired && found {
		b.removeLocked(oldest)
	}
}

func (b *subtitleTrackBindings) removeLocked(id string) {
	e, ok := b.byID[id]
	if !ok {
		return
	}
	delete(b.byID, id)
	if own := b.bySession[e.sessionID]; own != nil {
		delete(own, id)
		if len(own) == 0 {
			delete(b.bySession, e.sessionID)
		}
	}
}

// lookup returns the items bound to trackID for this session and slides the
// expiry.
func (b *subtitleTrackBindings) lookup(sessionID, trackID string, now time.Time) ([]parentalItem, bool) {
	if b == nil || sessionID == "" || !validSubtitleTrackID(trackID) {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := subtitleBindingID(sessionID, trackID)
	e, ok := b.byID[id]
	if !ok {
		return nil, false
	}
	if !now.Before(e.expires) {
		b.removeLocked(id)
		return nil, false
	}
	e.expires = now.Add(b.ttl)
	b.byID[id] = e
	return append([]parentalItem(nil), e.items...), true
}

// subtitleTrackIDFromRequest reads the track ID of
// GET /api/playback/subtitles/{id} exactly as the handler does, so the gate and
// the handler can never name different tracks.
func subtitleTrackIDFromRequest(r *http.Request) string {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		id = strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/playback/subtitles/"), "/")
		if u, err := url.PathUnescape(id); err == nil {
			id = u
		}
	}
	return id
}

// bindSubtitleTracks binds every track of an authorized restricted list
// request to the grant's session and item and returns the tracks that are now
// fetchable. A track that cannot be bound is dropped: the response never
// advertises a track the serve route would refuse. Without a restricted grant
// the tracks are returned untouched.
func (g *parentalGate) bindSubtitleTracks(grant parentalGrant, ok bool, tracks []playbackSubtitleTrack) []playbackSubtitleTrack {
	if g == nil || !ok || len(tracks) == 0 {
		return tracks
	}
	out := make([]playbackSubtitleTrack, 0, len(tracks))
	for _, t := range tracks {
		if g.subtitles.bind(grant.sessionID, t.ID, grant.item, g.now()) {
			out = append(out, t)
		}
	}
	return out
}
