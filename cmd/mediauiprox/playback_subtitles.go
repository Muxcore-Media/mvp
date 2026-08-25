package main

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
)

type playbackSubtitleTrack struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Language string `json:"language,omitempty"`
	Src      string `json:"src"`
	Default  bool   `json:"default,omitempty"`
}

type playbackSubtitlesResponse struct {
	Tracks []playbackSubtitleTrack `json:"tracks"`
}

var srtTimeRe = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

func parseStreamMediaID(src string) (kind, id string) {
	src = strings.TrimSpace(src)
	if strings.HasPrefix(src, "/stream/movies/") {
		id = strings.Trim(strings.TrimPrefix(src, "/stream/movies/"), "/")
		if u, err := url.PathUnescape(id); err == nil {
			id = u
		}
		return "movie", id
	}
	if strings.HasPrefix(src, "/stream/tv/") {
		id = strings.Trim(strings.TrimPrefix(src, "/stream/tv/"), "/")
		if u, err := url.PathUnescape(id); err == nil {
			id = u
		}
		return "episode", id
	}
	return "", ""
}

func resolveAbsMediaPath(rootFolder, filePath string) string {
	if filePath == "" {
		return ""
	}
	if filepath.IsAbs(filePath) {
		return filePath
	}
	if rootFolder == "" {
		return ""
	}
	rel := strings.TrimPrefix(filepath.ToSlash(filePath), "media/")
	candidate := filepath.Join(rootFolder, filepath.FromSlash(rel))
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate
	}
	return ""
}

func (s *server) resolvePlaybackMediaFile(ctx context.Context, kind, mediaID string) (absPath, fileID string) {
	if mediaID == "" {
		return "", ""
	}
	switch kind {
	case "movie":
		files, err := s.movies.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: mediaID})
		if err != nil || len(files.GetFiles()) == 0 {
			return "", ""
		}
		f := files.GetFiles()[0]
		movie, err := s.movies.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: mediaID})
		root := ""
		if err == nil && movie.GetMovie() != nil {
			root = movie.GetMovie().GetRootFolderPath()
		}
		return resolveAbsMediaPath(root, f.GetFilePath()), f.GetId()
	case "episode":
		if s.subtitles == nil {
			return "", ""
		}
		media, err := s.subtitles.GetMedia(ctx, &subtv1.GetMediaRequest{Id: mediaID})
		if err != nil || media.GetItem() == nil {
			return "", ""
		}
		return media.GetItem().GetVideoPath(), media.GetItem().GetMediaFileId()
	default:
		return "", ""
	}
}

func discoverSidecarSubtitles(videoPath string) []playbackSubtitleTrack {
	if videoPath == "" {
		return nil
	}
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var tracks []playbackSubtitleTrack
	seen := map[string]struct{}{}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		low := strings.ToLower(name)
		if !strings.HasSuffix(low, ".vtt") && !strings.HasSuffix(low, ".srt") && !strings.HasSuffix(low, ".ass") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stem != base && !strings.HasPrefix(stem, base+".") && !strings.HasPrefix(stem, base+" ") {
			continue
		}
		abs := filepath.Join(dir, name)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		lang := sidecarLanguage(stem, base)
		label := sidecarLabel(name, lang)
		tracks = append(tracks, playbackSubtitleTrack{
			ID:       sidecarTrackID(abs),
			Label:    label,
			Language: lang,
			Src:      "/api/playback/subtitles/" + url.PathEscape(sidecarTrackID(abs)),
		})
	}
	return tracks
}

func sidecarLanguage(stem, base string) string {
	suffix := strings.TrimPrefix(stem, base)
	suffix = strings.TrimPrefix(suffix, ".")
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return "und"
	}
	parts := strings.Split(suffix, ".")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) == 2 || len(p) == 3 {
			return strings.ToLower(p)
		}
	}
	return strings.ToLower(parts[0])
}

func sidecarLabel(name, lang string) string {
	if lang != "" && lang != "und" {
		return strings.ToUpper(lang) + " · " + name
	}
	return name
}

