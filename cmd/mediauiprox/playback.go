package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	introoutrov1 "github.com/Muxcore-Media/media-intro-outro/proto/gen/muxcore/introoutro/v1"
)

type playbackError struct {
	message string
	code    string
	status  int
}

func (e *playbackError) Error() string {
	return e.message
}

func newPlaybackErr(status int, message, code string) error {
	return &playbackError{message: message, code: code, status: status}
}

func playbackHTTP(err error) (status int, message, code string) {
	var pe *playbackError
	if errors.As(err, &pe) {
		return pe.status, pe.message, pe.code
	}
	return http.StatusInternalServerError, "playback error", "playback.internal_error"
}

func writePlaybackError(w http.ResponseWriter, err error) {
	status, message, code := playbackHTTP(err)
	writeJSONStatus(w, status, map[string]string{"error": message, "code": code})
}

type playbackPolicy struct {
	EnableResume     bool   `json:"enable_resume"`
	EnableTranscode  bool   `json:"enable_transcode"`
	PreferDirectPlay bool   `json:"prefer_direct_play"`
	TrickplayEnabled bool   `json:"trickplay_enabled"`
	MaxBitrateMbps   string `json:"max_bitrate_mbps"`
}

var (
	playbackPolicyMu sync.Mutex
)

func playbackPolicyPath() string {
	if p := strings.TrimSpace(os.Getenv("ADMIN_UI_PLAYBACK_FILE")); p != "" {
		return p
	}
	if d := strings.TrimSpace(os.Getenv("MEDIA_UI_USERDATA_DIR")); d != "" {
		return filepath.Join(d, "playback-policy.json")
	}
	return filepath.Join(os.TempDir(), "muxcore-admin-playback.json")
}

func loadPlaybackPolicy() playbackPolicy {
	playbackPolicyMu.Lock()
	defer playbackPolicyMu.Unlock()
	raw, err := os.ReadFile(playbackPolicyPath())
	if err != nil {
		return playbackPolicy{EnableResume: true, PreferDirectPlay: true, MaxBitrateMbps: "80"}
	}
	var p playbackPolicy
	if json.Unmarshal(raw, &p) != nil {
		return playbackPolicy{EnableResume: true, PreferDirectPlay: true, MaxBitrateMbps: "80"}
	}
	return p
}

type playbackResolveResponse struct {
	StreamURL          string `json:"stream_url"`
	Mode               string `json:"mode"`
	ResumeEnabled      bool   `json:"resume_enabled"`
	TranscoderEnabled  bool   `json:"transcoder_enabled"`
	PreferDirectPlay   bool   `json:"prefer_direct_play"`
	MaxBitrateMbps     string `json:"max_bitrate_mbps"`
	TrickplayEnabled   bool   `json:"trickplay_enabled"`
	TranscoderAvailable bool  `json:"transcoder_available"`
}

func (s *server) handlePlaybackResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writePlaybackError(w, newPlaybackErr(http.StatusMethodNotAllowed, "method not allowed", "playback.method_not_allowed"))
		return
	}
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	if src == "" {
		writePlaybackError(w, newPlaybackErr(http.StatusBadRequest, "src required", "playback.src_required"))
		return
	}
	pol := loadPlaybackPolicy()
	transcoderAvail := s.transcoderHTTP != nil && strings.TrimSpace(s.transcoderHTTP.String()) != ""
	mode := "direct"
	streamURL := src
	if strings.HasPrefix(src, "debrid:") {
		id := strings.TrimPrefix(src, "debrid:")
		streamURL = debridStreamURL(id)
	} else if pol.EnableTranscode && !pol.PreferDirectPlay && transcoderAvail {
		mode = "transcode"
		streamURL = "/stream/transcode?src=" + url.QueryEscape(src)
	}
	writeJSONStatus(w, http.StatusOK, playbackResolveResponse{
		StreamURL:           streamURL,
		Mode:                mode,
		ResumeEnabled:       pol.EnableResume,
		TranscoderEnabled:   pol.EnableTranscode,
		PreferDirectPlay:    pol.PreferDirectPlay,
		MaxBitrateMbps:      pol.MaxBitrateMbps,
		TrickplayEnabled:    pol.TrickplayEnabled,
		TranscoderAvailable: transcoderAvail,
	})
}

