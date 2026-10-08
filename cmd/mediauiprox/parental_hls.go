package main

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HLS key binding (ADR-0031 §1.2, C-PLAY `GET /stream/hls/{key}/{file}`).
//
// media-transcoder derives the HLS key deterministically from src and the
// encode parameters, so a key proves nothing about authorization: anyone who
// knows a src can compute it. When the gate authorizes a restricted principal's
// HLS index request, the BFF records the key from the transcoder's playlist
// redirect against that BFF session and the authorized item. Asset requests
// from restricted principals are served only for a key bound to their own
// session, and the bound item is re-evaluated on every request so a policy or
// classification change stops further segments within the policy TTL.

const (
	// hlsBindingTTL is a sliding window refreshed on every authorized asset
	// request; an expired binding needs a fresh, authorized index request.
	hlsBindingTTL = 4 * time.Hour
	// hlsBindingMax bounds the whole table. When it is full of live bindings
	// the least recently used one is evicted (active streams refresh theirs
	// every segment), so new playback is never refused for lack of room.
	hlsBindingMax = 8192
	// hlsBindingPerSession bounds one BFF session. Its own least recently used
	// binding is evicted first, so one session cannot fill the table and push
	// other users' playback out.
	hlsBindingPerSession = 256
)

type hlsBinding struct {
	sessionID string
	item      parentalItem
	expires   time.Time // last use + ttl; also the LRU order
}

type hlsKeyBindings struct {
	mu         sync.Mutex
	ttl        time.Duration
	max        int
	perSession int
	byID       map[string]hlsBinding          // hlsBindingID(sessionID, key)
	bySession  map[string]map[string]struct{} // sessionID → binding IDs
}

func newHLSKeyBindings() *hlsKeyBindings {
	return &hlsKeyBindings{
		ttl: hlsBindingTTL, max: hlsBindingMax, perSession: hlsBindingPerSession,
		byID: map[string]hlsBinding{}, bySession: map[string]map[string]struct{}{},
	}
}

func hlsBindingID(sessionID, key string) string { return sessionID + "\x00" + key }

// bind records key → item for one session, evicting that session's least
// recently used binding at the per-session cap and the table's least recently
// used binding at the global cap. It refuses invalid input.
func (b *hlsKeyBindings) bind(sessionID, key string, item parentalItem, now time.Time) bool {
	if b == nil || sessionID == "" || !validHLSKey(key) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := hlsBindingID(sessionID, key)
	if _, exists := b.byID[id]; !exists {
		if own := b.bySession[sessionID]; len(own) >= b.perSession {
			b.evictOldestLocked(own, now)
		}
		if len(b.byID) >= b.max {
			b.evictOldestLocked(nil, now)
		}
	}
	b.byID[id] = hlsBinding{sessionID: sessionID, item: item, expires: now.Add(b.ttl)}
	if b.bySession[sessionID] == nil {
		b.bySession[sessionID] = map[string]struct{}{}
	}
	b.bySession[sessionID][id] = struct{}{}
	return true
}

// evictOldestLocked drops expired bindings among ids (nil = the whole table)
// and, if none expired, the least recently used one.
func (b *hlsKeyBindings) evictOldestLocked(ids map[string]struct{}, now time.Time) {
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

func (b *hlsKeyBindings) removeLocked(id string) {
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

// lookup returns the item bound to key for this session and slides the expiry.
func (b *hlsKeyBindings) lookup(sessionID, key string, now time.Time) (parentalItem, bool) {
	if b == nil || sessionID == "" {
		return parentalItem{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := hlsBindingID(sessionID, key)
	e, ok := b.byID[id]
	if !ok {
		return parentalItem{}, false
	}
	if !now.Before(e.expires) {
		b.removeLocked(id)
		return parentalItem{}, false
	}
	e.expires = now.Add(b.ttl)
	b.byID[id] = e
	return e.item, true
}

// hlsKeyFromRedirect extracts the key from the transcoder's playlist redirect
// (`Location: /stream/hls/{key}/index.m3u8`). Only a same-origin path that the
// BFF itself serves is accepted.
func hlsKeyFromRedirect(resp *http.Response) (string, bool) {
	if resp == nil || resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", false
	}
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(u.EscapedPath(), "/stream/hls/")
	if !ok {
		return "", false
	}
	key, file, ok := strings.Cut(rest, "/")
	if !ok || file != "index.m3u8" || !validHLSKey(key) {
		return "", false
	}
	return key, true
}

// bindHLSRedirect binds the key of an authorized index response to the grant
// carried by the index request. It is a no-op without a restricted grant.
func (g *parentalGate) bindHLSRedirect(grant parentalGrant, resp *http.Response) {
	if g == nil {
		return
	}
	key, ok := hlsKeyFromRedirect(resp)
	if !ok {
		return
	}
	if !g.hls.bind(grant.sessionID, key, grant.item, g.now()) {
		log.Printf("parental: could not bind HLS key for %s %q; assets will be refused", grant.item.Kind, grant.item.ID)
	}
}
