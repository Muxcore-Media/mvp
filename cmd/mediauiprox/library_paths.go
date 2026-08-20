package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

// libraryPathsFile maps companion library keys → root folder path prefixes.
// Example:
//
//	{
//	  "musicvideos": ["/data/library/Music Videos", "/mnt/media/musicvideos"],
//	  "homevideos": ["/data/library/Home Videos"]
//	}
type libraryPathsFile struct {
	MusicVideos []string `json:"musicvideos"`
	HomeVideos  []string `json:"homevideos"`
}

type libraryPathsStore struct {
	mu   sync.Mutex
	path string
}

func newLibraryPathsStore(path, userdataDir string) *libraryPathsStore {
	if path == "" {
		if userdataDir != "" {
			path = filepath.Join(userdataDir, "library-paths.json")
		} else {
			path = filepath.Join(os.TempDir(), "muxcore-library-paths.json")
		}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	return &libraryPathsStore{path: path}
}

func (s *libraryPathsStore) load() libraryPathsFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return libraryPathsFile{}
	}
	var f libraryPathsFile
	if json.Unmarshal(raw, &f) != nil {
		return libraryPathsFile{}
	}
	return f
}

func (s *libraryPathsStore) prefixes(lib string) []string {
	f := s.load()
	switch normalizeLibraryKey(lib) {
	case "musicvideos":
		return cleanPrefixes(f.MusicVideos)
	case "homevideos":
		return cleanPrefixes(f.HomeVideos)
	default:
		return nil
	}
}

func cleanPrefixes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, filepath.Clean(p))
	}
	return out
}

func normalizeLibraryKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	switch s {
	case "musicvideos", "musicvideo":
		return "musicvideos"
	case "homevideos", "homevideo":
		return "homevideos"
	default:
		return ""
	}
}

func pathSuggestsLibrary(root, lib string) bool {
	root = strings.ToLower(filepath.ToSlash(root))
	if root == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(root))
	segs := strings.Split(root, "/")
	check := func(s string) bool {
		s = strings.TrimSpace(s)
		switch lib {
		case "musicvideos":
			return s == "musicvideos" || s == "musicvideo" || s == "music videos" || s == "music-videos"
		case "homevideos":
			return s == "homevideos" || s == "homevideo" || s == "home videos" || s == "home-videos"
		}
		return false
	}
	if check(base) {
		return true
	}
	for _, s := range segs {
		if check(s) {
			return true
		}
	}
	switch lib {
	case "musicvideos":
		return strings.Contains(root, "/music videos/") || strings.Contains(root, "/musicvideos/") ||
			strings.HasSuffix(root, "/music videos") || strings.HasSuffix(root, "/musicvideos")
	case "homevideos":
		return strings.Contains(root, "/home videos/") || strings.Contains(root, "/homevideos/") ||
			strings.HasSuffix(root, "/home videos") || strings.HasSuffix(root, "/homevideos")
	}
	return false
}

func prefixMatch(root string, prefixes []string) bool {
	root = strings.ToLower(filepath.Clean(root))
	if root == "" {
		return false
	}
	for _, p := range prefixes {
		p = strings.ToLower(filepath.Clean(p))
		if p == "" {
			continue
		}
		if root == p || strings.HasPrefix(root, p+string(filepath.Separator)) || strings.HasPrefix(root, p+"/") {
			return true
		}
		// Also allow slash-normalized compare on Unix-style paths from other hosts.
		rp, pp := filepath.ToSlash(root), filepath.ToSlash(p)
		if rp == pp || strings.HasPrefix(rp, pp+"/") {
			return true
		}
	}
	return false
}

func genreSuggestsLibrary(genre, lib string) bool {
	g := strings.ToLower(strings.TrimSpace(genre))
	switch lib {
	case "musicvideos":
		return g == "music video" || g == "musicvideo" || g == "music videos"
	case "homevideos":
		return g == "home video" || g == "homevideo" || g == "home videos" || g == "personal"
	}
	return false
}

var (
	homeVideoHeuristicRE = regexp.MustCompile(`(?i)home.?video|home.?movie|personal|family|vacation|wedding|camcorder`)
	musicVideoHeuristicHints = []string{"music video", "musicvideo", "official video"}
)

