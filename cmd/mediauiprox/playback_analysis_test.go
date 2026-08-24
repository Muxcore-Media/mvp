package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
)

func TestBuildInfoLine(t *testing.T) {
	line := buildInfoLine(&playbackAnalysisVideo{
		ResolutionLabel: "2160p",
		HDRType:         "HDR10",
		Codec:           "hevc",
	}, &playbackAnalysisQuality{Label: "2160p Remux"})
	if line != "2160p Remux" {
		t.Fatalf("prefer quality label: %q", line)
	}
	line = buildInfoLine(&playbackAnalysisVideo{Height: 1080, HDR: true, Codec: "h264"}, nil)
	if line != "1080p · HDR · H264" {
		t.Fatalf("got %q", line)
	}
}

func TestIsPictureSubtitleCodec(t *testing.T) {
	if !isPictureSubtitleCodec("hdmv_pgs_subtitle") {
		t.Fatal("pgs")
	}
	if isPictureSubtitleCodec("subrip") {
		t.Fatal("subrip is text")
	}
}

func TestHandlePlaybackAnalysisMissingSrc(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/analysis", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackAnalysis(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandlePlaybackAnalysisDisabled(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/analysis?src=/stream/movies/m1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackAnalysis(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var out playbackAnalysisResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Enabled {
		t.Fatalf("expected disabled: %+v", out)
	}
}

func TestFromAnalyzeResponseMapsTracks(t *testing.T) {
	out := fromAnalyzeResponse("/stream/movies/m1", &ffprobev1.AnalyzeResponse{
		Container:         "mkv",
		DurationSeconds:   7200,
		OverallBitrate:    12_000_000,
		Video: &ffprobev1.VideoStream{
			Codec:           "hevc",
			Height:          2160,
			ResolutionLabel: "2160p",
			Hdr:             true,
			HdrType:         "HDR10",
		},
		Audio: []*ffprobev1.AudioStream{{
			Index:         1,
			Codec:         "aac",
			Language:      "eng",
			Channels:      6,
			ChannelLayout: "5.1",
		}},
		Subtitles: []*ffprobev1.SubtitleStream{{
			Index:    2,
			Codec:    "hdmv_pgs_subtitle",
			Language: "eng",
			Forced:   true,
		}},
		Quality: &ffprobev1.MediaQuality{Label: "2160p Remux"},
	})
	if !out.Enabled || out.Container != "mkv" || out.InfoLine != "2160p Remux" {
		t.Fatalf("out=%+v", out)
	}
	if out.Video == nil || out.Video.Codec != "hevc" {
		t.Fatalf("video=%v", out.Video)
	}
	if len(out.Audio) != 1 || out.Audio[0].Label == "" {
		t.Fatalf("audio=%v", out.Audio)
	}
	if len(out.Subtitles) != 1 || !out.Subtitles[0].PictureBased {
		t.Fatalf("subs=%v", out.Subtitles)
	}
}

func TestAudioTrackLabelAndChannelCount(t *testing.T) {
	if got := audioTrackLabel("eng", "5.1", 6, "aac"); got != "ENG · 5.1 · AAC" {
		t.Fatalf("got %q", got)
	}
	if formatChannelCount(2) != "Stereo" {
		t.Fatal("stereo")
	}
}
