package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
)

type playbackAnalysisVideo struct {
	Codec           string  `json:"codec,omitempty"`
	CodecLong       string  `json:"codec_long,omitempty"`
	Width           int32   `json:"width,omitempty"`
	Height          int32   `json:"height,omitempty"`
	ResolutionLabel string  `json:"resolution_label,omitempty"`
	FrameRate       float64 `json:"frame_rate,omitempty"`
	BitrateKbps     int32   `json:"bitrate_kbps,omitempty"`
	HDR             bool    `json:"hdr,omitempty"`
	HDRType         string  `json:"hdr_type,omitempty"`
	AspectRatio     string  `json:"aspect_ratio,omitempty"`
}

type playbackAnalysisAudio struct {
	Index         int32  `json:"index"`
	Codec         string `json:"codec,omitempty"`
	CodecLong     string `json:"codec_long,omitempty"`
	Language      string `json:"language,omitempty"`
	Channels      int32  `json:"channels,omitempty"`
	ChannelLayout string `json:"channel_layout,omitempty"`
	BitrateKbps   int32  `json:"bitrate_kbps,omitempty"`
	Profile       string `json:"profile,omitempty"`
	Label         string `json:"label,omitempty"`
}

type playbackAnalysisSubtitle struct {
	Index           int32  `json:"index"`
	Codec           string `json:"codec,omitempty"`
	Language        string `json:"language,omitempty"`
	Forced          bool   `json:"forced,omitempty"`
	HearingImpaired bool   `json:"hearing_impaired,omitempty"`
	PictureBased    bool   `json:"picture_based,omitempty"`
	TextBased       bool   `json:"text_based,omitempty"`
	Label           string `json:"label,omitempty"`
}