func sidecarTrackID(absPath string) string {
	return "sc_" + base64.RawURLEncoding.EncodeToString([]byte(absPath))
}

func decodeSidecarTrackID(id string) (string, bool) {
	if !strings.HasPrefix(id, "sc_") {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, "sc_"))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func (s *server) moduleSubtitleTracks(ctx context.Context, fileID string) []playbackSubtitleTrack {
	if s.subtitles == nil || fileID == "" {
		return nil
	}
	resp, err := s.subtitles.ListSubtitles(ctx, &subtv1.ListSubtitlesRequest{
		MediaFileId: fileID,
		Page:        1,
		PageSize:    50,
	})
	if err != nil {
		return nil
	}
	var tracks []playbackSubtitleTrack
	for _, sub := range resp.GetSubtitles() {
		if sub == nil || sub.GetId() == "" {
			continue
		}
		label := sub.GetLanguage()
		if label == "" {
			label = "und"
		}
		label = strings.ToUpper(label)
		if sub.GetForced() {
			label += " (forced)"
		}
		if sub.GetHearingImpaired() {
			label += " (HI)"
		}
		if sub.GetReleaseInfo() != "" {
			label += " · " + sub.GetReleaseInfo()
		}
		tracks = append(tracks, playbackSubtitleTrack{
			ID:       sub.GetId(),
			Label:    label,
			Language: sub.GetLanguage(),
			Src:      "/api/playback/subtitles/" + url.PathEscape(sub.GetId()),
		})
	}
	return tracks
}

func (s *server) handlePlaybackSubtitlesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	kind, mediaID := parseStreamMediaID(src)
	if mediaID == "" {
		writeJSONStatus(w, http.StatusOK, playbackSubtitlesResponse{Tracks: nil})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	absPath, fileID := s.resolvePlaybackMediaFile(ctx, kind, mediaID)
	tracks := discoverSidecarSubtitles(absPath)
	tracks = append(tracks, s.moduleSubtitleTracks(ctx, fileID)...)

	writeJSONStatus(w, http.StatusOK, playbackSubtitlesResponse{Tracks: tracks})
}

func (s *server) handlePlaybackSubtitleServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		id = strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/playback/subtitles/"), "/")
		if u, err := url.PathUnescape(id); err == nil {
			id = u
		}
	}
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, "subtitle id required", "subtitles.id_required")
		return
	}

	if path, ok := decodeSidecarTrackID(id); ok {
		serveSubtitleFile(w, path)
		return
	}

	if s.subtitlesHTTP != nil {
		u := *s.subtitlesHTTP
		u.Path = strings.TrimRight(u.Path, "/") + "/api/subtitles/" + url.PathEscape(id) + "/vtt"
		u.RawQuery = ""
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "subtitle proxy", "subtitles.proxy_error")
			return
		}
		resp, err := upstreamClient.Do(req)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "subtitle unavailable", "subtitles.unavailable")
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			writeAPIError(w, resp.StatusCode, "subtitle not found", "subtitles.not_found")
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = io.Copy(w, resp.Body)
		return
	}

	http.NotFound(w, r)
}

func serveSubtitleFile(w http.ResponseWriter, path string) {
	if path == "" {
		http.NotFound(w, nil)
		return
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		http.NotFound(w, nil)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	low := strings.ToLower(path)
	if strings.HasSuffix(low, ".srt") {
		data = []byte(srtToVTT(string(data)))
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

func srtToVTT(raw string) string {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	blocks := strings.Split(raw, "\n\n")
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		if len(lines) < 2 {
			continue
		}
		timeLine := 0
		if _, err := strconv.Atoi(strings.TrimSpace(lines[0])); err == nil && len(lines) > 2 {
			timeLine = 1
		}
		timing := srtTimeRe.ReplaceAllString(lines[timeLine], "${1}.${2}")
		timing = strings.ReplaceAll(timing, " --> ", " --> ")
		out.WriteString(timing)
		out.WriteString("\n")
		for _, ln := range lines[timeLine+1:] {
			out.WriteString(ln)
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	return out.String()
}
