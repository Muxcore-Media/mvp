package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
)

type activityRecordJSON struct {
	ID               string `json:"id"`
	WantedItemID     string `json:"wanted_item_id,omitempty"`
	GUID             string `json:"guid,omitempty"`
	Title            string `json:"title"`
	Indexer          string `json:"indexer,omitempty"`
	Size             int64  `json:"size,omitempty"`
	Score            int32  `json:"score,omitempty"`
	DownloadProtocol string `json:"download_protocol,omitempty"`
	Status           string `json:"status"`
	StatusLabel      string `json:"status_label,omitempty"`
	StatusDetail     string `json:"status_detail,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	DownloadID       string `json:"download_id,omitempty"`
	Warning          bool   `json:"warning"`
	Stuck            bool   `json:"stuck"`
}

type wantedItemJSON struct {
	ID        string `json:"id"`
	ItemType  string `json:"item_type"`
	ItemID    string `json:"item_id"`
	Title     string `json:"title"`
	Year      int32  `json:"year,omitempty"`
	TMDBID    int32  `json:"tmdb_id,omitempty"`
	Monitored bool   `json:"monitored"`
	Missing   bool   `json:"missing"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Season    int32  `json:"season_number,omitempty"`
	Episode   int32  `json:"episode_number,omitempty"`
}

func activityWarning(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "import_failed", "stalled", "failed":
		return true
	default:
		return false
	}
}

func activityStuck(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "import_failed", "stalled", "failed":
		return true
	default:
		return false
	}
}

func (s *server) handleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"code":      "activity.unavailable",
			"message":   "Download activity is unavailable — start media-automation.",
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
	resp, err := s.automation.GetHistory(r.Context(), &automationv1.GetHistoryRequest{
		Page:         page,
		PageSize:     pageSize,
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
		WantedItemId: strings.TrimSpace(r.URL.Query().Get("wanted_item_id")),
	})
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
			"code":      "activity.history_failed",
			"message":   "Could not load download history.",
		})
		return
	}
	items := make([]activityRecordJSON, 0, len(resp.GetRecords()))
	for _, rec := range resp.GetRecords() {
		if rec == nil {
			continue
		}
		st := rec.GetStatus()
		items = append(items, activityRecordJSON{
			ID:               rec.GetId(),
			WantedItemID:     rec.GetWantedItemId(),
			GUID:             rec.GetGuid(),
			Title:            rec.GetTitle(),
			Indexer:          rec.GetIndexer(),
			Size:             rec.GetSize(),
			Score:            rec.GetScore(),
			DownloadProtocol: rec.GetDownloadProtocol(),
			Status:           st,
			StatusLabel:      rec.GetStatusLabel(),
			StatusDetail:     rec.GetStatusDetail(),
			CreatedAt:        rec.GetCreatedAt(),
			DownloadID:       rec.GetDownloadId(),
			Warning:          activityWarning(st),
			Stuck:            activityStuck(st),
		})
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
		"available": true,
	})
}

func (s *server) handleActivityRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "automation unavailable", "activity.unavailable")
		return
	}
	var body struct {
		HistoryID string `json:"history_id"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeAPIError(w, http.StatusBadRequest, "invalid json", "activity.invalid_json")
			return
		}
	}
	resp, err := s.automation.RetryImport(r.Context(), &automationv1.RetryImportRequest{
		HistoryId: strings.TrimSpace(body.HistoryID),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "activity.retry_failed")
		return
	}
	writeJSON(w, map[string]any{
		"attempted": resp.GetAttempted(),
		"message":   resp.GetMessage(),
	})
}

func (s *server) handleWanted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"code":      "activity.unavailable",
			"message":   "Wanted titles are unavailable — start media-automation.",
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
	req := &automationv1.GetQueueRequest{
		Page:     page,
		PageSize: pageSize,
		Filter:   strings.TrimSpace(r.URL.Query().Get("type")),
	}
	if v := strings.TrimSpace(r.URL.Query().Get("missing")); v == "1" || strings.EqualFold(v, "true") {
		yes := true
		req.Missing = &yes
	}
	if v := strings.TrimSpace(r.URL.Query().Get("monitored")); v == "1" || strings.EqualFold(v, "true") {
		yes := true
		req.Monitored = &yes
	}
	resp, err := s.automation.GetQueue(r.Context(), req)
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
			"code":      "activity.wanted_failed",
			"message":   "Could not load wanted titles.",
		})
		return
	}
	items := make([]wantedItemJSON, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		items = append(items, wantedItemJSON{
			ID:        it.GetId(),
			ItemType:  it.GetItemType(),
			ItemID:    it.GetItemId(),
			Title:     it.GetTitle(),
			Year:      it.GetYear(),
			TMDBID:    it.GetTmdbId(),
			Monitored: it.GetMonitored(),
			Missing:   it.GetMissing(),
			UpdatedAt: it.GetUpdatedAt(),
			Season:    it.GetSeasonNumber(),
			Episode:   it.GetEpisodeNumber(),
		})
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
		"available": true,
	})
}

func (s *server) handleWantedRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "automation unavailable", "activity.unavailable")
		return
	}
	var body struct {
		QueueID string `json:"queue_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "activity.invalid_json")
		return
	}
	id := strings.TrimSpace(body.QueueID)
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, "queue_id required", "activity.no_queue_id")
		return
	}
	if _, err := s.automation.RemoveFromQueue(r.Context(), &automationv1.RemoveFromQueueRequest{QueueId: id}); err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "activity.remove_failed")
		return
	}
	writeJSON(w, map[string]any{"removed": true, "queue_id": id})
}

func (s *server) handleWantedAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.automation == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "automation unavailable", "activity.unavailable")
		return
	}
	var body struct {
		ItemType         string `json:"item_type"`
		ItemID           string `json:"item_id"`
		Title            string `json:"title"`
		Year             int32  `json:"year"`
		TMDBID           int32  `json:"tmdb_id"`
		QualityProfileID string `json:"quality_profile_id"`
		SeasonNumber     int32  `json:"season_number"`
		EpisodeNumber    int32  `json:"episode_number"`
		AbsoluteNumber   int32  `json:"absolute_number"`
		SeriesType       string `json:"series_type"`
		SeriesID         string `json:"series_id"`
		Monitored        *bool  `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json", "activity.invalid_json")
		return
	}
	itemType := strings.TrimSpace(body.ItemType)
	itemID := strings.TrimSpace(body.ItemID)
	if itemType == "" || itemID == "" {
		writeAPIError(w, http.StatusBadRequest, "item_type and item_id required", "activity.no_wanted")
		return
	}
	req := &automationv1.AddToQueueRequest{
		ItemType:         itemType,
		ItemId:           itemID,
		Title:            strings.TrimSpace(body.Title),
		Year:             body.Year,
		TmdbId:           body.TMDBID,
		QualityProfileId: strings.TrimSpace(body.QualityProfileID),
		SeasonNumber:     body.SeasonNumber,
		EpisodeNumber:    body.EpisodeNumber,
		AbsoluteNumber:   body.AbsoluteNumber,
		SeriesType:       strings.TrimSpace(body.SeriesType),
		SeriesId:         strings.TrimSpace(body.SeriesID),
	}
	if body.Monitored != nil {
		req.Monitored = *body.Monitored
	}
	resp, err := s.automation.AddToQueue(r.Context(), req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "activity.wanted_add_failed")
		return
	}
	writeJSON(w, map[string]any{
		"added":    true,
		"queue_id": resp.GetQueueId(),
	})
}
