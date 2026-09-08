package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
)

type releaseMatchJSON struct {
	GUID             string `json:"guid"`
	Title            string `json:"title"`
	IndexerName      string `json:"indexer_name"`
	DownloadProtocol string `json:"download_protocol"`
	Size             int64  `json:"size"`
	Seeders          int32  `json:"seeders"`
	Peers            int32  `json:"peers"`
	Score            int32  `json:"score"`
	DownloadURL      string `json:"download_url"`
	InfoURL          string `json:"info_url,omitempty"`
	Category         string         `json:"category,omitempty"`
	Quality          map[string]any `json:"quality,omitempty"`
}

func (s *server) handleReleaseSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"code":      "releases.unavailable",
			"message":   "Release search is unavailable — start media-automation to score and grab from indexers.",
		})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeAPIError(w, http.StatusBadRequest, "q required", "releases.query_required")
		return
	}
	itemType := strings.TrimSpace(r.URL.Query().Get("type"))
	if itemType == "" {
		itemType = "movie"
	}
	if itemType != "movie" && itemType != "tv" {
		writeAPIError(w, http.StatusBadRequest, "type must be movie or tv", "releases.bad_type")
		return
	}
	limit := int32(50)
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = int32(n)
	}
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	tmdbID, _ := strconv.Atoi(r.URL.Query().Get("tmdb_id"))
	season, _ := strconv.Atoi(r.URL.Query().Get("season"))
	episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))

	resp, err := s.automation.SearchItem(r.Context(), &automationv1.SearchItemRequest{
		ItemType: itemType,
		Query:    q,
		Year:     int32(year),
		TmdbId:   int32(tmdbID),
		Season:   int32(season),
		Episode:  int32(episode),
		Limit:    limit,
	})
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
			"code":      "releases.search_failed",
			"message":   "Indexer search failed.",
		})
		return
	}
	items := make([]releaseMatchJSON, 0, len(resp.GetMatches()))
	for _, m := range resp.GetMatches() {
		if m == nil {
			continue
		}
		item := releaseMatchJSON{
			GUID:             m.GetGuid(),
			Title:            m.GetTitle(),
			IndexerName:      m.GetIndexerName(),
			DownloadProtocol: m.GetDownloadProtocol(),
			Size:             m.GetSize(),
			Seeders:          m.GetSeeders(),
			Peers:            m.GetPeers(),
			Score:            m.GetScore(),
			DownloadURL:      m.GetDownloadUrl(),
			InfoURL:          m.GetInfoUrl(),
			Category:         m.GetCategory(),
		}
		if q := s.parseQualityForTitle(r.Context(), m.GetTitle()); q != nil {
			item.Quality = q
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].Seeders > items[j].Seeders
		}
		return items[i].Score > items[j].Score
	})
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     len(items),
		"available": true,
		"query":     q,
		"type":      itemType,
	})
}

type cutoffItemJSON struct {
	QueueID          string `json:"queue_id,omitempty"`
	ItemType         string `json:"item_type"`
	ItemID           string `json:"item_id"`
	Title            string `json:"title"`
	Year             int32  `json:"year,omitempty"`
	CurrentScore     int32  `json:"current_score"`
	CutoffScore      int32  `json:"cutoff_score"`
	QualityProfileID string `json:"quality_profile_id,omitempty"`
}

func (s *server) handleCutoffUnmet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"code":      "releases.unavailable",
			"message":   "Quality upgrades are unavailable — start media-automation to list cutoff-unmet titles.",
		})
		return
	}
	page := int32(1)
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		page = int32(n)
	}
	pageSize := int32(50)
	if n, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && n > 0 && n <= 100 {
		pageSize = int32(n)
	}
	resp, err := s.automation.ListCutoffUnmet(r.Context(), &automationv1.ListCutoffUnmetRequest{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
			"code":      "releases.upgrades_failed",
			"message":   "Could not list titles below the quality cutoff.",
		})
		return
	}
	items := make([]cutoffItemJSON, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		items = append(items, cutoffItemJSON{
			QueueID:          it.GetQueueId(),
			ItemType:         it.GetItemType(),
			ItemID:           it.GetItemId(),
			Title:            it.GetTitle(),
			Year:             it.GetYear(),
			CurrentScore:     it.GetCurrentScore(),
			CutoffScore:      it.GetCutoffScore(),
			QualityProfileID: it.GetQualityProfileId(),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		gi := items[i].CutoffScore - items[i].CurrentScore
		gj := items[j].CutoffScore - items[j].CurrentScore
		if gi == gj {
			return items[i].Title < items[j].Title
		}
		return gi > gj
	})
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
		"available": true,
	})
}