type playbackAnalysisQuality struct {
	Label      string `json:"label,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Source     string `json:"source,omitempty"`
	CodecGroup string `json:"codec_group,omitempty"`
	HDR        bool   `json:"hdr,omitempty"`
}

type playbackAnalysisResponse struct {
	Src         string                     `json:"src"`
	Enabled     bool                       `json:"enabled"`
	Container   string                     `json:"container,omitempty"`
	DurationSec float64                    `json:"duration_seconds,omitempty"`
	BitrateKbps int32                      `json:"bitrate_kbps,omitempty"`
	InfoLine    string                     `json:"info_line,omitempty"`
	Video       *playbackAnalysisVideo     `json:"video,omitempty"`
	Audio       []playbackAnalysisAudio    `json:"audio,omitempty"`
	Subtitles   []playbackAnalysisSubtitle `json:"subtitles,omitempty"`
	Quality     *playbackAnalysisQuality   `json:"quality,omitempty"`
}

func bitrateKbps(bps float64) int32 {
	if bps <= 0 {
		return 0
	}
	return int32(bps / 1000)
}

func audioTrackLabel(lang, layout string, channels int32, codec string) string {
	parts := []string{}
	if lang != "" {
		parts = append(parts, strings.ToUpper(lang))
	}
	if layout != "" {
		parts = append(parts, layout)
	} else if channels > 0 {
		parts = append(parts, formatChannelCount(channels))
	}
	if codec != "" {
		parts = append(parts, strings.ToUpper(codec))
	}
	if len(parts) == 0 {
		return "Audio"
	}
	return strings.Join(parts, " · ")
}

func formatChannelCount(ch int32) string {
	switch ch {
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	default:
		return ""
	}
}

func subtitleTrackLabel(lang, codec string, forced, sdh bool) string {
	parts := []string{}
	if lang != "" {
		parts = append(parts, strings.ToUpper(lang))
	}
	if forced {
		parts = append(parts, "Forced")
	}
	if sdh {
		parts = append(parts, "SDH")
	}
	if codec != "" {
		parts = append(parts, strings.ToUpper(codec))
	}
	if len(parts) == 0 {
		return "Subtitle"
	}
	return strings.Join(parts, " · ")
}

func buildInfoLine(v *playbackAnalysisVideo, q *playbackAnalysisQuality) string {
	if q != nil && q.Label != "" {
		return q.Label
	}
	if v == nil {
		return ""
	}
	parts := []string{}
	if v.ResolutionLabel != "" {
		parts = append(parts, v.ResolutionLabel)
	} else if v.Height > 0 {
		parts = append(parts, formatHeightLabel(v.Height))
	}
	if v.HDRType != "" {
		parts = append(parts, v.HDRType)
	} else if v.HDR {
		parts = append(parts, "HDR")
	}
	if v.Codec != "" {
		parts = append(parts, strings.ToUpper(v.Codec))
	}
	return strings.Join(parts, " · ")
}

func formatHeightLabel(h int32) string {
	switch {
	case h >= 2160:
		return "2160p"
	case h >= 1440:
		return "1440p"
	case h >= 1080:
		return "1080p"
	case h >= 720:
		return "720p"
	default:
		return "SD"
	}
}

func fromAnalyzeResponse(src string, resp *ffprobev1.AnalyzeResponse) playbackAnalysisResponse {
	out := playbackAnalysisResponse{Src: src, Enabled: true}
	if resp == nil {
		out.Enabled = false
		return out
	}
	out.Container = resp.GetContainer()
	out.DurationSec = resp.GetDurationSeconds()
	out.BitrateKbps = bitrateKbps(resp.GetOverallBitrate())

	if v := resp.GetVideo(); v != nil {
		out.Video = &playbackAnalysisVideo{
			Codec:           v.GetCodec(),
			CodecLong:       v.GetCodecLong(),
			Width:           v.GetWidth(),
			Height:          v.GetHeight(),
			ResolutionLabel: v.GetResolutionLabel(),
			FrameRate:       v.GetFrameRate(),
			BitrateKbps:     bitrateKbps(v.GetBitrate()),
			HDR:             v.GetHdr(),
			HDRType:         v.GetHdrType(),
			AspectRatio:     v.GetAspectRatio(),
		}
	}
	for _, a := range resp.GetAudio() {
		if a == nil {
			continue
		}
		out.Audio = append(out.Audio, playbackAnalysisAudio{
			Index:         a.GetIndex(),
			Codec:         a.GetCodec(),
			CodecLong:     a.GetCodecLong(),
			Language:      a.GetLanguage(),
			Channels:      a.GetChannels(),
			ChannelLayout: a.GetChannelLayout(),
			BitrateKbps:   bitrateKbps(a.GetBitrate()),
			Profile:       a.GetProfile(),
			Label:         audioTrackLabel(a.GetLanguage(), a.GetChannelLayout(), a.GetChannels(), a.GetCodec()),
		})
	}
	for _, sub := range resp.GetSubtitles() {
		if sub == nil {
			continue
		}
		picture := isPictureSubtitleCodec(sub.GetCodec())
		out.Subtitles = append(out.Subtitles, playbackAnalysisSubtitle{
			Index:           sub.GetIndex(),
			Codec:           sub.GetCodec(),
			Language:        sub.GetLanguage(),
			Forced:          sub.GetForced(),
			HearingImpaired: sub.GetHearingImpaired(),
			PictureBased:    picture,
			TextBased:       !picture && sub.GetCodec() != "",
			Label:           subtitleTrackLabel(sub.GetLanguage(), sub.GetCodec(), sub.GetForced(), sub.GetHearingImpaired()),
		})
	}
	if q := resp.GetQuality(); q != nil {
		out.Quality = &playbackAnalysisQuality{
			Label:      q.GetLabel(),
			Resolution: q.GetResolution(),
			Source:     q.GetSource(),
			CodecGroup: q.GetCodecGroup(),
			HDR:        q.GetHdr(),
		}
	}
	out.InfoLine = buildInfoLine(out.Video, out.Quality)
	return out
}

// handlePlaybackAnalysis exposes ffprobe stream/quality metadata for a library item.
// SPA: GET /api/playback/analysis?src=/stream/movies/…
func (s *server) handlePlaybackAnalysis(w http.ResponseWriter, r *http.Request) {
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
		writeJSONStatus(w, http.StatusOK, playbackAnalysisResponse{Src: src, Enabled: false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp := s.analyzeMediaBySrc(ctx, src)
	writeJSONStatus(w, http.StatusOK, fromAnalyzeResponse(src, resp))
}
