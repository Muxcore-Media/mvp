package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// T-M5-13: POST /api/playback/session must bind the playback-monitor session
// key to the verified BFF principal, so one member cannot address, rewrite,
// progress or stop another member's native session by choosing its
// session_id.

// fakeMonitorRow is one playback-monitor sessions row.
type fakeMonitorRow struct {
	ID, ServerID, ServerType, ExternalID, UserID, UserName, ItemID, Title, State string
	Pos                                                                          int64
	Kicked                                                                       bool
}

// fakePlaybackMonitor models playback-monitor's POST /ingest
// (internal/ingest.go at bf14e0c): rows are keyed by (server_id,
// external_session_id); "started" on an active row overwrites user_id,
// user_name, item and title; "progress" on an active row updates position
// and title, on a kicked row answers {stopped:true}, otherwise starts a row;
// "stopped" closes the active row or inserts a stopped one.
type fakePlaybackMonitor struct {
	mu     sync.Mutex
	rows   []*fakeMonitorRow
	events []monitorSessionEvent
	paths  []string
	nextID int
	srv    *httptest.Server
}

func newFakePlaybackMonitor(t *testing.T) *fakePlaybackMonitor {
	t.Helper()
	m := &fakePlaybackMonitor{}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *fakePlaybackMonitor) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paths = append(m.paths, r.Method+" "+r.URL.EscapedPath())
	if r.Method != http.MethodPost || r.URL.Path != "/ingest" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var ev monitorSessionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	m.events = append(m.events, ev)
	ext := strings.TrimSpace(ev.ExternalSessionID)
	if ext == "" {
		ext = ev.UserID + ":" + ev.ItemID
	}
	active, latest := m.find(ev.ServerID, ext)
	state := "playing"
	if ev.IsPaused {
		state = "paused"
	}
	switch ev.EventType {
	case "playback.started":
		if active != nil {
			active.UserID, active.UserName, active.ItemID, active.Title = ev.UserID, ev.UserName, ev.ItemID, ev.Title
			active.Pos, active.State = ev.PositionSeconds, state
			writeJSON(w, map[string]any{"session_id": active.ID, "created": false})
			return
		}
		writeJSON(w, map[string]any{"session_id": m.insert(ev, ext, state).ID, "created": true})
	case "playback.progress":
		if active != nil {
			active.Pos, active.State = ev.PositionSeconds, state
			if ev.Title != "" {
				active.Title = ev.Title
			}
			writeJSON(w, map[string]any{"session_id": active.ID, "created": false})
			return
		}
		if latest != nil && latest.Kicked {
			writeJSON(w, map[string]any{"session_id": latest.ID, "created": false, "stopped": true})
			return
		}
		writeJSON(w, map[string]any{"session_id": m.insert(ev, ext, state).ID, "created": true})
	case "playback.stopped":
		if active != nil {
			active.State = "stopped"
			if ev.PositionSeconds > 0 {
				active.Pos = ev.PositionSeconds
			}
			writeJSON(w, map[string]any{"session_id": active.ID, "created": false})
			return
		}
		writeJSON(w, map[string]any{"session_id": m.insert(ev, ext, "stopped").ID, "created": true})
	default:
		http.Error(w, "unknown event_type", http.StatusBadRequest)
	}
}

func (m *fakePlaybackMonitor) find(serverID, ext string) (active, latest *fakeMonitorRow) {
	for i := len(m.rows) - 1; i >= 0; i-- {
		row := m.rows[i]
		if row.ServerID != serverID || row.ExternalID != ext {
			continue
		}
		if latest == nil {
			latest = row
		}
		if active == nil && (row.State == "playing" || row.State == "paused") {
			active = row
		}
	}
	return active, latest
}

func (m *fakePlaybackMonitor) insert(ev monitorSessionEvent, ext, state string) *fakeMonitorRow {
	m.nextID++
	row := &fakeMonitorRow{
		ID: fmt.Sprintf("mon-%d", m.nextID), ServerID: ev.ServerID, ServerType: ev.ServerType, ExternalID: ext,
		UserID: ev.UserID, UserName: ev.UserName, ItemID: ev.ItemID, Title: ev.Title, State: state, Pos: ev.PositionSeconds,
	}
	m.rows = append(m.rows, row)
	return row
}

