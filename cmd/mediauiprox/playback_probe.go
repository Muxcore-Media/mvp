package main

import (
	"context"
	"strings"
	"time"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
)

// analyzeMediaFile runs media-ffprobe Analyze for an on-disk library file.
func (s *server) analyzeMediaFile(ctx context.Context, absPath string) *ffprobev1.AnalyzeResponse {
	if s.ffprobe == nil || absPath == "" {
		return nil
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 12*time.Second)
	defer probeCancel()
	resp, err := s.ffprobe.Analyze(probeCtx, &ffprobev1.AnalyzeRequest{FilePath: absPath})
	probeCancel()
	if err != nil || resp == nil || resp.GetError() != "" {
		return nil
	}
	return resp
}

func (s *server) analyzeMediaByStream(ctx context.Context, kind, mediaID string) *ffprobev1.AnalyzeResponse {
	if kind == "" || mediaID == "" {
		return nil
	}
	absPath, _ := s.resolvePlaybackMediaFile(ctx, kind, mediaID)
	if absPath == "" {
		return nil
	}
	return s.analyzeMediaFile(ctx, absPath)
}

func (s *server) analyzeMediaBySrc(ctx context.Context, src string) *ffprobev1.AnalyzeResponse {
	kind, mediaID := parseStreamMediaID(src)
	if kind == "" {
		return nil
	}
	return s.analyzeMediaByStream(ctx, kind, mediaID)
}

func isPictureSubtitleCodec(codec string) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	if c == "" {
		return false
	}
	switch {
	case strings.Contains(c, "pgs"),
		strings.Contains(c, "dvd"),
		strings.Contains(c, "vobsub"),
		strings.Contains(c, "dvb_sub"),
		strings.Contains(c, "xsub"),
		strings.Contains(c, "hdmv"):
		return true
	default:
		return false
	}
}