type playbackSegment struct {
	Kind         string  `json:"kind"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Confidence   float64 `json:"confidence"`
	Source       string  `json:"source"`
}

type playbackSegmentsResponse struct {
	MediaID  string            `json:"media_id"`
	Segments []playbackSegment `json:"segments"`
	Enabled  bool              `json:"enabled"`
}

// handlePlaybackSegments surfaces intro/outro/credits/recap skip segments for a
// media item, backed by the media-intro-outro module (GetSegments, falling back
// to an on-demand heuristic Detect+persist the first time a duration is known).
// SPA: GET /api/playback/segments?media_id=…&duration=<seconds>
func (s *server) handlePlaybackSegments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writePlaybackError(w, newPlaybackErr(http.StatusMethodNotAllowed, "method not allowed", "playback.method_not_allowed"))
		return
	}
	mediaID := strings.TrimSpace(r.URL.Query().Get("media_id"))
	if mediaID == "" {
		writePlaybackError(w, newPlaybackErr(http.StatusBadRequest, "media_id required", "playback.media_id_required"))
		return
	}
	if s.introOutro == nil {
		writeJSONStatus(w, http.StatusOK, playbackSegmentsResponse{MediaID: mediaID, Segments: []playbackSegment{}, Enabled: false})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	segs, err := s.introOutro.GetSegments(ctx, &introoutrov1.GetSegmentsRequest{MediaId: mediaID})
	if err != nil {
		writeJSONStatus(w, http.StatusOK, playbackSegmentsResponse{MediaID: mediaID, Segments: []playbackSegment{}, Enabled: false})
		return
	}

	out := fromIntroOutroSegments(segs.GetSegments())
	if len(out) == 0 {
		duration, _ := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get("duration")), 64)
		chapters := s.chaptersForMediaID(ctx, mediaID)
		if duration > 0 || len(chapters) > 0 {
			detectReq := &introoutrov1.DetectRequest{
				MediaId:         mediaID,
				DurationSeconds: duration,
				Persist:         true,
				Chapters:        toIntroOutroChapters(chapters),
			}
			detected, detErr := s.introOutro.Detect(ctx, detectReq)
			if detErr == nil {
				out = fromIntroOutroSegments(detected.GetSegments())
			}
		}
	}
	writeJSONStatus(w, http.StatusOK, playbackSegmentsResponse{MediaID: mediaID, Segments: out, Enabled: true})
}

func fromIntroOutroSegments(in []*introoutrov1.Segment) []playbackSegment {
	out := make([]playbackSegment, 0, len(in))
	for _, seg := range in {
		if seg == nil {
			continue
		}
		out = append(out, playbackSegment{
			Kind:         seg.GetKind(),
			StartSeconds: seg.GetStartSeconds(),
			EndSeconds:   seg.GetEndSeconds(),
			Confidence:   seg.GetConfidence(),
			Source:       seg.GetSource(),
		})
	}
	return out
}

func (s *server) handleTranscodeStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writePlaybackError(w, newPlaybackErr(http.StatusMethodNotAllowed, "method not allowed", "playback.method_not_allowed"))
		return
	}
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	if src == "" {
		writePlaybackError(w, newPlaybackErr(http.StatusBadRequest, "src required", "playback.src_required"))
		return
	}
	pol := loadPlaybackPolicy()
	if !pol.EnableTranscode {
		http.Redirect(w, r, src, http.StatusTemporaryRedirect)
		return
	}
	if s.transcoderHTTP == nil {
		http.Redirect(w, r, src, http.StatusTemporaryRedirect)
		return
	}

	sourceURL := s.playbackSourceURL(src)
	upstream := *s.transcoderHTTP
	upstream.Path = ""
	upstream.RawPath = ""
	upstream.Fragment = ""
	proxy := httputil.NewSingleHostReverseProxy(&upstream)

	q := url.Values{}
	q.Set("src", sourceURL)
	if profile := strings.TrimSpace(r.URL.Query().Get("profile")); profile != "" {
		q.Set("profile", profile)
	}
	if gpu := strings.TrimSpace(r.URL.Query().Get("gpu")); gpu != "" {
		q.Set("gpu", gpu)
	}
	if start := strings.TrimSpace(r.URL.Query().Get("start")); start != "" {
		q.Set("start", start)
	}
	if maxHeight := strings.TrimSpace(r.URL.Query().Get("max_height")); maxHeight != "" {
		q.Set("max_height", maxHeight)
	}
	if audioIndex := strings.TrimSpace(r.URL.Query().Get("audio_index")); audioIndex != "" {
		q.Set("audio_index", audioIndex)
	}

	r2 := r.Clone(r.Context())
	r2.URL.Scheme = upstream.Scheme
	r2.URL.Host = upstream.Host
	r2.URL.Path = "/stream/transcode"
	r2.URL.RawQuery = q.Encode()
	proxy.ServeHTTP(w, r2)
}

// handleTrickplaySprite proxies scrubbing-preview sprite requests to
// media-transcoder, which generates (and caches) a tiled thumbnail sheet on
// first request for a given source + interval.
// SPA: GET /stream/trickplay?src=…&duration=<seconds>[&interval=<seconds>]
func (s *server) handleTrickplaySprite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writePlaybackError(w, newPlaybackErr(http.StatusMethodNotAllowed, "method not allowed", "playback.method_not_allowed"))
		return
	}
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	if src == "" {
		writePlaybackError(w, newPlaybackErr(http.StatusBadRequest, "src required", "playback.src_required"))
		return
	}
	if s.transcoderHTTP == nil {
		writePlaybackError(w, newPlaybackErr(http.StatusServiceUnavailable, "transcoder unavailable", "playback.transcoder_unavailable"))
		return
	}

	sourceURL := s.playbackSourceURL(src)
	upstream := *s.transcoderHTTP
	upstream.Path = ""
	upstream.RawPath = ""
	upstream.Fragment = ""
	proxy := httputil.NewSingleHostReverseProxy(&upstream)

	q := url.Values{}
	q.Set("src", sourceURL)
	if duration := strings.TrimSpace(r.URL.Query().Get("duration")); duration != "" {
		q.Set("duration", duration)
	}
	if interval := strings.TrimSpace(r.URL.Query().Get("interval")); interval != "" {
		q.Set("interval", interval)
	}

	r2 := r.Clone(r.Context())
	r2.URL.Scheme = upstream.Scheme
	r2.URL.Host = upstream.Host
	r2.URL.Path = "/stream/trickplay"
	r2.URL.RawQuery = q.Encode()
	proxy.ServeHTTP(w, r2)
}

func (s *server) playbackSourceURL(src string) string {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return src
	}
	if strings.HasPrefix(src, "/stream/movies/") {
		return strings.TrimRight(s.moviesHTTP.String(), "/") + src
	}
	if strings.HasPrefix(src, "/stream/tv/") {
		return strings.TrimRight(s.tvHTTP.String(), "/") + src
	}
	return src
}
