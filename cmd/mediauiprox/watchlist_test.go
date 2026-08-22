package main

import (
	"testing"

	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
)

func TestWatchlistItemFilter(t *testing.T) {
	items := []*listsyncv1.SyncItem{
		{TmdbId: 550, Title: "Fight Club", Year: 1999, MediaType: "movie", Action: "watchlist"},
		{TmdbId: 0, Title: "No TMDB", MediaType: "movie", Action: "watchlist"},
		{TmdbId: 1396, Title: "Breaking Bad", MediaType: "tv", Action: "collection"},
	}
	var out []watchlistItem
	for _, it := range items {
		if it == nil || it.GetAction() != "watchlist" || it.GetTmdbId() <= 0 {
			continue
		}
		kind := it.GetMediaType()
		if kind != "movie" && kind != "tv" {
			continue
		}
		out = append(out, watchlistItem{ID: it.GetTmdbId(), Title: it.GetTitle(), Year: it.GetYear(), MediaType: kind})
	}
	if len(out) != 1 || out[0].Title != "Fight Club" {
		t.Fatalf("%+v", out)
	}
}
