package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *server) handleItemWatchStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	itemID := strings.TrimSpace(r.URL.Query().Get("id"))
	if itemID == "" {
		itemID = strings.TrimSpace(r.URL.Query().Get("item_id"))
	}
	if itemID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "watch_stats.id_required"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, map[string]any{"available": false, "itemId": itemID})
		return
	}
	runtime := strings.TrimSpace(r.URL.Query().Get("runtime"))
	path := "/stats/item?item_id=" + url.QueryEscape(itemID)
	if runtime != "" {
		path += "&runtime=" + url.QueryEscape(runtime)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	raw, ok, err := s.playbackMonitorGET(ctx, path)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "itemId": itemID, "error": err.Error()})
		return
	}
	writeJSON(w, publicItemWatchStats(itemID, raw, ok))
}

func publicItemWatchStats(itemID string, raw map[string]any, available bool) map[string]any {
	if raw == nil {
		raw = map[string]any{}
	}
	return map[string]any{
		"available":          available,
		"itemId":             firstNonEmpty(firstString(raw, "itemId", "item_id", "ItemID"), itemID),
		"playCount":          floatVal(raw, "playCount", "play_count", "PlayCount"),
		"viewCount":          floatVal(raw, "viewCount", "view_count", "ViewCount"),
		"uniqueUsers":        floatVal(raw, "uniqueUsers", "unique_users", "UniqueUsers"),
		"watchMinutes":       floatVal(raw, "watchMinutes", "watch_minutes", "WatchMinutes"),
		"longestMinutes":     floatVal(raw, "longestMinutes", "longest_minutes", "LongestMinutes"),
		"hasActivity":        boolVal(raw, "hasActivity", "has_activity", "HasActivity"),
		"neverWatched":       boolVal(raw, "neverWatched", "never_watched", "NeverWatched"),
		"daysSinceLastWatch": floatVal(raw, "daysSinceLastWatch", "days_since_last_watch", "DaysSinceLastWatch"),
		"lastWatchedAt":      firstString(raw, "lastWatchedAt", "last_watched_at", "LastWatchedAt"),
	}
}

func (s *server) handleWatchStatsStale(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "neverWatched": 0, "stale": 0})
		return
	}
	staleDays := strings.TrimSpace(r.URL.Query().Get("stale_days"))
	if staleDays == "" {
		staleDays = "90"
	}
	limit := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limit == "" {
		limit = "40"
	}
	path := "/library/stale?stale_days=" + url.QueryEscape(staleDays) + "&limit=" + url.QueryEscape(limit)
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	raw, ok, err := s.playbackMonitorGET(ctx, path)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "neverWatched": 0, "stale": 0, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{
		"available":    ok,
		"items":        publicStaleLibraryItems(homeOrKey(raw, "items", "Items")),
		"neverWatched": floatVal(raw, "neverWatched", "never_watched_count", "NeverWatchedCount"),
		"stale":        floatVal(raw, "stale", "stale_count", "StaleCount"),
	})
}

func (s *server) handleImportTautulli(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_stats.forbidden"})
		return
	}
	var body struct {
		TautulliURL string `json:"tautulli_url"`
		APIKey      string `json:"api_key"`
		RecordsJSON string `json:"records_json"`
		ServerID    string `json:"server_id"`
		DryRun      bool   `json:"dry_run"`
		MaxRecords  int    `json:"max_records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "watch_stats.invalid_json"})
		return
	}
	if strings.TrimSpace(body.RecordsJSON) == "" && strings.TrimSpace(body.TautulliURL) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "tautulli_url or records_json required", "code": "watch_stats.import_required"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "watch_stats.unavailable"})
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"tautulli_url": body.TautulliURL,
		"api_key":      body.APIKey,
		"records_json": body.RecordsJSON,
		"server_id":    body.ServerID,
		"dry_run":      body.DryRun,
		"max_records":  body.MaxRecords,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	raw, err := s.playbackMonitorPOST(ctx, "/import/tautulli", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watch_stats.import_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"imported":     floatVal(raw, "imported", "Imported"),
		"skipped":      floatVal(raw, "skipped", "Skipped"),
		"failed":       floatVal(raw, "failed", "Failed"),
		"totalFetched": floatVal(raw, "totalFetched", "total_fetched", "TotalFetched"),
		"error":        firstString(raw, "error", "Error"),
		"dryRun":       body.DryRun,
	})
}

func (s *server) handleImportJellystat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "watch_stats.forbidden"})
		return
	}
	var body struct {
		BackupJSON string `json:"backup_json"`
		ServerID   string `json:"server_id"`
		ServerType string `json:"server_type"`
		DryRun     bool   `json:"dry_run"`
		MaxRecords int    `json:"max_records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "watch_stats.invalid_json"})
		return
	}
	if strings.TrimSpace(body.BackupJSON) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "backup_json required", "code": "watch_stats.import_required"})
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "playback monitor is not connected", "code": "watch_stats.unavailable"})
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"backup_json": body.BackupJSON,
		"server_id":   body.ServerID,
		"server_type": body.ServerType,
		"dry_run":     body.DryRun,
		"max_records": body.MaxRecords,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	raw, err := s.playbackMonitorPOST(ctx, "/import/jellystat", payload)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "watch_stats.import_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"imported":     floatVal(raw, "imported", "Imported"),
		"skipped":      floatVal(raw, "skipped", "Skipped"),
		"failed":       floatVal(raw, "failed", "Failed"),
		"totalFetched": floatVal(raw, "totalFetched", "total_fetched", "TotalFetched"),
		"error":        firstString(raw, "error", "Error"),
		"dryRun":       body.DryRun,
	})
}