// seed adds a row as another source (Jellyfin/Plex bridge) would.
func (m *fakePlaybackMonitor) seed(row fakeMonitorRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	row.ID = fmt.Sprintf("mon-%d", m.nextID)
	m.rows = append(m.rows, &row)
}

// kick marks a row kicked, as POST /sessions/{id}/stop does.
func (m *fakePlaybackMonitor) kick(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.rows {
		if row.ID == id {
			row.State, row.Kicked = "stopped", true
		}
	}
}

func (m *fakePlaybackMonitor) row(id string) fakeMonitorRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.rows {
		if row.ID == id {
			return *row
		}
	}
	return fakeMonitorRow{}
}

func (m *fakePlaybackMonitor) snapshot() ([]fakeMonitorRow, []monitorSessionEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]fakeMonitorRow, 0, len(m.rows))
	for _, row := range m.rows {
		rows = append(rows, *row)
	}
	return rows, append([]monitorSessionEvent(nil), m.events...)
}

func (m *fakePlaybackMonitor) eventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

// playbackOwnerHarness routes through the full BFF stack (withAuth, route
// table, parental and operator gates) with the fake monitor behind it.
type playbackOwnerHarness struct {
	*parentalHarness
	mon *fakePlaybackMonitor
}

func newPlaybackOwnerHarness(t *testing.T) *playbackOwnerHarness {
	t.Helper()
	h := &playbackOwnerHarness{parentalHarness: newParentalHarness(t), mon: newFakePlaybackMonitor(t)}
	h.provider.doc(func(u string) string { return configuredDoc(u, "", 1, unrestrictedPolicyJSON) })
	h.s.playbackMonitorHTTP = mustURL(h.mon.srv.URL)
	h.s.playbackMonitorToken = "house-token"
	return h
}

type playbackPostResult struct {
	status    int
	raw       string
	Accepted  bool   `json:"accepted"`
	Forwarded bool   `json:"forwarded"`
	Stopped   bool   `json:"stopped"`
	SessionID string `json:"session_id"`
	Code      string `json:"code"`
}

func (h *playbackOwnerHarness) post(tok, body string) playbackPostResult {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	}
	res := serve(h.gated, req)
	if res.panic != "" {
		h.t.Fatalf("handler panicked: %s", res.panic)
	}
	out := playbackPostResult{status: res.status, raw: res.body}
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		h.t.Fatalf("response not JSON (%d): %s", res.status, res.body)
	}
	return out
}

func playbackBody(event, sessionID, mediaID, title string, pos int64) string {
	b, _ := json.Marshal(map[string]any{
		"event_type": event, "session_id": sessionID, "media_id": mediaID, "title": title,
		"media_type": "movie", "position_seconds": pos, "duration_seconds": 7200,
	})
	return string(b)
}

