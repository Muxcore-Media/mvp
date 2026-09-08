package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

func (s *server) handleImportCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.scanner == nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	resp, err := s.scanner.ListImportCandidates(ctx, &scannerv1.ListImportCandidatesRequest{Limit: 100})
	if err != nil {
		writeJSON(w, map[string]any{
			"items":     []any{},
			"total":     0,
			"available": false,
			"error":     err.Error(),
		})
		return
	}
	items := make([]map[string]any, 0, len(resp.GetCandidates()))
	lookups := 0
	for _, c := range resp.GetCandidates() {
		if c == nil {
			continue
		}
		row := publicImportCandidate(c)
		if q := s.parseQualityForTitle(ctx, importCandidateParseTitle(c)); q != nil {
			row["quality"] = q
		}
		if lookups < 40 && s.enrichImportCandidate(ctx, row, c) {
			lookups++
		}
		items = append(items, row)
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     len(items),
		"available": true,
	})
}

func (s *server) handleImportPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.scanner == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Manual import is unavailable — start media-scanner.",
			"code":  "import.unavailable",
		})
		return
	}
	var body struct {
		Path          string `json:"path"`
		Title         string `json:"title"`
		MediaType     string `json:"media_type"`
		Year          int32  `json:"year"`
		TMDBID        int32  `json:"tmdb_id"`
		SeasonNumber  int32  `json:"season_number"`
		EpisodeNumber int32  `json:"episode_number"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	path := strings.TrimSpace(body.Path)
	if path == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "path is required",
			"code":  "import.path_required",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.scanner.ImportPath(ctx, &scannerv1.ImportPathRequest{
		Path:          path,
		Title:         strings.TrimSpace(body.Title),
		MediaType:     strings.TrimSpace(body.MediaType),
		Year:          body.Year,
		TmdbId:        body.TMDBID,
		SeasonNumber:  body.SeasonNumber,
		EpisodeNumber: body.EpisodeNumber,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(),
			"code":  "import.failed",
		})
		return
	}
	writeJSON(w, map[string]any{
		"imported": resp.GetFilesImported(),
		"skipped":  resp.GetFilesSkipped(),
		"found":    resp.GetFilesFound(),
		"message":  importResultMessage(resp),
	})
}

func publicImportCandidate(c *scannerv1.ImportCandidate) map[string]any {
	title := strings.TrimSpace(c.GetTitle())
	if title == "" {
		title = strings.TrimSpace(c.GetName())
	}
	return map[string]any{
		"path":           c.GetPath(),
		"name":           c.GetName(),
		"title":          title,
		"size":           c.GetSize(),
		"media_type":     c.GetMediaType(),
		"year":           c.GetYear(),
		"season_number":  c.GetSeasonNumber(),
		"episode_number": c.GetEpisodeNumber(),
		"artist":         c.GetArtist(),
		"album":          c.GetAlbum(),
		"watch_dir_id":   c.GetWatchDirId(),
	}
}

func importCandidateParseTitle(c *scannerv1.ImportCandidate) string {
	if c == nil {
		return ""
	}
	if name := strings.TrimSpace(c.GetName()); name != "" {
		return name
	}
	if path := strings.TrimSpace(c.GetPath()); path != "" {
		return filepath.Base(path)
	}
	return strings.TrimSpace(c.GetTitle())
}

func isTVImport(mediaType string, season, episode int32) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "tv", "episode", "series":
		return true
	default:
		return season > 0 || episode > 0
	}
}

// enrichImportCandidate matches a TV download to a library episode (Sonarr Manual Import).
// Returns true when LookupEpisode was attempted so callers can bound fan-out.
func (s *server) enrichImportCandidate(ctx context.Context, row map[string]any, c *scannerv1.ImportCandidate) bool {
	if s == nil || s.tv == nil || c == nil {
		return false
	}
	if !isTVImport(c.GetMediaType(), c.GetSeasonNumber(), c.GetEpisodeNumber()) {
		return false
	}
	title := strings.TrimSpace(c.GetTitle())
	if title == "" {
		title = strings.TrimSpace(c.GetName())
	}
	if title == "" && c.GetSeasonNumber() == 0 && c.GetEpisodeNumber() == 0 {
		return false
	}
	resp, err := s.tv.LookupEpisode(ctx, &tvmgmtv1.LookupEpisodeRequest{
		Title:         title,
		Year:          c.GetYear(),
		SeasonNumber:  c.GetSeasonNumber(),
		EpisodeNumber: c.GetEpisodeNumber(),
	})
	if err != nil || resp == nil {
		return true
	}
	row["matched"] = resp.GetFound()
	if name := strings.TrimSpace(resp.GetSeriesName()); name != "" {
		row["series_name"] = name
	}
	if id := strings.TrimSpace(resp.GetSeriesId()); id != "" {
		row["series_id"] = id
	}
	if ep := strings.TrimSpace(resp.GetEpisodeTitle()); ep != "" {
		row["episode_title"] = ep
	}
	if air := strings.TrimSpace(resp.GetAirDate()); air != "" {
		row["air_date"] = air
	}
	if resp.GetFound() {
		if resp.GetSeasonNumber() > 0 {
			row["season_number"] = resp.GetSeasonNumber()
		}
		if resp.GetEpisodeNumber() > 0 {
			row["episode_number"] = resp.GetEpisodeNumber()
		}
	}
	return true
}

func importResultMessage(resp *scannerv1.ImportPathResponse) string {
	return fmt.Sprintf("imported=%d skipped=%d found=%d", resp.GetFilesImported(), resp.GetFilesSkipped(), resp.GetFilesFound())
}