func (s *server) handleWatchStatsDuplicates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, map[string]any{"available": false, "groups": []any{}})
		return
	}
	limit := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limit == "" {
		limit = "20"
	}
	path := "/library/duplicates?limit=" + url.QueryEscape(limit)
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	raw, ok, err := s.playbackMonitorGET(ctx, path)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "groups": []any{}, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{
		"available": ok,
		"groups":    publicDuplicateGroups(homeOrKey(raw, "groups", "Groups")),
	})
}

func (s *server) handleWatchStatsStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, map[string]any{"available": false, "libraries": []any{}, "totalItems": 0, "totalBytes": 0, "duplicateWasteBytes": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	raw, ok, err := s.playbackMonitorGET(ctx, "/library/storage")
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "libraries": []any{}, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{
		"available":           ok,
		"totalItems":          floatVal(raw, "totalItems", "total_items", "TotalItems"),
		"totalBytes":          floatVal(raw, "totalBytes", "total_bytes", "TotalBytes"),
		"duplicateWasteBytes": floatVal(raw, "duplicateWasteBytes", "duplicate_waste_bytes", "DuplicateWaste"),
		"totalHuman":          firstString(raw, "totalHuman", "total_human"),
		"duplicateWasteHuman": firstString(raw, "duplicateWasteHuman", "duplicate_waste_human"),
		"libraries":           publicStorageLibraries(homeOrKey(raw, "libraries", "Libraries")),
	})
}

func (s *server) handleWatchStatsStorageHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	days := watchStatsDays(r)
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, map[string]any{
			"available":  false,
			"days":       days,
			"history":    []any{},
			"prediction": map[string]any{"growthBytesPerDay": 0, "projectedBytes": 0, "horizonDays": 90},
		})
		return
	}
	path := "/library/storage/history?days=" + strconv.Itoa(days)
	if lib := strings.TrimSpace(r.URL.Query().Get("library")); lib != "" {
		path += "&library_name=" + url.QueryEscape(lib)
	}
	if pred := strings.TrimSpace(r.URL.Query().Get("predict_days")); pred != "" {
		path += "&predict_days=" + url.QueryEscape(pred)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	raw, ok, err := s.playbackMonitorGET(ctx, path)
	if err != nil {
		writeJSON(w, map[string]any{
			"available":  false,
			"days":       days,
			"history":    []any{},
			"prediction": map[string]any{"growthBytesPerDay": 0, "projectedBytes": 0, "horizonDays": 90},
			"error":      err.Error(),
		})
		return
	}
	writeJSON(w, publicStorageHistory(raw, ok, days))
}

