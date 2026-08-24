package main

import (
	"testing"

	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
)

func TestMapDiscoverBrowseResult(t *testing.T) {
	item, ok := mapDiscoverBrowseResult(&metadatav1.SearchResult{
		Id: 550, Title: "Fight Club", ReleaseDate: "1999-10-15",
		Overview: "soap", PosterPath: "/p.jpg", VoteAverage: 8.4,
		MediaType: metadatav1.MediaType_MEDIA_TYPE_MOVIE,
	})
	if !ok || item.Title != "Fight Club" || item.MediaType != "movie" || item.Year != 1999 {
		t.Fatalf("%+v ok=%v", item, ok)
	}
}

func TestDiscoverMediaType(t *testing.T) {
	if _, ok := discoverMediaType("movie"); !ok {
		t.Fatal("movie")
	}
	if _, ok := discoverMediaType("shows"); !ok {
		t.Fatal("shows")
	}
	if _, ok := discoverMediaType("music"); ok {
		t.Fatal("music should fail")
	}
}