func (s *server) handleSearchNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "automation unavailable", "releases.unavailable")
		return
	}
	var body struct {
		QueueID  string `json:"queue_id"`
		ItemType string `json:"item_type"`
		ItemID   string `json:"item_id"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeAPIError(w, http.StatusBadRequest, "invalid json", "releases.invalid_json")
			return
		}
	}
	resp, err := s.automation.SearchNow(r.Context(), &automationv1.SearchNowRequest{
		QueueId:  strings.TrimSpace(body.QueueID),
		ItemType: strings.TrimSpace(body.ItemType),
		ItemId:   strings.TrimSpace(body.ItemID),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "releases.search_now_failed")
		return
	}
	writeJSON(w, map[string]any{
		"started": resp.GetStarted(),
		"message": resp.GetMessage(),
	})
}

func (s *server) handleReleaseBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "automation unavailable", "releases.unavailable")
		return
	}
	var body struct {
		GUID         string `json:"guid"`
		ItemID       string `json:"item_id"`
		WantedItemID string `json:"wanted_item_id"`
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "releases.invalid_json")
		return
	}
	guid := strings.TrimSpace(body.GUID)
	wanted := strings.TrimSpace(body.WantedItemID)
	if wanted == "" {
		wanted = strings.TrimSpace(body.ItemID)
	}
	if guid == "" || wanted == "" {
		writeAPIError(w, http.StatusBadRequest, "guid and item_id required", "releases.no_block")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "household"
	}
	resp, err := s.automation.BlocklistRelease(r.Context(), &automationv1.BlocklistReleaseRequest{
		WantedItemId: wanted,
		Guid:         guid,
		Reason:       reason,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "releases.block_failed")
		return
	}
	writeJSON(w, map[string]any{
		"success": resp.GetSuccess(),
		"guid":    guid,
	})
}

func (s *server) handleReleaseGrab(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "automation unavailable", "releases.unavailable")
		return
	}
	var body struct {
		GUID             string `json:"guid"`
		Title            string `json:"title"`
		DownloadURL      string `json:"download_url"`
		DownloadProtocol string `json:"download_protocol"`
		Size             int64  `json:"size"`
		Score            int32  `json:"score"`
		IndexerName      string `json:"indexer_name"`
		ItemType         string `json:"item_type"`
		ItemID           string `json:"item_id"`
		TmdbID           int32  `json:"tmdb_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "releases.invalid_json")
		return
	}
	if strings.TrimSpace(body.GUID) == "" && strings.TrimSpace(body.DownloadURL) == "" {
		writeAPIError(w, http.StatusBadRequest, "guid or download_url required", "releases.no_release")
		return
	}
	itemType := strings.TrimSpace(body.ItemType)
	if itemType == "" {
		itemType = "movie"
	}
	resp, err := s.automation.Dispatch(r.Context(), &automationv1.DispatchRequest{
		Guid:             body.GUID,
		Title:            body.Title,
		DownloadUrl:      body.DownloadURL,
		DownloadProtocol: body.DownloadProtocol,
		Size:             body.Size,
		Score:            body.Score,
		IndexerName:      body.IndexerName,
		ItemType:         itemType,
		ItemId:           body.ItemID,
		TmdbId:           body.TmdbID,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "releases.grab_failed")
		return
	}
	writeJSON(w, map[string]any{
		"download_id": resp.GetDownloadId(),
		"status":      resp.GetStatus(),
	})
}