func publicStorageHistory(raw map[string]any, available bool, days int) map[string]any {
	if raw == nil {
		raw = map[string]any{}
	}
	nested, _ := raw["data"].(map[string]any)
	if nested == nil {
		nested = raw
	}
	pred, _ := nested["prediction"].(map[string]any)
	if pred == nil {
		pred, _ = raw["prediction"].(map[string]any)
	}
	if pred == nil {
		pred = map[string]any{}
	}
	history := make([]map[string]any, 0)
	for _, row := range homeOrKey(nested, "history", "History") {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		day := firstString(rec, "day", "Day")
		if day == "" {
			continue
		}
		history = append(history, map[string]any{
			"day":       day,
			"bytes":     floatVal(rec, "bytes", "totalBytes", "total_bytes", "TotalBytes"),
			"itemCount": floatVal(rec, "itemCount", "item_count", "ItemCount"),
		})
	}
	return map[string]any{
		"available": available,
		"days":      days,
		"history":   history,
		"prediction": map[string]any{
			"growthBytesPerDay": floatVal(pred, "growthBytesPerDay", "growth_bytes_per_day"),
			"projectedBytes":    floatVal(pred, "projectedBytes", "projected_bytes"),
			"horizonDays":       floatVal(pred, "horizonDays", "horizon_days"),
		},
	}
}

func publicDuplicateGroups(rows []any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		title := firstString(rec, "title", "Title")
		if title == "" {
			continue
		}
		copies := make([]map[string]any, 0)
		for _, copy := range homeOrKey(rec, "copies", "Copies") {
			c, ok := copy.(map[string]any)
			if !ok {
				continue
			}
			copies = append(copies, map[string]any{
				"title":   firstString(c, "title", "Title"),
				"itemId":  firstString(c, "itemId", "item_id", "ItemID"),
				"library": firstString(c, "library", "libraryName", "LibraryName"),
				"path":    firstString(c, "path", "mediaPath", "MediaPath"),
				"bytes":   floatVal(c, "bytes", "fileSizeBytes", "FileSizeBytes", "file_size_bytes"),
			})
		}
		out = append(out, map[string]any{
			"title":     title,
			"groupKey":  firstString(rec, "groupKey", "group_key", "GroupKey"),
			"copyCount": floatVal(rec, "copyCount", "copy_count", "CopyCount"),
			"copies":    copies,
		})
	}
	return out
}

func publicStorageLibraries(rows []any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(rec, "name", "library", "libraryName", "LibraryName")
		if name == "" {
			continue
		}
		out = append(out, map[string]any{
			"name":      name,
			"itemCount": floatVal(rec, "itemCount", "item_count", "ItemCount"),
			"bytes":     floatVal(rec, "bytes", "totalBytes", "TotalBytes", "total_bytes"),
		})
	}
	return out
}

func publicStaleLibraryItems(rows []any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		title := firstString(rec, "title", "Title")
		if title == "" {
			continue
		}
		out = append(out, map[string]any{
			"title":      title,
			"itemId":     firstString(rec, "itemId", "item_id", "ItemID"),
			"mediaType":  strings.ToLower(firstString(rec, "mediaType", "MediaType", "media_type")),
			"library":    firstString(rec, "library", "libraryName", "LibraryName"),
			"category":   strings.ToLower(firstString(rec, "category", "Category")),
			"daysStale":  floatVal(rec, "daysStale", "days_stale", "DaysStale"),
			"watchCount": floatVal(rec, "watchCount", "watch_count", "WatchCount"),
		})
	}
	return out
}

