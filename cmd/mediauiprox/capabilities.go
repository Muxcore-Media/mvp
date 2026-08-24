package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
)

func (s *server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	movies := s.movies != nil
	tv := s.tv != nil
	music := s.libraryModuleLive(ctx, libraryKind{Upstream: s.musicHTTP, ListPath: "/api/artists"})
	books := s.libraryModuleLive(ctx, libraryKind{Upstream: s.booksHTTP, ListPath: "/api/authors"})
	comics := s.libraryModuleLive(ctx, libraryKind{Upstream: s.comicsHTTP, ListPath: "/api/series"})
	audiobooks := s.libraryModuleLive(ctx, libraryKind{Upstream: s.audiobooksHTTP, ListPath: "/api/audiobooks"})
	request := s.requestModuleLive(ctx)
	watchlist := s.listSyncModuleLive(ctx)

	homevideos := movies && s.companionLibraryConfigured("homevideos")
	musicvideos := movies && s.companionLibraryConfigured("musicvideos")
	transcoder := s.transcoderModuleLive(ctx)
	debrid := s.debridModuleLive(ctx)
	pol := loadPlaybackPolicy()

	writeJSON(w, map[string]any{
		"libraries": map[string]bool{
			"movies":      movies,
			"tv":          tv,
			"music":       music,
			"books":       books,
			"comics":      comics,
			"audiobooks":  audiobooks,
			"homevideos":  homevideos,
			"musicvideos": musicvideos,
		},
		"features": map[string]bool{
			"search":       request,
			"request":      request,
			"collections":  movies,
			"studios":      movies,
			"upcoming":     tv,
			"mixed":        movies && tv,
			"livetv":       s.livetv != nil,
			"quickconnect": s.quickconnect != nil,
			"playlists":    s.userdata != nil,
			"queue":        s.userdata != nil,
			"favorites":    s.userdata != nil,
			"watchlist":    watchlist,
			"transcoder":   transcoder,
			"debrid":       debrid,
		},
		"playback": map[string]any{
			"transcoder_available": transcoder,
			"transcoder_enabled":   pol.EnableTranscode,
			"prefer_direct_play":   pol.PreferDirectPlay,
		},
	})
}

func (s *server) listSyncModuleLive(ctx context.Context) bool {
	if s.listSync == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := s.listSync.GetItems(ctx, &listsyncv1.GetItemsRequest{Page: 1, PageSize: 1})
	return err == nil
}

func (s *server) debridModuleLive(ctx context.Context) bool {
	if s.debridHTTP == nil || strings.TrimSpace(s.debridHTTP.String()) == "" {
		return false
	}
	u := *s.debridHTTP
	u.Path = "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *server) transcoderModuleLive(ctx context.Context) bool {
	if s.transcoderHTTP == nil || strings.TrimSpace(s.transcoderHTTP.String()) == "" {
		return false
	}
	u := *s.transcoderHTTP
	u.Path = "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *server) companionLibraryConfigured(lib string) bool {
	if s.libraryPaths == nil {
		return false
	}
	return len(s.libraryPaths.prefixes(lib)) > 0
}

func (s *server) libraryModuleLive(ctx context.Context, kind libraryKind) bool {
	if kind.Upstream == nil || strings.TrimSpace(kind.Upstream.String()) == "" || kind.ListPath == "" {
		return false
	}
	u := *kind.Upstream
	u.Path = strings.TrimRight(kind.Upstream.Path, "/") + kind.ListPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *server) requestModuleLive(ctx context.Context) bool {
	if s.requestHTTP == nil || strings.TrimSpace(s.requestHTTP.String()) == "" {
		return false
	}
	u := *s.requestHTTP
	u.Path = strings.TrimRight(s.requestHTTP.Path, "/") + "/healthz"
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil); err == nil {
		if resp, err := upstreamClient.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
	}

	// request-media may not expose /healthz; probe search instead.
	u2 := *s.requestHTTP
	u2.Path = strings.TrimRight(s.requestHTTP.Path, "/") + "/api/search"
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, u2.String(), nil)
	if err != nil {
		return false
	}
	resp2, err := upstreamClient.Do(req2)
	if err != nil {
		return false
	}
	defer resp2.Body.Close()
	return resp2.StatusCode != http.StatusNotFound
}
