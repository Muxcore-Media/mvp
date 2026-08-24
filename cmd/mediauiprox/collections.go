package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func (s *server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.movies.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "collections.gateway_error")
		return
	}
	items := make([]map[string]any, 0, len(resp.GetCollections()))
	for _, c := range resp.GetCollections() {
		items = append(items, map[string]any{
			"id":          strconv.Itoa(int(c.GetCollectionId())),
			"name":        c.GetName(),
			"movie_count": c.GetMovieCount(),
		})
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     len(items),
		"available": true,
		"source":    "media-movies",
	})
}

func (s *server) handleCollectionByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/collections/"), "/")
	if idStr == "" || strings.Contains(idStr, "/") {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid collection id", "collections.invalid_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.movies.GetCollectionMovies(ctx, &mgmntv1.GetCollectionMoviesRequest{CollectionId: int32(id)})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "collections.gateway_error")
		return
	}
	movies := make([]map[string]any, 0, len(resp.GetMovies()))
	for _, m := range resp.GetMovies() {
		movies = append(movies, movieJSON(m))
	}
	writeJSON(w, map[string]any{
		"id":     idStr,
		"name":   resp.GetName(),
		"movies": movies,
		"total":  len(movies),
	})
}