func (s *server) handleWatchStatsCharts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	days := watchStatsDays(r)
	empty := map[string]any{
		"available":           false,
		"days":                days,
		"hours":               []any{},
		"users":               []any{},
		"platforms":           []any{},
		"daysOfWeek":          []any{},
		"months":              []any{},
		"streamTypes":         []any{},
		"streamResolutions":   []any{},
		"sourceResolutions":   []any{},
		"platformResolutions": []any{},
		"concurrent":          map[string]any{"peak": 0, "series": []any{}},
	}
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, empty)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	q := "?days=" + strconv.Itoa(days)
	hourRaw, hourOK, hourErr := s.playbackMonitorGET(ctx, "/stats/plays-by-hour"+q)
	userRaw, userOK, userErr := s.playbackMonitorGET(ctx, "/stats/plays-by-top-users"+q+"&limit=8")
	platRaw, platOK, platErr := s.playbackMonitorGET(ctx, "/stats/plays-by-top-platforms"+q+"&limit=8")
	concRaw, concOK, concErr := s.playbackMonitorGET(ctx, "/stats/concurrent-streams"+q)
	if hourErr != nil || userErr != nil || platErr != nil || concErr != nil {
		empty["error"] = firstWatchStatsError(hourErr, userErr, platErr, concErr)
		writeJSON(w, empty)
		return
	}
	dowRaw, dowOK, dowErr := s.playbackMonitorGET(ctx, "/stats/plays-by-dow"+q)
	monthRaw, monthOK, monthErr := s.playbackMonitorGET(ctx, "/stats/plays-by-month"+q)
	typeRaw, typeOK, typeErr := s.playbackMonitorGET(ctx, "/stats/plays-by-stream-type"+q)
	streamResRaw, streamResOK, streamResErr := s.playbackMonitorGET(ctx, "/stats/plays-by-stream-resolution"+q+"&limit=8")
	sourceResRaw, sourceResOK, sourceResErr := s.playbackMonitorGET(ctx, "/stats/plays-by-source-resolution"+q+"&limit=8")
	platResRaw, platResOK, platResErr := s.playbackMonitorGET(ctx, "/stats/plays-by-platform-resolution"+q+"&limit=8")
	writeJSON(w, map[string]any{
		"available":           hourOK || userOK || platOK || concOK || dowOK || monthOK || typeOK || streamResOK || sourceResOK || platResOK,
		"days":                days,
		"hours":               publicChartBuckets(homeOrKey(hourRaw, "rows", "Rows")),
		"users":               publicChartBuckets(homeOrKey(userRaw, "rows", "Rows")),
		"platforms":           publicChartBuckets(homeOrKey(platRaw, "rows", "Rows")),
		"daysOfWeek":          chartBucketsOrEmpty(dowRaw, dowOK, dowErr),
		"months":              chartBucketsOrEmpty(monthRaw, monthOK, monthErr),
		"streamTypes":         chartBucketsOrEmpty(typeRaw, typeOK, typeErr),
		"streamResolutions":   chartBucketsOrEmpty(streamResRaw, streamResOK, streamResErr),
		"sourceResolutions":   chartBucketsOrEmpty(sourceResRaw, sourceResOK, sourceResErr),
		"platformResolutions": chartBucketsOrEmpty(platResRaw, platResOK, platResErr),
		"concurrent":          publicConcurrentChart(concRaw),
	})
}

func watchStatsDays(r *http.Request) int {
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			days = n
		}
	}
	if days > 365 {
		days = 365
	}
	return days
}

func chartBucketsOrEmpty(raw map[string]any, ok bool, err error) []map[string]any {
	if err != nil || !ok {
		return []map[string]any{}
	}
	return publicChartBuckets(homeOrKey(raw, "rows", "Rows"))
}

func publicChartBuckets(rows []any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		label := firstString(rec, "label", "Label", "key", "Key")
		if label == "" {
			continue
		}
		out = append(out, map[string]any{
			"key":   firstString(rec, "key", "Key"),
			"label": label,
			"count": floatVal(rec, "count", "Count"),
		})
	}
	return out
}

func publicConcurrentChart(raw map[string]any) map[string]any {
	if raw == nil {
		return map[string]any{"peak": 0, "series": []any{}}
	}
	series := homeOrKey(raw, "series", "Series")
	out := make([]map[string]any, 0, len(series))
	peak := 0.0
	for _, row := range series {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(rec, "name", "Name")
		if name == "" {
			continue
		}
		max := 0.0
		data, _ := rec["data"].([]any)
		if data == nil {
			data, _ = rec["Data"].([]any)
		}
		for _, v := range data {
			n := floatVal(map[string]any{"v": v}, "v")
			if n > max {
				max = n
			}
			if n > peak {
				peak = n
			}
		}
		out = append(out, map[string]any{"name": name, "peak": max})
	}
	return map[string]any{"peak": peak, "series": out}
}

