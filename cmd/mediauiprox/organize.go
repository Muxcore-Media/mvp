package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
)

func organizePathAllowed(dir string, allowed []string) bool {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "" || clean == "." || clean == string(filepath.Separator) || !filepath.IsAbs(clean) {
		return false
	}
	for _, raw := range allowed {
		root := filepath.Clean(strings.TrimSpace(raw))
		if root == "" || root == "." || root == string(filepath.Separator) {
			continue
		}
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *server) organizeAllowedPrefixes(ctx context.Context) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	if s.roots != nil {
		resp, err := s.roots.ListRoots(ctx, &rootsv1.ListRootsRequest{})
		if err == nil {
			for _, root := range resp.GetRoots() {
				if root != nil {
					add(root.GetPath())
				}
			}
		}
	}
	if s.scanner != nil {
		resp, err := s.scanner.ListWatchDirs(ctx, &scannerv1.ListWatchDirsRequest{})
		if err == nil {
			for _, dir := range resp.GetDirs() {
				if dir == nil {
					continue
				}
				add(dir.GetPath())
				add(dir.GetLibraryPath())
				add(dir.GetTvLibraryPath())
				add(dir.GetMusicLibraryPath())
			}
		}
	}
	return out
}

func publicOrganizeResult(item *renamev1.RenameResult) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	return map[string]any{
		"original":   item.GetOriginal(),
		"renamed_to": item.GetRenamedTo(),
		"success":    item.GetSuccess(),
		"error":      item.GetError(),
	}
}

func (s *server) handleOrganizeLibrary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "organize.forbidden"})
		return
	}
	if s.rename == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "rename unavailable", "code": "organize.unavailable"})
		return
	}
	var body struct {
		Directory  string `json:"directory"`
		MediaType  string `json:"media_type"`
		DryRun     *bool  `json:"dry_run"`
		ImportMode string `json:"import_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "organize.invalid_json"})
		return
	}
	directory := filepath.Clean(strings.TrimSpace(body.Directory))
	if directory == "" || directory == "." {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "directory required", "code": "organize.directory_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	allowed := s.organizeAllowedPrefixes(ctx)
	if len(allowed) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "add a library root or watch folder before organizing", "code": "organize.root_required"})
		return
	}
	if !organizePathAllowed(directory, allowed) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "directory must be a configured root or watch folder", "code": "organize.path_forbidden"})
		return
	}
	dryRun := true
	if body.DryRun != nil {
		dryRun = *body.DryRun
	}
	mediaType := normalizeRenameMediaType(body.MediaType)
	resp, err := s.rename.BatchRename(ctx, &renamev1.BatchRenameRequest{
		Directory:  directory,
		MediaType:  mediaType,
		DryRun:     dryRun,
		ImportMode: strings.TrimSpace(body.ImportMode),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "organize.failed"})
		return
	}
	items := make([]map[string]any, 0, len(resp.GetResults()))
	for _, item := range resp.GetResults() {
		items = append(items, publicOrganizeResult(item))
	}
	writeJSON(w, map[string]any{
		"available":  true,
		"directory":  directory,
		"media_type": mediaType,
		"dry_run":    dryRun,
		"total":      resp.GetTotal(),
		"renamed":    resp.GetRenamed(),
		"errors":     resp.GetErrors(),
		"items":      items,
	})
}
