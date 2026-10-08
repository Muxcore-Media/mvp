package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// T-M5-13 review round 1: withAuth accepts a request on either its session
// cookie or its bearer, but sessionPrincipal reads the cookie first. A request
// with a stale cookie and a valid bearer therefore passes withAuth with no
// resolvable principal. handlePlaybackSession used to substitute "anonymous"
// and forward native::anonymous:<id>, a namespace every such caller shared.
// These tests drive the full stack and require that no such request reaches
// playback-monitor or mutates a row.

// creds decorates a request with credentials.
type creds func(*http.Request)

func cookie(tok string) creds {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session", Value: tok}) }
}

func bearer(tok string) creds {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

func sessionHeader(tok string) creds {
	return func(r *http.Request) { r.Header.Set("X-MuxCore-Session", tok) }
}

func both(cs ...creds) creds {
	return func(r *http.Request) {
		for _, c := range cs {
			c(r)
		}
	}
}

// postWith posts a playback event through handler h with the given credentials.
func (h *playbackOwnerHarness) postWith(handler http.Handler, c creds, body string) playbackPostResult {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c != nil {
		c(req)
	}
	res := serve(handler, req)
	if res.panic != "" {
		h.t.Fatalf("handler panicked: %s", res.panic)
	}
	out := playbackPostResult{status: res.status, raw: res.body}
	_ = json.Unmarshal([]byte(res.body), &out)
	return out
}

func (h *playbackOwnerHarness) requireNoAnonymous(label string) {
	h.t.Helper()
	rows, events := h.mon.snapshot()
	for _, ev := range events {
		if strings.Contains(ev.ExternalSessionID, "anonymous") || ev.UserID == "" || ev.UserID == "anonymous" {
			h.t.Fatalf("%s: anonymous event reached playback-monitor: %+v", label, ev)
		}
	}
	for _, row := range rows {
		if strings.Contains(row.ExternalID, "anonymous") || row.UserID == "anonymous" {
			h.t.Fatalf("%s: anonymous row in playback-monitor: %+v", label, row)
		}
	}
}

// TestPlaybackSessionStaleCookieValidBearerRefused is the review reproduction:
// Alice and Bob each hold a valid bearer, both send session=<stale> and
// session_id=s1, and both used to get 200 for monitor key native::anonymous:s1.
// Both are now refused with 401 and nothing is forwarded or mutated.
func TestPlaybackSessionStaleCookieValidBearerRefused(t *testing.T) {
	stale := cookie("stale-session-cookie")
	variants := map[string]func(tok string) creds{
		"authorization-bearer": func(tok string) creds { return both(stale, bearer(tok)) },
		"x-muxcore-session":    func(tok string) creds { return both(stale, sessionHeader(tok)) },
	}
	for name, mk := range variants {
		for _, event := range []string{"started", "progress", "stopped", "paused", "playback.started", "playback.progress", "playback.stopped"} {
			t.Run(name+"/"+event, func(t *testing.T) {
				h := newPlaybackOwnerHarness(t)
				alice := h.session("alice", "", "bearer-alice")
				bob := h.session("bob", "", "bearer-bob")

				// Alice has a live session under the same client id.
				start := h.post(alice, playbackBody("started", "s1", "m-alice", "Alice Movie", 10))
				if start.status != http.StatusOK || !start.Forwarded {
					t.Fatalf("alice start: %d %s", start.status, start.raw)
				}
				before := h.mon.row(start.SessionID)
				eventsBefore := h.mon.eventCount()

				for who, tok := range map[string]string{"alice": alice, "bob": bob} {
					res := h.postWith(h.gated, mk(tok), playbackBody(event, "s1", "m-"+who, who+" Injected", 6666))
					if res.status != http.StatusUnauthorized || res.Code != "api.unauthorized" || res.Forwarded || res.Accepted {
						t.Fatalf("%s mixed-credential %s: %d %s", who, event, res.status, res.raw)
					}
				}
				if n := h.mon.eventCount(); n != eventsBefore {
					t.Fatalf("mixed-credential requests forwarded %d event(s)", n-eventsBefore)
				}
				if after := h.mon.row(start.SessionID); after != before {
					t.Fatalf("alice's row changed:\n before %+v\n after  %+v", before, after)
				}
				h.requireNoAnonymous("mixed credentials")
			})
		}
	}
}

// TestPlaybackSessionHandlerFailsClosedWithoutPrincipal exercises the handler
// guard itself, without withAuth in front: with auth required, no resolvable
// principal (no credentials, stale cookie, stale cookie + valid bearer, or a
// session whose user id is blank) is a 401 and never reaches the monitor.
func TestPlaybackSessionHandlerFailsClosedWithoutPrincipal(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	alice := h.session("alice", "", "bearer-alice")
	blank, err := h.s.sessions.CreateWithAuth("  ", "ghost", "", []string{"user"}, "bearer-ghost")
	if err != nil {
		t.Fatal(err)
	}
	direct := http.HandlerFunc(h.s.handlePlaybackSession)
	cases := map[string]creds{
		"no credentials":              nil,
		"stale cookie":                cookie("stale"),
		"stale cookie + valid bearer": both(cookie("stale"), bearer(alice)),
		"stale cookie + valid header": both(cookie("stale"), sessionHeader(alice)),
		"blank user id (cookie)":      cookie(blank),
		"blank user id (bearer)":      bearer(blank),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.postWith(direct, c, playbackBody("started", "s1", "m1", "x", 1))
			if res.status != http.StatusUnauthorized || res.Code != "api.unauthorized" {
				t.Fatalf("%d %s", res.status, res.raw)
			}
		})
	}
	if n := h.mon.eventCount(); n != 0 {
		t.Fatalf("monitor received %d events", n)
	}
	// The principal is checked before the body: a malformed body gives an
	// unauthenticated caller 401, not a validation oracle.
	if res := h.postWith(direct, nil, "{"); res.status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated malformed body: %d %s", res.status, res.raw)
	}
}