func heuristicLibraryMatch(m *mgmntv1.MovieItem, lib string) bool {
	if m == nil {
		return false
	}
	blob := strings.ToLower(strings.Join([]string{
		m.GetTitle(), m.GetTagline(), m.GetOverview(), strings.Join(m.GetGenres(), " "),
	}, " "))
	switch lib {
	case "musicvideos":
		for _, h := range musicVideoHeuristicHints {
			if strings.Contains(blob, h) {
				return true
			}
		}
		for _, g := range m.GetGenres() {
			if strings.Contains(strings.ToLower(g), "music") {
				return true
			}
		}
		return false
	case "homevideos":
		if homeVideoHeuristicRE.MatchString(blob) {
			return true
		}
		return m.GetTmdbId() == 0 && m.GetHasFile()
	}
	return false
}

func movieMatchesLibrary(m *mgmntv1.MovieItem, lib string, prefixes []string, allowHeuristic bool) bool {
	if m == nil || lib == "" {
		return false
	}
	root := m.GetRootFolderPath()
	if prefixMatch(root, prefixes) {
		return true
	}
	if pathSuggestsLibrary(root, lib) {
		return true
	}
	for _, g := range m.GetGenres() {
		if genreSuggestsLibrary(g, lib) {
			return true
		}
	}
	if allowHeuristic && heuristicLibraryMatch(m, lib) {
		return true
	}
	return false
}

func libraryTagLabels(lib string) []string {
	switch lib {
	case "musicvideos":
		return []string{"musicvideo", "music video", "musicvideos", "music videos"}
	case "homevideos":
		return []string{"homevideo", "home video", "homevideos", "home videos"}
	}
	return nil
}

func (s *server) resolveLibraryTagID(ctx context.Context, lib string) string {
	if s.movies == nil {
		return ""
	}
	resp, err := s.movies.ListTags(ctx, &mgmntv1.ListTagsRequest{})
	if err != nil || resp == nil {
		return ""
	}
	want := map[string]bool{}
	for _, l := range libraryTagLabels(lib) {
		want[strings.ToLower(l)] = true
	}
	for _, t := range resp.GetTags() {
		if want[strings.ToLower(strings.TrimSpace(t.GetLabel()))] {
			return t.GetId()
		}
	}
	return ""
}

func (s *server) collectMoviesForLibrary(ctx context.Context, lib string) ([]*mgmntv1.MovieItem, error) {
	byID := map[string]*mgmntv1.MovieItem{}
	add := func(list []*mgmntv1.MovieItem) {
		for _, m := range list {
			if m == nil || m.GetId() == "" {
				continue
			}
			byID[m.GetId()] = m
		}
	}

	if tagID := s.resolveLibraryTagID(ctx, lib); tagID != "" {
		resp, err := s.movies.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
			Page: 1, PageSize: 100, TagId: tagID,
		})
		if err == nil && resp != nil {
			add(resp.GetMovies())
		}
	}

	// Scan library pages for path / genre / heuristic matches.
	for page := int32(1); page <= 10; page++ {
		resp, err := s.movies.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
			Page: page, PageSize: 100,
		})
		if err != nil {
			return nil, err
		}
		add(resp.GetMovies())
		if int(page)*100 >= int(resp.GetTotal()) || len(resp.GetMovies()) == 0 {
			break
		}
	}

	out := make([]*mgmntv1.MovieItem, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	return out, nil
}

func (s *server) handleLibraryMovies(w http.ResponseWriter, r *http.Request, lib string) {
	page, pageSize := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var prefixes []string
	if s.libraryPaths != nil {
		prefixes = s.libraryPaths.prefixes(lib)
	}
	allowHeuristic := len(prefixes) == 0
	filterMode := "config"
	if allowHeuristic {
		filterMode = "heuristic"
	}

	all, err := s.collectMoviesForLibrary(ctx, lib)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	matched := make([]*mgmntv1.MovieItem, 0)
	for _, m := range all {
		if movieMatchesLibrary(m, lib, prefixes, allowHeuristic) {
			matched = append(matched, m)
		}
	}

	total := len(matched)
	start := int((page - 1) * pageSize)
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + int(pageSize)
	if end > total {
		end = total
	}
	pageItems := matched[start:end]

	items := make([]map[string]any, 0, len(pageItems))
	for _, m := range pageItems {
		row := movieJSON(m)
		row["library_type"] = lib
		items = append(items, row)
	}
	writeJSON(w, map[string]any{
		"items":       items,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"library":     lib,
		"filter_mode": filterMode,
	})
}
