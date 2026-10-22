package main

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ADR-0042: sidecar references are opaque, session-bound and confined to
// MEDIA_UI_MEDIA_ROOTS. Every fixture lives under t.TempDir().

func (f *subtitleFixture) point(movie, video string) {
	f.s.movies.(*subtitleMovies).files[movie] = video
}

// listAs lists src as tok and returns the labels of the sidecar tracks.
func (f *subtitleFixture) sidecarsAs(t *testing.T, src, tok string) []playbackSubtitleTrack {
	t.Helper()
	var out []playbackSubtitleTrack
	for _, tr := range f.tracks(t, src, tok) {
		if strings.HasPrefix(tr.ID, "sc_") {
			out = append(out, tr)
		}
	}
	return out
}

func TestSubtitleServeNeverDecodesIDs(t *testing.T) {
	f := newSubtitleFixture(t)
	f.point("m-g", filepath.Join(f.dir, "pg", "Movie PG.mkv"))
	mine := f.sidecarsAs(t, "/stream/movies/m-g", f.adult)
	if len(mine) != 2 {
		t.Fatalf("unrestricted sidecars = %+v", mine)
	}
	for _, tr := range mine {
		if strings.Contains(tr.Src, "pg") || strings.Contains(tr.ID, f.dir) {
			t.Fatalf("reference leaks a path: %+v", tr)
		}
		if res := f.get(tr.Src, f.adult); res.status != http.StatusOK {
			t.Fatalf("own track: %d %s", res.status, res.body)
		}
	}
	other := f.session("adult", "", "bearer-adult-2") // same user, another session
	stranger := f.session("sibling", "", "bearer-sibling")
	assertNotFound(t, "other session of the same user", f.get(mine[0].Src, other))
	assertBlocked(t, "other (restricted) user", f.get(mine[0].Src, stranger))
	forged := map[string]string{
		"old base64 of a real secret":   "sc_" + b64(f.secret),
		"old base64 of /etc/passwd":     "sc_" + b64("/etc/passwd"),
		"session hash of a secret":      sidecarTrackID(sessionID(f.adult), f.secret),
		"random sc_ id":                 "sc_00112233445566778899aabbccddeeff",
		"bare prefix":                   "sc_",
		"traversal in id":               "sc_../../etc/passwd",
		"own id, altered":               mine[0].ID + "0",
		"own id of a never-listed file": sidecarTrackID(sessionID(f.adult), filepath.Join(f.dir, "r", "Movie R.en.srt")),
	}
	for name, id := range forged {
		assertNotFound(t, name, f.get("/api/playback/subtitles/"+url.PathEscape(id), f.adult))
	}
	// After a restart (empty table) a once valid ID is gone, not decodable.
	f.s.parental.subtitles = newSubtitleTrackBindings()
	assertNotFound(t, "after restart", f.get(mine[0].Src, f.adult))
	// Expiry.
	mine = f.sidecarsAs(t, "/stream/movies/m-g", f.adult)
	f.clock.Advance(subtitleBindingTTL + time.Second)
	f.setPolicy(restrictedPolicyJSON)
	assertNotFound(t, "expired", f.get(mine[0].Src, f.adult))
}

func TestSubtitleDiscoveryRefusesPathsOutsideRoots(t *testing.T) {
	f := newSubtitleFixture(t)
	root := filepath.Join(f.dir, "movies")
	sibling := root + "2" // shares the root's textual prefix
	other := t.TempDir()
	f.s.mediaRoots = []string{root}
	for _, d := range []string{root, sibling, other} {
		writeFixtureFile(t, filepath.Join(d, "Film.mkv"), "video")
		writeFixtureFile(t, filepath.Join(d, "Film.en.srt"), fmt0("hi"))
	}
	if err := os.Symlink(other, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "Film.en.srt"), filepath.Join(root, "Pic.en.srt")); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "Pic.mkv"), "video")

	cases := map[string]string{
		"dot-dot":                 filepath.Join(root, "..", "movies2", "Film.mkv"),
		"dot-dot, raw":            root + "/../movies2/Film.mkv",
		"absolute outside":        filepath.Join(other, "Film.mkv"),
		"prefix sibling":          filepath.Join(sibling, "Film.mkv"),
		"video through a symlink": filepath.Join(root, "link", "Film.mkv"),
		"NUL byte":                filepath.Join(root, "Film.mkv") + "\x00.mkv",
		"relative":                "movies/Film.mkv",
		"empty":                   "",
	}
	for name, video := range cases {
		if got := f.s.discoverSidecarSubtitles("sess", video); len(got) != 0 {
			t.Errorf("%s listed %+v", name, got)
		}
	}
	if got := f.s.discoverSidecarSubtitles("sess", filepath.Join(root, "Film.mkv")); len(got) != 1 {
		t.Fatalf("control inside the root: %+v", got)
	}
	if got := f.s.discoverSidecarSubtitles("sess", filepath.Join(root, "Pic.mkv")); len(got) != 0 {
		t.Errorf("a sidecar symlinked out of the root was listed: %+v", got)
	}
	if got := f.s.discoverSidecarSubtitles("", filepath.Join(root, "Film.mkv")); len(got) != 0 {
		t.Errorf("no session must list nothing: %+v", got)
	}
	// Over HTTP, a module row with a traversing file_path lists no sidecar.
	f.point("m-g", filepath.Join(root, "..", "movies2", "Film.mkv"))
	if got := f.sidecarsAs(t, "/stream/movies/m-g", f.adult); len(got) != 0 {
		t.Errorf("traversing module row listed %+v", got)
	}
}

