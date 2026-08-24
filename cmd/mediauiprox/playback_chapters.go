package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
	introoutrov1 "github.com/Muxcore-Media/media-intro-outro/proto/gen/muxcore/introoutro/v1"
)

type playbackChapter struct {
	Index        int32   `json:"index"`
	Title        string  `json:"title"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Source       string  `json:"source,omitempty"` // embedded | scene | interval
}

type playbackChaptersResponse struct {
	Src      string            `json:"src"`
	Chapters []playbackChapter `json:"chapters"`
	Enabled  bool              `json:"enabled"`
	Source   string            `json:"source,omitempty"` // dominant source when uniform
}

func chaptersResponseSource(chs []playbackChapter) string {
	if len(chs) == 0 {
		return ""
	}
	src := chs[0].Source
	for _, ch := range chs[1:] {
		if ch.Source != src {
			return "mixed"
		}
	}
	return src
}

func (s *server) chaptersForMedia(ctx context.Context, kind, mediaID string) []playbackChapter {
	resp := s.analyzeMediaByStream(ctx, kind, mediaID)
	if resp == nil {
		return nil
	}
	return fromFFprobeChapters(resp.GetChapters())
}

func fromFFprobeChapters(in []*ffprobev1.Chapter) []playbackChapter {
	out := make([]playbackChapter, 0, len(in))
	for _, ch := range in {
		if ch == nil {
			continue
		}
		out = append(out, playbackChapter{
			Index:        ch.GetIndex(),
			Title:        ch.GetTitle(),
			StartSeconds: ch.GetStartSeconds(),
			EndSeconds:   ch.GetEndSeconds(),
			Source:       ch.GetSource(),
		})
	}
	return out
}

func (s *server) chaptersForMediaID(ctx context.Context, mediaID string) []playbackChapter {
	if ch := s.chaptersForMedia(ctx, "movie", mediaID); len(ch) > 0 {
		return ch
	}
	return s.chaptersForMedia(ctx, "episode", mediaID)
}

func toIntroOutroChapters(chs []playbackChapter) []*introoutrov1.Chapter {
	out := make([]*introoutrov1.Chapter, 0, len(chs))
	for _, ch := range chs {
		out = append(out, &introoutrov1.Chapter{
			Title:        ch.Title,
			StartSeconds: ch.StartSeconds,
			EndSeconds:   ch.EndSeconds,
		})
	}
	return out
}

// handlePlaybackChapters returns chapter markers for a stream source: embedded
// container chapters when present, otherwise scene-detected or interval-generated
// via media-ffprobe.
// SPA: GET /api/playback/chapters?src=/stream/movies/…
func (s *server) handlePlaybackChapters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writePlaybackError(w, newPlaybackErr(http.StatusMethodNotAllowed, "method not allowed", "playback.method_not_allowed"))
		return
	}
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	if src == "" {
		writePlaybackError(w, newPlaybackErr(http.StatusBadRequest, "src required", "playback.src_required"))
		return
	}
	if s.ffprobe == nil {
		writeJSONStatus(w, http.StatusOK, playbackChaptersResponse{Src: src, Chapters: []playbackChapter{}, Enabled: false})
		return
	}

	kind, mediaID := parseStreamMediaID(src)
	if kind == "" {
		writeJSONStatus(w, http.StatusOK, playbackChaptersResponse{Src: src, Chapters: []playbackChapter{}, Enabled: false})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	out := s.chaptersForMedia(ctx, kind, mediaID)
	if len(out) == 0 {
		if duration, dErr := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get("duration")), 64); dErr == nil && duration >= 180 {
			out = intervalChaptersFallback(duration)
		}
	}
	writeJSONStatus(w, http.StatusOK, playbackChaptersResponse{
		Src:      src,
		Chapters: out,
		Enabled:  true,
		Source:   chaptersResponseSource(out),
	})
}

func intervalChaptersFallback(durationSec float64) []playbackChapter {
	interval := 600.0
	if durationSec < 1200 {
		interval = 300
	}
	var out []playbackChapter
	idx := int32(0)
	for start := 0.0; start < durationSec-45; start += interval {
		end := start + interval
		if end > durationSec {
			end = durationSec
		}
		out = append(out, playbackChapter{
			Index:        idx,
			Title:        "Chapter " + strconv.Itoa(int(idx)+1),
			StartSeconds: start,
			EndSeconds:   end,
			Source:       "interval",
		})
		idx++
	}
	if len(out) < 2 {
		return nil
	}
	return out
}
