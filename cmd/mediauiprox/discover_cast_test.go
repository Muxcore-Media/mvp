package main

import (
	"testing"

	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
)

func TestDiscoverCast(t *testing.T) {
	cast := discoverCast([]*metadatav1.Credits{{
		Cast: []*metadatav1.CastMember{
			{Id: 1, Name: "Alice", Character: "Hero", ProfilePath: "/a.jpg"},
			{Id: 2, Name: "Bob", Character: "Villain"},
			nil,
		},
	}})
	if len(cast) != 2 {
		t.Fatalf("len=%d", len(cast))
	}
	if cast[0].Name != "Alice" || cast[0].Character != "Hero" || cast[0].ProfilePath != "/a.jpg" {
		t.Fatalf("%+v", cast[0])
	}
	if cast[1].Name != "Bob" {
		t.Fatalf("%+v", cast[1])
	}
}

func TestDiscoverCastEmpty(t *testing.T) {
	if discoverCast(nil) != nil {
		t.Fatal("expected nil")
	}
}
