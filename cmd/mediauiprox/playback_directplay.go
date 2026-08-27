package main

import (
	"strings"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
)

// browserDirectPlayCompatible reports whether a file can be played in a typical
// HTML5 <video> element without remuxing or transcoding.
func browserDirectPlayCompatible(resp *ffprobev1.AnalyzeResponse) bool {
	if resp == nil || resp.GetVideo() == nil {
		return false
	}
	container := strings.ToLower(strings.TrimSpace(resp.GetContainer()))
	if !browserDirectPlayContainer(container) {
		return false
	}
	if !browserDirectPlayVideo(resp.GetVideo().GetCodec(), container) {
		return false
	}
	if len(resp.GetAudio()) == 0 {
		return true
	}
	for _, a := range resp.GetAudio() {
		if a == nil {
			continue
		}
		if browserDirectPlayAudio(a.GetCodec(), a.GetChannels()) {
			return true
		}
	}
	return false
}

func browserDirectPlayContainer(container string) bool {
	// ffprobe format_name is comma-separated (e.g. "mov,mp4,m4a,3gp,3g2,mj2").
	if containerHasFormat(container, "matroska", "mkv") {
		return false
	}
	return containerHasFormat(container, "mp4", "mov", "m4v", "webm")
}

func browserDirectPlayVideo(codec, container string) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	switch c {
	case "h264", "avc", "avc1":
		return containerHasFormat(container, "mp4", "mov", "m4v")
	case "vp8", "vp9", "av1":
		return containerHasFormat(container, "webm")
	default:
		return false
	}
}

func containerFormats(container string) []string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(container)), ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containerHasFormat(container string, names ...string) bool {
	formats := containerFormats(container)
	if len(formats) == 0 {
		return false
	}
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[strings.ToLower(strings.TrimSpace(n))] = struct{}{}
	}
	for _, f := range formats {
		if _, ok := want[f]; ok {
			return true
		}
	}
	return false
}

func browserDirectPlayAudio(codec string, channels int32) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	switch c {
	case "aac", "mp3", "mp4a", "opus", "vorbis":
		return channels <= 2
	default:
		return false
	}
}
