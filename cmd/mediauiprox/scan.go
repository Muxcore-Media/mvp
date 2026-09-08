package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
)

const householdScanTimeout = 45 * time.Second

func scannerDisplayStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "running":
		return "running"
	case "failed", "error":
		return "failed"
	case "completed", "idle", "":
		return "idle"
	default:
		return raw
	}
}

func scannerLastScanAt(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func publicScanStats(stats *scannerv1.GetStatsResponse) map[string]any {
	if stats == nil {
		return map[string]any{
			"available": true, "status": "idle", "last_scan_at": "",
			"watch_dirs": 0, "total_imported": 0,
			"last_found": 0, "last_imported": 0, "last_skipped": 0, "last_error": "",
		}
	}
	status := scannerDisplayStatus(stats.GetLastScanStatus())
	lastError := stats.GetLastError()
	if status == "failed" && lastError == "" {
		lastError = stats.GetLastScanStatus()
	}
	return map[string]any{
		"available":      true,
		"status":         status,
		"last_scan_at":   scannerLastScanAt(stats.GetLastScanAt()),
		"watch_dirs":     stats.GetWatchDirs(),
		"total_imported": stats.GetTotalImported(),
		"last_found":     stats.GetLastScanFilesFound(),
		"last_imported":  stats.GetLastScanFilesImported(),
		"last_skipped":   stats.GetLastScanFilesSkipped(),
		"last_error":     lastError,
	}
}

func normalizeScanType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "library_roots", "library-roots", "roots", "library":
		return "library_roots"
	default:
		return "watch"
	}
}

func (s *server) handleLibraryScanStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "scan.forbidden"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	plus := s.libraryPlusScanStatus(ctx)
	plusOK := plusScanAvailable(plus)
	if s.scanner == nil {
		out := map[string]any{
			"available": plusOK, "scanner": false, "status": "", "last_scan_at": "",
			"watch_dirs": 0, "total_imported": 0,
			"last_found": 0, "last_imported": 0, "last_skipped": 0, "last_error": "",
			"libraries": plus,
		}
		if plusOK {
			out["status"] = "idle"
		}
		writeJSON(w, out)
		return
	}
	stats, err := s.scanner.GetStats(ctx, &scannerv1.GetStatsRequest{})
	if err != nil {
		out := map[string]any{
			"available": plusOK, "scanner": false, "status": "", "last_scan_at": "",
			"watch_dirs": 0, "total_imported": 0,
			"last_found": 0, "last_imported": 0, "last_skipped": 0,
			"last_error": err.Error(),
			"libraries":  plus,
		}
		if plusOK {
			out["status"] = "idle"
		}
		writeJSON(w, out)
		return
	}
	out := publicScanStats(stats)
	out["scanner"] = true
	out["libraries"] = plus
	writeJSON(w, out)
}