// TestPlaybackSessionUnauthenticatedHouseholdRefused: with auth required, a
// request with no credentials never gets past withAuth.
func TestPlaybackSessionUnauthenticatedHouseholdRefused(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	for name, c := range map[string]creds{"none": nil, "stale cookie": cookie("stale"), "stale bearer": bearer("stale")} {
		res := h.postWith(h.gated, c, playbackBody("started", "s1", "m1", "x", 1))
		if res.status != http.StatusUnauthorized || res.Forwarded {
			t.Fatalf("%s: %d %s", name, res.status, res.raw)
		}
	}
	if n := h.mon.eventCount(); n != 0 {
		t.Fatalf("monitor received %d events", n)
	}
}

// TestPlaybackSessionValidCredentialShapesStillWork: each single valid
// credential shape reaches the same namespaced monitor session; a valid cookie
// together with another member's bearer is attributed to the cookie holder
// (withAuth and sessionPrincipal agree on the cookie), never to the bearer's
// owner and never to anonymous. Kick and BFF restart behave as before.
func TestPlaybackSessionValidCredentialShapesStillWork(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	alice := h.session("alice", "", "bearer-alice")
	bob := h.session("bob", "", "bearer-bob")

	start := h.postWith(h.gated, cookie(alice), playbackBody("started", "s1", "m1", "Dune", 10))
	if start.status != http.StatusOK || !start.Forwarded || start.SessionID == "" {
		t.Fatalf("cookie: %d %s", start.status, start.raw)
	}
	for name, c := range map[string]creds{
		"bearer":           bearer(alice),
		"x-muxcore":        sessionHeader(alice),
		"cookie+bearer":    both(cookie(alice), bearer(alice)),
		"alice cookie+bob": both(cookie(alice), bearer(bob)),
	} {
		res := h.postWith(h.gated, c, playbackBody("progress", "s1", "m1", "Dune", 20))
		if res.status != http.StatusOK || !res.Forwarded || res.SessionID != start.SessionID {
			t.Fatalf("%s: %d %s (want session %s)", name, res.status, res.raw, start.SessionID)
		}
	}
	if row := h.mon.row(start.SessionID); row.UserID != "alice" || row.Pos != 20 {
		t.Fatalf("row %+v", row)
	}
	// Bob's own bearer reaches Bob's namespace only.
	if res := h.postWith(h.gated, bearer(bob), playbackBody("progress", "s1", "m1", "Dune", 30)); res.status != http.StatusOK || res.SessionID == start.SessionID {
		t.Fatalf("bob bearer: %d %s", res.status, res.raw)
	}

	// Kick reaches the owner through any of her valid credentials.
	h.mon.kick(start.SessionID)
	if res := h.postWith(h.gated, bearer(alice), playbackBody("progress", "s1", "m1", "Dune", 40)); !res.Stopped || res.SessionID != start.SessionID {
		t.Fatalf("kick via bearer: %d %s", res.status, res.raw)
	}

	// A restarted BFF with a fresh login for Alice continues the same session.
	restarted := newPlaybackOwnerHarness(t)
	restarted.s.playbackMonitorHTTP = h.s.playbackMonitorHTTP
	aliceAgain := restarted.session("alice", "", "bearer-alice-2")
	if res := restarted.postWith(restarted.gated, bearer(aliceAgain), playbackBody("progress", "s1", "m1", "Dune", 50)); res.SessionID != start.SessionID {
		t.Fatalf("after restart: %d %s", res.status, res.raw)
	}
	h.requireNoAnonymous("valid credential shapes")
}

// TestPlaybackSessionExplicitDevModeKeepsAnonymous pins the only place the
// anonymous identity survives: MEDIA_UI_REQUIRE_AUTH=0 (no withAuth), request
// without a BFF session. A request that does carry a valid session is still
// attributed to its user.
func TestPlaybackSessionExplicitDevModeKeepsAnonymous(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	h.s.requireAuth = false
	dev := http.NewServeMux()
	h.s.registerRoutes(dev)
	alice := h.session("alice", "", "bearer-alice")

	res := h.postWith(dev, nil, playbackBody("started", "s1", "m1", "Dune", 10))
	if res.status != http.StatusOK || !res.Forwarded {
		t.Fatalf("dev anonymous: %d %s", res.status, res.raw)
	}
	if row := h.mon.row(res.SessionID); row.UserID != "anonymous" || row.ExternalID != "native::anonymous:s1" {
		t.Fatalf("dev anonymous row %+v", row)
	}

	res = h.postWith(dev, bearer(alice), playbackBody("started", "s1", "m1", "Dune", 10))
	if res.status != http.StatusOK || !res.Forwarded {
		t.Fatalf("dev signed-in: %d %s", res.status, res.raw)
	}
	if row := h.mon.row(res.SessionID); row.UserID != "alice" || row.ExternalID != "native::alice:s1" {
		t.Fatalf("dev signed-in row %+v", row)
	}
}
