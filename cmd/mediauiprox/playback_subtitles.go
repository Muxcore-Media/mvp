package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/module/pathguard"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
)

type playbackSubtitleTrack struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Language string `json:"language,omitempty"`
	Src      string `json:"src"`
	Default  bool   `json:"default,omitempty"`
	// path is the confined sidecar file behind an sc_ track. It is server-side
	// only: it is bound to the session and never sent to the client.
	path string
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

func hasDotDotSegment(p string) bool {
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// resolveAbsMediaPath only joins; module rows are untrusted until
// discoverSidecarSubtitles confines the result against the media roots.
func resolveAbsMediaPath(rootFolder, filePath string) string {
	if filePath == "" || strings.ContainsRune(filePath, 0) || strings.ContainsRune(rootFolder, 0) || hasDotDotSegment(filePath) {
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
		if s.movies == nil {
			return "", ""
		}
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
		return s.resolveEpisodePlaybackFile(ctx, mediaID)
	default:
		return "", ""
	}
}

func (s *server) resolveEpisodePlaybackFile(ctx context.Context, mediaID string) (absPath, fileID string) {
	if mediaID == "" || s.tvHTTP == nil {
		return "", ""
	}
	u := *s.tvHTTP
	u.Path = strings.TrimRight(s.tvHTTP.Path, "/") + "/api/episodes/" + url.PathEscape(mediaID) + "/file"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", ""
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var body struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return "", ""
	}
	return body.FilePath, body.FileID
}

// requestSessionID is "" for a request without a session, which binds nothing.
func requestSessionID(r *http.Request) string {
	if tok := sessionTokenFromRequest(r); tok != "" {
		return sessionID(tok)
	}
	return ""
}

// Sidecar limits (ADR-0042). media-subtitles also takes .ssa/.sub/.txt; the
// BFF serves only what the player can show.
const sidecarMaxBytes = 5 << 20

var sidecarExts = map[string]bool{".srt": true, ".vtt": true, ".ass": true}

// confineMediaPath resolves p against the configured media roots. It fails
// closed: no roots, a path outside them or an unreadable root is "".
func (s *server) confineMediaPath(p string) string {
	if len(s.mediaRoots) == 0 || p == "" {
		return ""
	}
	real, err := pathguard.Confine(p, s.mediaRoots)
	if err != nil {
		return ""
	}
	return real
}

// sidecarFileOK reports whether fi is a regular, in-limit file (never a
// symlink, directory or FIFO).
func sidecarFileOK(fi os.FileInfo) bool {
	return fi.Mode().IsRegular() && fi.Size() <= sidecarMaxBytes
}

// discoverSidecarSubtitles lists the subtitle files the video at videoPath
// owns (sidecarOwnedBy). The video is confined to the media roots first; a
// sidecar must resolve to a regular file in that same directory. Track IDs are
// opaque and scoped to sessionID; the path travels only in track.path.
func (s *server) discoverSidecarSubtitles(sessionID, videoPath string) []playbackSubtitleTrack {
	video := s.confineMediaPath(videoPath)
	if video == "" || sessionID == "" {
		return nil
	}
	dir := filepath.Dir(video)
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
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
		if !sidecarExts[strings.ToLower(filepath.Ext(name))] || !sidecarOwnedBy(name, video, entries) {
			continue
		}
		abs := s.confineMediaPath(filepath.Join(dir, name))
		if abs == "" || filepath.Dir(abs) != dir {
			continue
		}
		if fi, err := os.Lstat(abs); err != nil || !sidecarFileOK(fi) {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		lang := sidecarLanguage(stem, base)
		id := sidecarTrackID(sessionID, abs)
		tracks = append(tracks, playbackSubtitleTrack{
			ID:       id,
			Label:    sidecarLabel(name, lang),
			Language: lang,
			Src:      "/api/playback/subtitles/" + url.PathEscape(id),
			path:     abs,
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

// sidecarTrackID is opaque: it hides the path and is meaningless to any other
// session. It is looked up in the binding table, never decoded.
func sidecarTrackID(sessionID, absPath string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + absPath))
	return "sc_" + hex.EncodeToString(sum[:16])
}

// moduleSubtitleTracks lists the subtitle rows of fileID. For a restricted
// principal a row that does not name fileID is dropped: the file ID is the only
// link to the authorized item (as in collectSubtitleFiles).
func (s *server) moduleSubtitleTracks(ctx context.Context, fileID string, restricted bool) []playbackSubtitleTrack {
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
		if restricted && sub.GetMediaFileId() != fileID {
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
		if src := sub.GetSource(); src != "" && src != "sidecar" && src != "upload" {
			label += " · " + src
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

	// A restricted principal's grant names the item the gate authorized; the
	// tracks are resolved and bound for exactly that item, whatever the
	// handler would make of src.
	grant, restricted := parentalGrantFrom(ctx)
	if restricted {
		kind, mediaID = grant.item.Kind, grant.item.ID
	}
	if !restricted {
		// Unrestricted sessions are bound too (ADR-0042): the item is only
		// recorded, never authorized.
		grant = parentalGrant{sessionID: requestSessionID(r), item: parentalItem{Kind: kind, ID: mediaID}}
	}
	absPath, fileID := s.resolvePlaybackMediaFile(ctx, kind, mediaID)
	tracks := s.discoverSidecarSubtitles(grant.sessionID, absPath)
	tracks = append(tracks, s.moduleSubtitleTracks(ctx, fileID, restricted)...)
	tracks = s.parental.bindSubtitleTracks(grant, restricted, tracks)

	writeJSONStatus(w, http.StatusOK, playbackSubtitlesResponse{Tracks: tracks})
}

func (s *server) handlePlaybackSubtitleServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := subtitleTrackIDFromRequest(r)
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, "subtitle id required", "subtitles.id_required")
		return
	}

	if strings.HasPrefix(id, "sc_") {
		// Never decoded: the ID is only a key into this session's bindings. A
		// restricted principal has already passed the gate's per-fetch check.
		sid := requestSessionID(r)
		if grant, ok := parentalGrantFrom(r.Context()); ok {
			sid = grant.sessionID
		}
		path, ok := s.parental.subtitles.path(sid, id, s.parental.now())
		if !ok {
			writeAPIError(w, http.StatusNotFound, "subtitle not found", "subtitles.not_found")
			return
		}
		s.serveSubtitleFile(w, path)
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

// serveSubtitleFile re-confines the bound path and reads it through an
// os.Root on its directory, so a file swapped for a symlink after listing
// cannot leave that directory.
func (s *server) serveSubtitleFile(w http.ResponseWriter, path string) {
	notFound := func() { writeAPIError(w, http.StatusNotFound, "subtitle not found", "subtitles.not_found") }
	real := s.confineMediaPath(path)
	if real == "" || !sidecarExts[strings.ToLower(filepath.Ext(real))] {
		notFound()
		return
	}
	root, err := os.OpenRoot(filepath.Dir(real))
	if err != nil {
		notFound()
		return
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(real)
	if fi, err := root.Lstat(name); err != nil || !sidecarFileOK(fi) {
		notFound()
		return
	}
	f, err := root.Open(name)
	if err != nil {
		notFound()
		return
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !sidecarFileOK(fi) {
		notFound()
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, sidecarMaxBytes+1))
	if err != nil || len(data) > sidecarMaxBytes {
		notFound()
		return
	}
	if strings.EqualFold(filepath.Ext(name), ".srt") {
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