// TestPlaybackSessionCrossUserReproduction is the T-M5-13 reproduction: member
// A starts s1, member B posts each event type with session_id s1. Before the
// fix B's started rewrote A's user_id/title/item, B's progress injected
// position/title and B's stopped closed A's session. Now B's events land on a
// session B owns and A's record is untouched.
func TestPlaybackSessionCrossUserReproduction(t *testing.T) {
	for _, event := range []string{"started", "progress", "stopped", "paused", "playback.started", "playback.progress", "playback.stopped"} {
		t.Run(event, func(t *testing.T) {
			h := newPlaybackOwnerHarness(t)
			alice := h.session("alice", "", "bearer-alice")
			bob := h.session("bob", "", "bearer-bob")

			start := h.post(alice, playbackBody("started", "s1", "m-alice", "Alice Movie", 10))
			if start.status != http.StatusOK || !start.Forwarded || start.SessionID == "" {
				t.Fatalf("alice start: %d %s", start.status, start.raw)
			}
			before := h.mon.row(start.SessionID)

			res := h.post(bob, playbackBody(event, "s1", "m-bob", "Bob Injected", 6666))
			rows, events := h.mon.snapshot()
			for _, ev := range events {
				t.Logf("monitor received: %s ext=%q user_id=%q item=%q title=%q pos=%d", ev.EventType, ev.ExternalSessionID, ev.UserID, ev.ItemID, ev.Title, ev.PositionSeconds)
			}
			for _, row := range rows {
				t.Logf("monitor row: id=%s ext=%q user_id=%q item=%q title=%q state=%s pos=%d", row.ID, row.ExternalID, row.UserID, row.ItemID, row.Title, row.State, row.Pos)
			}

			after := h.mon.row(start.SessionID)
			if after != before {
				t.Fatalf("bob's %s changed alice's session:\n before %+v\n after  %+v", event, before, after)
			}
			if res.SessionID == start.SessionID {
				t.Fatalf("bob's %s was applied to alice's monitor session %s", event, start.SessionID)
			}
			bobRow := h.mon.row(res.SessionID)
			if bobRow.UserID != "bob" || bobRow.ExternalID == before.ExternalID {
				t.Fatalf("bob's event did not land on a bob-owned session: %+v", bobRow)
			}
		})
	}
}

// TestPlaybackSessionLegitimateFlow: the same member's started → progress →
// stopped reach one monitor session, exactly as before.
func TestPlaybackSessionLegitimateFlow(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	alice := h.session("alice", "", "bearer-alice")
	start := h.post(alice, playbackBody("started", "0b5d6c1e-2f4a-4e8b-9c1d-7a3f2e1b0c9d", "m1", "Dune", 0))
	prog := h.post(alice, playbackBody("progress", "0b5d6c1e-2f4a-4e8b-9c1d-7a3f2e1b0c9d", "m1", "Dune", 120))
	stop := h.post(alice, playbackBody("stopped", "0b5d6c1e-2f4a-4e8b-9c1d-7a3f2e1b0c9d", "m1", "Dune", 300))
	for _, r := range []playbackPostResult{start, prog, stop} {
		if r.status != http.StatusOK || !r.Accepted || !r.Forwarded || r.Stopped {
			t.Fatalf("legitimate event: %d %s", r.status, r.raw)
		}
	}
	if start.SessionID == "" || prog.SessionID != start.SessionID || stop.SessionID != start.SessionID {
		t.Fatalf("events split across sessions: %q %q %q", start.SessionID, prog.SessionID, stop.SessionID)
	}
	row := h.mon.row(start.SessionID)
	if row.UserID != "alice" || row.UserName != "alice" || row.State != "stopped" || row.Pos != 300 || row.Title != "Dune" {
		t.Fatalf("row %+v", row)
	}
	rows, events := h.mon.snapshot()
	if len(rows) != 1 || len(events) != 3 {
		t.Fatalf("rows=%d events=%d", len(rows), len(events))
	}
	for _, ev := range events {
		if ev.ServerID != "muxcore-native" || ev.ServerType != "native" || ev.SourceModule != "media-ui" || ev.UserID != "alice" {
			t.Fatalf("event %+v", ev)
		}
	}
}

// TestPlaybackSessionKickReachesOwnerOnly: an operator stop (kick) still
// pauses the owner's player, and another member reusing the id neither gets
// the owner's stop signal nor learns that the session was kicked.
func TestPlaybackSessionKickReachesOwnerOnly(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	alice := h.session("alice", "", "bearer-alice")
	bob := h.session("bob", "", "bearer-bob")
	start := h.post(alice, playbackBody("started", "s1", "m1", "Dune", 10))
	h.mon.kick(start.SessionID)

	if got := h.post(bob, playbackBody("progress", "s1", "m1", "Dune", 20)); got.Stopped || got.SessionID == start.SessionID {
		t.Fatalf("bob saw alice's kick: %s", got.raw)
	}
	if got := h.post(alice, playbackBody("progress", "s1", "m1", "Dune", 20)); !got.Stopped || got.SessionID != start.SessionID {
		t.Fatalf("alice did not get her kick: %s", got.raw)
	}
}

