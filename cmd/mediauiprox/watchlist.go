package main

import (
	"net/http"
	"strconv"
	"strings"

	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
)

type watchlistItem struct {
	ID         int32   `json:"id"`
	Title      string  `json:"title"`
	Year       int32   `json:"year"`
	Overview   string  `json:"overview"`
	Poster     string  `json:"poster"`
	VoteAvg    float64 `json:"voteAvg"`
	MediaType  string  `json:"mediaType"`
	Status     string  `json:"status,omitempty"`
	SourceID   string  `json:"sourceId,omitempty"`
	ExternalID string  `json:"externalId,omitempty"`
}

func (s *server) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.listSync == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "watchlist module unavailable", "code": "watchlist.unavailable", "items": []watchlistItem{}})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	mediaType := strings.TrimSpace(r.URL.Query().Get("type"))

	resp, err := s.listSync.GetItems(r.Context(), &listsyncv1.GetItemsRequest{
		Page: int32(page), PageSize: int32(pageSize), MediaType: mediaType,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watchlist.gateway_error", "items": []watchlistItem{}})
		return
	}

	items := make([]watchlistItem, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil || it.GetAction() != "watchlist" {
			continue
		}
		if it.GetTmdbId() <= 0 {
			continue
		}
		kind := strings.TrimSpace(it.GetMediaType())
		if kind != "movie" && kind != "tv" {
			continue
		}
		items = append(items, watchlistItem{
			ID: it.GetTmdbId(), Title: strings.TrimSpace(it.GetTitle()), Year: it.GetYear(),
			MediaType: kind, Status: it.GetStatus(), SourceID: it.GetSourceId(), ExternalID: it.GetExternalId(),
		})
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"items": items, "total": len(items), "page": page, "page_size": pageSize,
	})
}