func (s *server) handleWatchStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	days := watchStatsDays(r)
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		writeJSON(w, map[string]any{
			"available": false,
			"days":      days,
			"stats":     []any{},
			"topMovies": []any{},
			"topShows":  []any{},
			"plays":     []any{},
			"libraries": []any{},
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	q := "?days=" + strconv.Itoa(days)
	homeRaw, homeOK, homeErr := s.playbackMonitorGET(ctx, "/stats/home"+q)
	topRaw, topOK, topErr := s.playbackMonitorGET(ctx, "/stats/top-content"+q+"&limit=8")
	playsRaw, playsOK, playsErr := s.playbackMonitorGET(ctx, "/stats/plays-by-date"+q)
	libRaw, libOK, libErr := s.playbackMonitorGET(ctx, "/stats/libraries"+q+"&limit=8")
	if homeErr != nil || topErr != nil || playsErr != nil || libErr != nil {
		errMsg := firstWatchStatsError(homeErr, topErr, playsErr, libErr)
		writeJSON(w, map[string]any{
			"available": false,
			"days":      days,
			"stats":     []any{},
			"topMovies": []any{},
			"topShows":  []any{},
			"plays":     []any{},
			"libraries": []any{},
			"error":     errMsg,
		})
		return
	}
	available := homeOK || topOK || playsOK || libOK
	writeJSON(w, map[string]any{
		"available": available,
		"days":      days,
		"stats":     publicWatchHomeStats(homeRaw),
		"topMovies": publicWatchTop(homeOrKey(topRaw, "movies", "Movies")),
		"topShows":  publicWatchTop(homeOrKey(topRaw, "shows", "Shows")),
		"plays":     publicWatchPlays(playsRaw),
		"libraries": publicWatchLibraries(libRaw),
	})
}

func firstWatchStatsError(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

func (s *server) playbackMonitorGET(ctx context.Context, path string) (map[string]any, bool, error) {
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		return map[string]any{}, false, nil
	}
	u := *s.playbackMonitorHTTP
	base := strings.TrimRight(s.playbackMonitorHTTP.Path, "/")
	q := ""
	if i := strings.Index(path, "?"); i >= 0 {
		q = path[i+1:]
		path = path[:i]
	}
	u.Path = base + path
	u.RawQuery = q
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(s.playbackMonitorToken); tok != "" {
		req.Header.Set("X-Playback-Monitor-Token", tok)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, false, errPlaybackMonitorStatus(resp.StatusCode, msg)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, true, nil
}

func (s *server) playbackMonitorPOST(ctx context.Context, path string, payload []byte) (map[string]any, error) {
	return s.playbackMonitorDo(ctx, http.MethodPost, path, payload)
}

func (s *server) playbackMonitorDo(ctx context.Context, method, path string, payload []byte) (map[string]any, error) {
	if s.playbackMonitorHTTP == nil || strings.TrimSpace(s.playbackMonitorHTTP.String()) == "" {
		return nil, errPlaybackMonitorStatus(http.StatusServiceUnavailable, "playback monitor is not connected")
	}
	u := *s.playbackMonitorHTTP
	u.Path = strings.TrimRight(s.playbackMonitorHTTP.Path, "/") + path
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := strings.TrimSpace(s.playbackMonitorToken); tok != "" {
		req.Header.Set("X-Playback-Monitor-Token", tok)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, errPlaybackMonitorStatus(resp.StatusCode, msg)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return decoded, nil
}

func homeOrKey(raw map[string]any, keys ...string) []any {
	if raw == nil {
		return nil
	}
	for _, key := range keys {
		if rows, ok := raw[key].([]any); ok {
			return rows
		}
	}
	return nil
}

func publicWatchHomeStats(raw map[string]any) []map[string]any {
	rows := homeOrKey(raw, "stats", "Stats")
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		key := firstString(rec, "key", "Key")
		label := firstString(rec, "label", "Label")
		if key == "" && label == "" {
			continue
		}
		out = append(out, map[string]any{
			"key":   key,
			"label": label,
			"value": floatVal(rec, "value", "Value"),
		})
	}
	return out
}

func publicWatchTop(rows []any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		title := firstString(rec, "title", "Title")
		if title == "" {
			continue
		}
		out = append(out, map[string]any{
			"title":        title,
			"mediaType":    strings.ToLower(firstString(rec, "mediaType", "MediaType", "media_type")),
			"playCount":    floatVal(rec, "playCount", "PlayCount", "play_count"),
			"watchMinutes": floatVal(rec, "watchMinutes", "WatchMinutes", "watch_minutes"),
		})
	}
	return out
}

func publicWatchPlays(raw map[string]any) []map[string]any {
	rows := homeOrKey(raw, "rows", "Rows")
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		date := firstString(rec, "date", "Date")
		if date == "" {
			continue
		}
		out = append(out, map[string]any{
			"date":  date,
			"count": floatVal(rec, "count", "Count"),
		})
	}
	return out
}

func publicWatchLibraries(raw map[string]any) []map[string]any {
	rows := homeOrKey(raw, "libraries", "Libraries")
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rec, ok := row.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(rec, "libraryName", "LibraryName", "library_name", "name", "Name")
		if name == "" {
			continue
		}
		out = append(out, map[string]any{
			"name":         name,
			"playCount":    floatVal(rec, "playCount", "PlayCount", "play_count"),
			"watchMinutes": floatVal(rec, "watchMinutes", "WatchMinutes", "watch_minutes"),
		})
	}
	return out
}