// TestPlaybackSessionStatelessAcrossRestart: the binding is derived, not
// stored, so a fresh BFF (restart) maps the owner's continuing events to the
// same monitor session. Nothing is kept per session id.
func TestPlaybackSessionStatelessAcrossRestart(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	alice := h.session("alice", "", "bearer-alice")
	start := h.post(alice, playbackBody("started", "s1", "m1", "Dune", 10))

	restarted := newPlaybackOwnerHarness(t)
	restarted.s.playbackMonitorHTTP = h.s.playbackMonitorHTTP
	aliceAgain := restarted.session("alice", "", "bearer-alice-2")
	prog := restarted.post(aliceAgain, playbackBody("progress", "s1", "m1", "Dune", 40))
	if prog.SessionID != start.SessionID {
		t.Fatalf("after restart progress went to %q, want %q", prog.SessionID, start.SessionID)
	}
	if row := h.mon.row(start.SessionID); row.UserID != "alice" || row.Pos != 40 {
		t.Fatalf("row %+v", row)
	}
}

// TestPlaybackSessionForgedNamespace: a member who sends another member's
// full monitor key, the derived default id, or the other user's id as a
// prefix still only reaches their own namespace.
func TestPlaybackSessionForgedNamespace(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	alice := h.session("alice", "", "bearer-alice")
	bob := h.session("bob", "", "bearer-bob")
	explicit := h.post(alice, playbackBody("started", "s1", "m1", "Dune", 10))
	derived := h.post(alice, playbackBody("started", "", "m2", "Arrival", 10))
	aliceRows := map[string]fakeMonitorRow{explicit.SessionID: h.mon.row(explicit.SessionID), derived.SessionID: h.mon.row(derived.SessionID)}

	forged := []string{
		aliceRows[explicit.SessionID].ExternalID, // alice's exact monitor key
		aliceRows[derived.SessionID].ExternalID,
		"alice:m2", // the pre-fix derived default for alice
		"media:m2",
		"alice:s1",
		":alice:s1",
		"native::alice:s1",
		"native:%3Aalice:s1",
	}
	for _, sid := range forged {
		for _, event := range []string{"started", "progress", "stopped"} {
			res := h.post(bob, playbackBody(event, sid, "m2", "Bob", 999))
			if res.status != http.StatusOK || aliceRows[res.SessionID] != (fakeMonitorRow{}) {
				t.Fatalf("bob %s %q reached alice's session: %s", event, sid, res.raw)
			}
		}
	}
	for id, want := range aliceRows {
		if got := h.mon.row(id); got != want {
			t.Fatalf("alice row changed:\n before %+v\n after  %+v", want, got)
		}
	}
}

// TestPlaybackSessionTenantSeparated: equal user ids in different tenants do
// not share sessions.
func TestPlaybackSessionTenantSeparated(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	a := h.session("u1", "tenant-a", "bearer-a")
	b := h.session("u1", "tenant-b", "bearer-b")
	ra := h.post(a, playbackBody("started", "s1", "m1", "A", 1))
	rb := h.post(b, playbackBody("started", "s1", "m1", "B", 2))
	if ra.SessionID == "" || ra.SessionID == rb.SessionID {
		t.Fatalf("tenants shared a session: %s / %s", ra.raw, rb.raw)
	}
	if row := h.mon.row(ra.SessionID); row.Title != "A" || row.Pos != 1 {
		t.Fatalf("tenant-a row %+v", row)
	}
}

