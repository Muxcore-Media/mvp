package main

import "testing"

func TestParseStreamMediaID(t *testing.T) {
	kind, id := parseStreamMediaID("/stream/movies/abc%201")
	if kind != "movie" || id != "abc 1" {
		t.Fatalf("movie parse: kind=%q id=%q", kind, id)
	}
	kind, id = parseStreamMediaID("/stream/tv/ep_1")
	if kind != "episode" || id != "ep_1" {
		t.Fatalf("episode parse: kind=%q id=%q", kind, id)
	}
}

func TestSRTToVTT(t *testing.T) {
	in := "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	out := srtToVTT(in)
	if !containsAll(out, "WEBVTT", "00:00:01.000 --> 00:00:02.000", "Hello") {
		t.Fatalf("unexpected vtt: %q", out)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
