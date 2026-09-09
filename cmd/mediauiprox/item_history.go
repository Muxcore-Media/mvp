package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

func historyEventLabel(t mediaadminv1.HistoryEventType) string {
	switch t {
	case mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_GRAB:
		return "grab"
	case mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_IMPORT:
		return "import"
	case mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_DELETE_ITEM:
		return "delete_item"
	case mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_DELETE_FILE:
		return "delete_file"
	default:
		return ""
	}
}

func parseHistoryEventFilter(raw string) mediaadminv1.HistoryEventType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "grab", "1":
		return mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_GRAB
	case "import", "2":
		return mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_IMPORT
	case "delete_item", "delete-item", "3":
		return mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_DELETE_ITEM
	case "delete_file", "delete-file", "4":
		return mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_DELETE_FILE
	default:
		return mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_UNSPECIFIED
	}
}

func publicHistoryRecord(rec *mediaadminv1.HistoryRecord) map[string]any {
	if rec == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":           rec.GetId(),
		"event_type":   historyEventLabel(rec.GetEventType()),
		"item_id":      rec.GetItemId(),
		"title":        rec.GetTitle(),
		"source_title": rec.GetSourceTitle(),
		"quality":      rec.GetQuality(),
		"indexer":      rec.GetIndexer(),
		"file_path":    rec.GetFilePath(),
		"download_id":  rec.GetDownloadId(),
		"created_at":   rec.GetCreatedAt(),
	}
}

func (s *server) listItemHistory(ctx context.Context, client mediaadminv1.MediaAdminServiceClient, itemID, event string) (map[string]any, bool) {
	if client == nil {
		return map[string]any{"available": false, "items": []any{}, "total": 0}, false
	}
	resp, err := client.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{
		Page:      1,
		PageSize:  50,
		ItemId:    itemID,
		EventType: parseHistoryEventFilter(event),
	})
	if err != nil {
		return map[string]any{"available": false, "items": []any{}, "total": 0, "error": err.Error()}, false
	}
	items := make([]map[string]any, 0, len(resp.GetRecords()))
	for _, rec := range resp.GetRecords() {
		if rec == nil {
			continue
		}
		items = append(items, publicHistoryRecord(rec))
	}
	return map[string]any{
		"available": true,
		"items":     items,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
	}, true
}

func (s *server) handleListMovieHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "history.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	body, _ := s.listItemHistory(ctx, s.moviesAdmin, id, r.URL.Query().Get("event"))
	writeJSON(w, body)
}

func (s *server) handleListTVHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "history.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	body, _ := s.listItemHistory(ctx, s.tvAdmin, id, r.URL.Query().Get("event"))
	writeJSON(w, body)
}

func (s *server) handleListMusicHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "history.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	body, _ := s.listItemHistory(ctx, s.musicAdmin, id, r.URL.Query().Get("event"))
	writeJSON(w, body)
}

func (s *server) handleListBookHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "history.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	body, _ := s.listItemHistory(ctx, s.booksAdmin, id, r.URL.Query().Get("event"))
	writeJSON(w, body)
}

func (s *server) listLibraryPlusHistory(w http.ResponseWriter, r *http.Request, upstream *url.URL, modulePath string) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "history.id_required"})
		return
	}
	if upstream == nil || upstream.String() == "" {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "total": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	raw, err := s.getLibraryPlusJSON(ctx, upstream, modulePath+url.PathEscape(id)+"/history")
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "total": 0, "error": err.Error()})
		return
	}
	if _, ok := raw["available"]; !ok {
		raw["available"] = true
	}
	if _, ok := raw["items"]; !ok {
		raw["items"] = []any{}
	}
	writeJSON(w, raw)
}

func (s *server) handleListComicHistory(w http.ResponseWriter, r *http.Request) {
	s.listLibraryPlusHistory(w, r, s.comicsHTTP, "/api/series/")
}

func (s *server) handleListAudiobookHistory(w http.ResponseWriter, r *http.Request) {
	s.listLibraryPlusHistory(w, r, s.audiobooksHTTP, "/api/audiobooks/")
}