func TestResolveAbsMediaPathRefusesTraversal(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "a.mkv"), "v")
	if got := resolveAbsMediaPath(root, "media/a.mkv"); got != filepath.Join(root, "a.mkv") {
		t.Fatalf("control: %q", got)
	}
	for _, p := range []string{"../a.mkv", "media/../../etc/passwd", "/x/../etc/passwd", "a.mkv\x00", `..\a.mkv`} {
		if got := resolveAbsMediaPath(root, p); got != "" {
			t.Errorf("%q resolved to %q", p, got)
		}
	}
}

func TestSubtitleServeRevalidatesBoundPath(t *testing.T) {
	f := newSubtitleFixture(t)
	root := filepath.Join(f.dir, "lib")
	f.s.mediaRoots = []string{root}
	f.point("m-g", filepath.Join(root, "Film.mkv"))
	writeFixtureFile(t, filepath.Join(root, "Film.mkv"), "video")
	writeFixtureFile(t, filepath.Join(root, "Film.en.srt"), fmt0("Hello"))
	tracks := f.sidecarsAs(t, "/stream/movies/m-g", f.adult)
	if len(tracks) != 1 {
		t.Fatalf("tracks = %+v", tracks)
	}
	tr := tracks[0]
	if res := f.get(tr.Src, f.adult); res.status != http.StatusOK || !strings.Contains(res.body, "Hello") {
		t.Fatalf("control: %d %s", res.status, res.body)
	}
	srt := filepath.Join(root, "Film.en.srt")

	// Swapped for a symlink to a file outside the root after listing.
	if err := os.Remove(srt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.secret, srt); err != nil {
		t.Fatal(err)
	}
	assertNotFound(t, "swapped for an outside symlink", f.get(tr.Src, f.adult))
	// A symlink that resolves inside the roots is served as its target.
	writeFixtureFile(t, filepath.Join(root, "Other.srt"), fmt0("Hello other"))
	_ = os.Remove(srt)
	if err := os.Symlink(filepath.Join(root, "Other.srt"), srt); err != nil {
		t.Fatal(err)
	}
	if res := f.get(tr.Src, f.adult); res.status != http.StatusOK {
		t.Fatalf("inside symlink: %d", res.status)
	}
	// Swapped for a directory / FIFO.
	_ = os.Remove(srt)
	if err := os.Mkdir(srt, 0o755); err != nil {
		t.Fatal(err)
	}
	assertNotFound(t, "directory", f.get(tr.Src, f.adult))
	_ = os.Remove(srt)
	if err := syscall.Mkfifo(srt, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	assertNotFound(t, "fifo", f.get(tr.Src, f.adult))
	// Root shrinks (configuration change) between list and fetch.
	_ = os.Remove(srt)
	writeFixtureFile(t, srt, fmt0("Hello"))
	if res := f.get(tr.Src, f.adult); res.status != http.StatusOK {
		t.Fatalf("restored: %d", res.status)
	}
	f.s.mediaRoots = []string{filepath.Join(f.dir, "elsewhere")}
	assertNotFound(t, "root no longer configured", f.get(tr.Src, f.adult))
}

// A bound path is only a key's value: even a bound path is re-checked.
func TestSubtitleServeRefusesBoundPathsThatBreakTheRules(t *testing.T) {
	f := newSubtitleFixture(t)
	root := filepath.Join(f.dir, "lib")
	f.s.mediaRoots = []string{root}
	sid := sessionID(f.adult)
	item := parentalItem{Kind: "movie", ID: "m-g"}
	big := filepath.Join(root, "big.srt")
	writeFixtureFile(t, big, strings.Repeat("a", sidecarMaxBytes+1))
	exact := filepath.Join(root, "exact.vtt")
	writeFixtureFile(t, exact, strings.Repeat("a", sidecarMaxBytes))
	writeFixtureFile(t, filepath.Join(root, "notes.txt"), "Hello")
	writeFixtureFile(t, filepath.Join(root, "movie.mkv"), "Hello")
	writeFixtureFile(t, filepath.Join(root, "ok.ass"), "[Script Info]\nHello")
	bound := map[string]string{
		"over the size cap":     big,
		"outside the roots":     f.secret,
		"passwd":                "/etc/passwd",
		"dot-dot":               root + "/../outside/secret.srt",
		"extension not allowed": filepath.Join(root, "notes.txt"),
		"a video":               filepath.Join(root, "movie.mkv"),
		"a directory":           root,
		"nul":                   filepath.Join(root, "ok.ass") + "\x00x",
		"missing":               filepath.Join(root, "nope.srt"),
	}
	n := 0
	for name, p := range bound {
		n++
		id := "sc_" + strings.Repeat("0", 31) + string(rune('a'+n))
		if !f.s.parental.subtitles.bind(sid, id, p, item, f.clock.Now()) {
			t.Fatalf("%s: could not bind", name)
		}
		assertNotFound(t, name, f.get("/api/playback/subtitles/"+id, f.adult))
	}
	for name, p := range map[string]string{"exactly at the cap": exact, "ass": filepath.Join(root, "ok.ass")} {
		n++
		id := "sc_" + strings.Repeat("0", 31) + string(rune('a'+n))
		f.s.parental.subtitles.bind(sid, id, p, item, f.clock.Now())
		if res := f.get("/api/playback/subtitles/"+id, f.adult); res.status != http.StatusOK {
			t.Errorf("%s: %d", name, res.status)
		}
	}
}

func TestSubtitleSidecarsFailClosedWithoutRoots(t *testing.T) {
	f := newSubtitleFixture(t)
	f.point("m-g", filepath.Join(f.dir, "pg", "Movie PG.mkv"))
	tracks := f.sidecarsAs(t, "/stream/movies/m-g", f.adult)
	if len(tracks) == 0 {
		t.Fatal("control: sidecars should list with a root")
	}
	for _, roots := range [][]string{nil, {}, {filepath.Join(f.dir, "unmounted")}} {
		f.s.mediaRoots = roots
		assertNotFound(t, "serve with roots "+strings.Join(roots, ","), f.get(tracks[0].Src, f.adult))
		if got := f.sidecarsAs(t, "/stream/movies/m-g", f.adult); len(got) != 0 {
			t.Errorf("roots %v listed %+v", roots, got)
		}
		// The restricted principal is denied the same way and module tracks still list.
		kidTracks := f.tracks(t, "/stream/movies/m-pg", f.kid)
		if len(kidTracks) != 1 || kidTracks[0].ID != "sub-pg-1" {
			t.Errorf("roots %v: restricted tracks = %+v", roots, kidTracks)
		}
	}
	assertBlocked(t, "restricted, sidecar from before", f.get(tracks[0].Src, f.kid))
}

func TestParseMediaRoots(t *testing.T) {
	got := parseMediaRoots(" /data/movies/ :relative:/data/tv::/a\x00b")
	if strings.Join(got, "|") != "/data/movies|/data/tv" {
		t.Fatalf("roots = %q", got)
	}
	if parseMediaRoots("") != nil {
		t.Fatal("unset must stay empty")
	}
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func fmt0(s string) string { return "1\n00:00:01,000 --> 00:00:02,000\n" + s + "\n" }

func TestDropProtectedRoots(t *testing.T) {
	prot := []string{"/etc", "/data/mesh-id", "/data/media-ui", "/var/lib/bff"}
	for _, c := range []struct {
		name, root string
		keep       bool
	}{
		{"equal", "/data/mesh-id", false},
		{"inside", "/data/media-ui/sessions", false},
		{"inside configured dir", "/var/lib/bff/x", false},
		{"containing", "/data", false},
		{"filesystem root", "/", false},
		{"shared prefix sibling", "/data/mesh-id2", true},
		{"shared prefix etc", "/etcetera", true},
		{"good", "/media/movies", true},
	} {
		got := dropProtectedRoots([]string{c.root}, prot)
		if (len(got) == 1) != c.keep {
			t.Errorf("%s: root %q kept=%v, want %v", c.name, c.root, len(got) == 1, c.keep)
		}
	}
	if got := dropProtectedRoots([]string{"/data", "/media/tv"}, prot); len(got) != 1 || got[0] != "/media/tv" {
		t.Errorf("mixed = %q", got)
	}
}
