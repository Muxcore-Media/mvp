package main

import (
	"testing"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
)

func TestBrowserDirectPlayCompatible(t *testing.T) {
	mkvH264Stereo := &ffprobev1.AnalyzeResponse{
		Container: "mkv",
		Video:     &ffprobev1.VideoStream{Codec: "h264"},
		Audio:     []*ffprobev1.AudioStream{{Codec: "aac", Channels: 2}},
	}
	if browserDirectPlayCompatible(mkvH264Stereo) {
		t.Fatal("mkv should require transcode")
	}

	mp4H264Surround := &ffprobev1.AnalyzeResponse{
		Container: "mp4",
		Video:     &ffprobev1.VideoStream{Codec: "h264"},
		Audio:     []*ffprobev1.AudioStream{{Codec: "aac", Channels: 6, ChannelLayout: "5.1"}},
	}
	if browserDirectPlayCompatible(mp4H264Surround) {
		t.Fatal("5.1 aac in mp4 should require transcode")
	}

	mp4H264Stereo := &ffprobev1.AnalyzeResponse{
		Container: "mp4",
		Video:     &ffprobev1.VideoStream{Codec: "h264"},
		Audio:     []*ffprobev1.AudioStream{{Codec: "aac", Channels: 2}},
	}
	if !browserDirectPlayCompatible(mp4H264Stereo) {
		t.Fatal("mp4 h264 stereo aac should direct play")
	}

	nineToFive := &ffprobev1.AnalyzeResponse{
		Container: "matroska",
		Video:     &ffprobev1.VideoStream{Codec: "h264"},
		Audio:     []*ffprobev1.AudioStream{{Codec: "aac", Channels: 2}},
	}
	if browserDirectPlayCompatible(nineToFive) {
		t.Fatal("matroska should require transcode")
	}

	ffprobeMKV := &ffprobev1.AnalyzeResponse{
		Container: "matroska,webm",
		Video:     &ffprobev1.VideoStream{Codec: "h264"},
		Audio:     []*ffprobev1.AudioStream{{Codec: "aac", Channels: 2}},
	}
	if browserDirectPlayCompatible(ffprobeMKV) {
		t.Fatal("matroska,webm should require transcode")
	}

	ffprobeStereoMP4 := &ffprobev1.AnalyzeResponse{
		Container: "mov,mp4,m4a,3gp,3g2,mj2",
		Video:     &ffprobev1.VideoStream{Codec: "h264"},
		Audio:     []*ffprobev1.AudioStream{{Codec: "aac", Channels: 2}},
	}
	if !browserDirectPlayCompatible(ffprobeStereoMP4) {
		t.Fatal("ffprobe mp4 family stereo aac should direct play")
	}

	ffprobeSurroundMP4 := &ffprobev1.AnalyzeResponse{
		Container: "mov,mp4,m4a,3gp,3g2,mj2",
		Video:     &ffprobev1.VideoStream{Codec: "h264"},
		Audio:     []*ffprobev1.AudioStream{{Codec: "aac", Channels: 6, ChannelLayout: "5.1"}},
	}
	if browserDirectPlayCompatible(ffprobeSurroundMP4) {
		t.Fatal("ffprobe mp4 family 5.1 aac should require transcode")
	}
}