// TestPlaybackSessionCannotReachMediaServerSessions: native events are always
// sent as server muxcore-native/native and namespaced, so a member cannot hit
// a Jellyfin/Plex row (or one that /sessions dedupes against) by choosing its
// id, and client-supplied identity/server fields are ignored.
func TestPlaybackSessionCannotReachMediaServerSessions(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	jf := fakeMonitorRow{ServerID: "jellyfin", ServerType: "jellyfin", ExternalID: "8f2c0a", UserID: "jf-user", Title: "JF", State: "playing", Pos: 5}
	plex := fakeMonitorRow{ServerID: "plex", ServerType: "plex", ExternalID: "plexsess1", UserID: "plex-user", Title: "PX", State: "playing", Pos: 5}
	h.mon.seed(jf)
	h.mon.seed(plex)
	bob := h.session("bob", "", "bearer-bob")
	for _, sid := range []string{"8f2c0a", "plexsess1", "mon-1", "mon-2"} {
		for _, event := range []string{"started", "progress", "stopped"} {
			body := fmt.Sprintf(`{"event_type":%q,"session_id":%q,"media_id":"m1","title":"x",`+
				`"server_id":"jellyfin","ServerID":"jellyfin","server_type":"jellyfin","ServerType":"plex",`+
				`"user_id":"jf-user","UserID":"jf-user","ExternalSessionID":"8f2c0a","SourceModule":"jellyfin"}`, event, sid)
			if res := h.post(bob, body); res.status != http.StatusOK || res.SessionID == "mon-1" || res.SessionID == "mon-2" {
				t.Fatalf("%s %q: %s", event, sid, res.raw)
			}
		}
	}
	rows, events := h.mon.snapshot()
	if rows[0].Title != "JF" || rows[0].UserID != "jf-user" || rows[0].State != "playing" ||
		rows[1].Title != "PX" || rows[1].UserID != "plex-user" || rows[1].State != "playing" {
		t.Fatalf("media-server rows changed: %+v %+v", rows[0], rows[1])
	}
	for _, ev := range events {
		if ev.ServerID != "muxcore-native" || ev.ServerType != "native" || ev.SourceModule != "media-ui" || ev.UserID != "bob" ||
			!strings.HasPrefix(ev.ExternalSessionID, "native:") {
			t.Fatalf("forwarded %+v", ev)
		}
	}
}

// TestPlaybackSessionIDEdgeCases covers empty, oversized, unicode, control,
// invisible-format and path-like ids.
func TestPlaybackSessionIDEdgeCases(t *testing.T) {
	type tc struct {
		name, sid string
		ok        bool
		wantExt   string // monitor key suffix after "native::alice:"
	}
	cases := []tc{
		{"empty derives from media", "", true, "media:m1"},
		{"whitespace derives from media", "   \t ", true, "media:m1"},
		{"uuid", "0b5d6c1e-2f4a-4e8b-9c1d-7a3f2e1b0c9d", true, "0b5d6c1e-2f4a-4e8b-9c1d-7a3f2e1b0c9d"},
		{"web fallback", "web:m1", true, "web:m1"},
		{"trimmed", "  s1  ", true, "s1"},
		{"max length", strings.Repeat("a", maxNativeSessionIDBytes), true, strings.Repeat("a", maxNativeSessionIDBytes)},
		{"multibyte at limit", strings.Repeat("é", maxNativeSessionIDBytes/2), true, strings.Repeat("é", maxNativeSessionIDBytes/2)},
		{"unicode", "séance-🎬-会话", true, "séance-🎬-会话"},
		{"path traversal", "../../sessions/mon-1/stop", true, "../../sessions/mon-1/stop"},
		{"absolute path", "/etc/passwd", true, "/etc/passwd"},
		{"encoded path", "a%2F..%2Fb?x=1#f", true, "a%2F..%2Fb?x=1#f"},
		{"too long", strings.Repeat("a", maxNativeSessionIDBytes+1), false, ""},
		{"multibyte over limit", strings.Repeat("é", maxNativeSessionIDBytes/2+1), false, ""},
		{"huge", strings.Repeat("x", 512<<10), false, ""},
		{"nul", "s1\x00alice", false, ""},
		{"newline", "s1\nx", false, ""},
		{"escape", "s1\x1b[2J", false, ""},
		{"del", "s1\x7f", false, ""},
		{"bidi override", "s1\u202ealice", false, ""},
		{"zero width", "s\u200b1", false, ""},
		{"line separator", "s\u20281", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newPlaybackOwnerHarness(t)
			alice := h.session("alice", "", "bearer-alice")
			res := h.post(alice, playbackBody("started", c.sid, "m1", "Dune", 1))
			_, events := h.mon.snapshot()
			if !c.ok {
				if res.status != http.StatusBadRequest || res.Code != "playback.session_id_invalid" || len(events) != 0 {
					t.Fatalf("want 400 playback.session_id_invalid and no forward, got %d %s (forwarded %d)", res.status, res.raw, len(events))
				}
				return
			}
			if res.status != http.StatusOK || len(events) != 1 {
				t.Fatalf("status %d events %d body %s", res.status, len(events), res.raw)
			}
			if want := "native::alice:" + c.wantExt; events[0].ExternalSessionID != want {
				t.Fatalf("monitor key %q want %q", events[0].ExternalSessionID, want)
			}
			h.mon.mu.Lock()
			paths := append([]string(nil), h.mon.paths...)
			h.mon.mu.Unlock()
			if len(paths) != 1 || paths[0] != "POST /ingest" {
				t.Fatalf("session id leaked into the monitor URL: %v", paths)
			}
		})
	}

	// Invalid UTF-8 is replaced with U+FFFD by encoding/json before the
	// handler sees it; it stays an opaque id inside the caller's namespace.
	h := newPlaybackOwnerHarness(t)
	alice := h.session("alice", "", "bearer-alice")
	res := h.post(alice, `{"event_type":"started","session_id":"s1`+"\xff\xfe"+`","media_id":"m1"}`)
	_, events := h.mon.snapshot()
	if res.status != http.StatusOK || len(events) != 1 || events[0].ExternalSessionID != "native::alice:s1��" {
		t.Fatalf("invalid utf-8: %d %s %+v", res.status, res.raw, events)
	}
}