func (s *server) handleLibraryScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "scan.forbidden"})
		return
	}
	var body struct {
		Type      string `json:"type"`
		Path      string `json:"path"`
		MediaType string `json:"media_type"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "scan.invalid_json"})
			return
		}
	}
	scanType := normalizeScanType(body.Type)
	path := strings.TrimSpace(body.Path)
	mediaType := strings.TrimSpace(body.MediaType)
	plusOnly := libraryPlusMediaType(mediaType)
	ctx, cancel := context.WithTimeout(r.Context(), householdScanTimeout)
	defer cancel()

	var found, imported, skipped int32
	var msg string
	var scannerErr error
	if !plusOnly && s.scanner == nil && scanType != "library_roots" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-scanner is not connected", "code": "scan.unavailable"})
		return
	}
	if !plusOnly && s.scanner != nil {
		if scanType == "library_roots" {
			resp, err := s.scanner.ScanLibraryRoots(ctx, &scannerv1.ScanLibraryRootsRequest{
				Path: path, MediaType: mediaType,
			})
			if err != nil {
				scannerErr = err
			} else {
				found, imported, skipped = resp.GetFilesFound(), resp.GetFilesImported(), resp.GetFilesSkipped()
				msg = fmt.Sprintf("library roots scan complete — found=%d imported=%d skipped=%d", found, imported, skipped)
			}
		} else {
			resp, err := s.scanner.Scan(ctx, &scannerv1.ScanRequest{
				Path: path, MediaType: mediaType,
			})
			if err != nil {
				writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "scan.failed"})
				return
			}
			found, imported, skipped = resp.GetFilesFound(), resp.GetFilesImported(), resp.GetFilesSkipped()
			msg = fmt.Sprintf("watch scan complete — found=%d imported=%d skipped=%d", found, imported, skipped)
		}
	}

	var plus []map[string]any
	if scanType == "library_roots" || plusOnly {
		var plusFound, plusImported, plusSkipped int32
		plus, plusFound, plusImported, plusSkipped = s.scanLibraryPlus(ctx, mediaType)
		found += plusFound
		imported += plusImported
		skipped += plusSkipped
		if bits := plusScanSummary(plus); bits != "" {
			if msg == "" {
				msg = "library-plus scan complete — " + bits
			} else {
				msg = msg + " · " + bits
			}
		}
	}
	if msg == "" && scannerErr != nil && !plusScanAvailable(plus) {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": scannerErr.Error(), "code": "scan.failed"})
		return
	}
	if msg == "" && s.scanner == nil && !plusScanAvailable(plus) {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-scanner is not connected", "code": "scan.unavailable"})
		return
	}
	if msg == "" {
		msg = fmt.Sprintf("library roots scan complete — found=%d imported=%d skipped=%d", found, imported, skipped)
	}
	writeJSON(w, map[string]any{
		"type":           scanType,
		"files_found":    found,
		"files_imported": imported,
		"files_skipped":  skipped,
		"message":        msg,
		"libraries":      plus,
	})
}

func libraryPlusMediaType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "music", "books", "comics", "audiobooks":
		return true
	default:
		return false
	}
}

func plusScanAvailable(rows []map[string]any) bool {
	for _, row := range rows {
		if row["available"] == true {
			return true
		}
	}
	return false
}

func plusScanSummary(rows []map[string]any) string {
	bits := make([]string, 0, len(rows))
	for _, row := range rows {
		if row["available"] != true {
			continue
		}
		name, _ := row["library"].(string)
		imported := asInt32(row["files_imported"])
		bits = append(bits, fmt.Sprintf("%s imported=%d", name, imported))
	}
	return strings.Join(bits, " · ")
}

func (s *server) libraryPlusKinds() []libraryKind {
	return []libraryKind{
		{Name: "music", Upstream: s.musicHTTP, ListPath: "/api/artists", CodePrefix: "music"},
		{Name: "books", Upstream: s.booksHTTP, ListPath: "/api/authors", CodePrefix: "books"},
		{Name: "comics", Upstream: s.comicsHTTP, ListPath: "/api/series", CodePrefix: "comics"},
		{Name: "audiobooks", Upstream: s.audiobooksHTTP, ListPath: "/api/audiobooks", CodePrefix: "audiobooks"},
	}
}

func (s *server) libraryPlusScanStatus(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0, 4)
	for _, kind := range s.libraryPlusKinds() {
		out = append(out, map[string]any{
			"library":   kind.Name,
			"available": s.libraryModuleLive(ctx, kind),
		})
	}
	return out
}

func (s *server) scanLibraryPlus(ctx context.Context, mediaType string) ([]map[string]any, int32, int32, int32) {
	want := strings.ToLower(strings.TrimSpace(mediaType))
	rows := make([]map[string]any, 0, 4)
	var found, imported, skipped int32
	for _, kind := range s.libraryPlusKinds() {
		if want != "" && libraryPlusMediaType(want) && want != kind.Name {
			continue
		}
		row := s.postLibraryPlusScan(ctx, kind)
		rows = append(rows, row)
		if row["available"] == true {
			found += asInt32(row["files_found"])
			imported += asInt32(row["files_imported"])
			skipped += asInt32(row["files_skipped"])
		}
	}
	return rows, found, imported, skipped
}

func (s *server) postLibraryPlusScan(ctx context.Context, kind libraryKind) map[string]any {
	out := map[string]any{"library": kind.Name, "available": false, "files_found": 0, "files_imported": 0, "files_skipped": 0}
	if kind.Upstream == nil || kind.Upstream.String() == "" {
		return out
	}
	u := *kind.Upstream
	u.Path = strings.TrimRight(kind.Upstream.Path, "/") + "/api/scan"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		out["error"] = resp.Status
		return out
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		out["error"] = err.Error()
		return out
	}
	out["available"] = true
	out["files_found"] = asInt32(firstNonNil(raw["files_found"], raw["FilesFound"]))
	out["files_imported"] = asInt32(firstNonNil(raw["files_imported"], raw["FilesImported"]))
	out["files_skipped"] = asInt32(firstNonNil(raw["files_skipped"], raw["FilesSkipped"]))
	return out
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}