// TestPlaybackSessionInvalidIDRejectedBeforeMonitor: a rejected id never
// reaches the monitor, for any event type and any member.
func TestPlaybackSessionInvalidIDRejectedBeforeMonitor(t *testing.T) {
	h := newPlaybackOwnerHarness(t)
	bob := h.session("bob", "", "bearer-bob")
	for _, event := range []string{"started", "progress", "stopped"} {
		res := h.post(bob, playbackBody(event, "s1\u202e", "m1", "x", 1))
		if res.status != http.StatusBadRequest || res.Code != "playback.session_id_invalid" {
			t.Fatalf("%s: %d %s", event, res.status, res.raw)
		}
	}
	if n := h.mon.eventCount(); n != 0 {
		t.Fatalf("monitor received %d events", n)
	}
}

// TestNativeSessionKeyInjective: the key encodes (tenant, user, id) so no two
// distinct principals or ids share a key, whatever separators they contain.
func TestNativeSessionKeyInjective(t *testing.T) {
	t.Parallel()
	type in struct{ tenant, user, sid string }
	inputs := []in{
		{"", "a:b", "c"}, {"", "a", "b:c"}, {"a", "b", "c"}, {"", "a:b:c", ""}, {"a:b", "", "c"},
		{"", "alice", "s1"}, {"", "alice", ":s1"}, {"", "alice:", "s1"}, {":", "alice", "s1"},
		{"", "a%3Ab", "c"}, {"", "a+b", "c"}, {"", "a b", "c"}, {"", "anonymous", "s1"},
		{"t", "alice", "s1"}, {"", "t:alice", "s1"}, {"", "", "alice:s1"},
	}
	seen := map[string]in{}
	for _, v := range inputs {
		k := nativeSessionKey(v.tenant, v.user, v.sid)
		if !strings.HasPrefix(k, "native:") {
			t.Fatalf("key %q lacks native: prefix", k)
		}
		if prev, dup := seen[k]; dup {
			t.Fatalf("collision %q: %+v and %+v", k, prev, v)
		}
		seen[k] = v
	}
	if got := nativeSessionKey("", "alice", "s1"); got != "native::alice:s1" {
		t.Fatalf("readable form changed: %q", got)
	}
}
