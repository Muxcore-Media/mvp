package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

func queryDeleteFiles(r *http.Request) bool {
	raw := strings.TrimSpace(r.URL.Query().Get("delete_files"))
	return raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes")
}

func (s *server) handleDeleteMovie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies module is not connected", "code": "library.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.movies.RemoveMovie(ctx, &mgmntv1.RemoveMovieRequest{MovieId: id, DeleteFiles: deleteFiles}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "delete_files": deleteFiles})
}

func (s *server) handleDeleteTV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv module is not connected", "code": "library.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.tv.RemoveTVShow(ctx, &tvmgmtv1.RemoveTVShowRequest{SeriesId: id, DeleteFiles: deleteFiles}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "delete_files": deleteFiles})
}

func (s *server) handleDeleteMovieFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies module is not connected", "code": "library.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	listed, err := s.movies.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: id})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
		return
	}
	removed := 0
	for _, f := range listed.GetFiles() {
		fileID := strings.TrimSpace(f.GetId())
		if fileID == "" {
			continue
		}
		if _, err := s.movies.RemoveFile(ctx, &mgmntv1.RemoveFileRequest{FileId: fileID, DeleteFiles: deleteFiles}); err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
			return
		}
		removed++
	}
	writeJSON(w, map[string]any{"removed": removed > 0, "delete_files": deleteFiles, "files": removed})
}

func (s *server) handleDeleteEpisodeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv module is not connected", "code": "library.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.tv.RemoveEpisodeFile(ctx, &tvmgmtv1.RemoveEpisodeFileRequest{EpisodeId: id, DeleteFiles: deleteFiles}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "delete_files": deleteFiles})
}

func (s *server) handleRefreshMovie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.movies == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies module is not connected", "code": "library.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := s.movies.RefreshMetadata(ctx, &mgmntv1.RefreshMetadataRequest{MovieId: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.refresh_failed"})
		return
	}
	writeJSON(w, map[string]any{"refreshed": true})
}

func (s *server) handleRefreshTV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.tv == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tv module is not connected", "code": "library.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := s.tv.RefreshMetadata(ctx, &tvmgmtv1.RefreshMetadataRequest{SeriesId: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.refresh_failed"})
		return
	}
	writeJSON(w, map[string]any{"refreshed": true})
}

func (s *server) handleRefreshMusicArtist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "library.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := s.music.RefreshMetadata(ctx, &musicv1.RefreshMetadataRequest{ArtistId: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.refresh_failed"})
		return
	}
	writeJSON(w, map[string]any{"refreshed": true})
}

func (s *server) handleDeleteMusicArtist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if s.music == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module is not connected", "code": "library.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.music.RemoveArtist(ctx, &musicv1.RemoveArtistRequest{Id: id, DeleteFiles: deleteFiles}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "delete_files": deleteFiles})
}

func (s *server) handleDeleteBookAuthor(w http.ResponseWriter, r *http.Request) {
	s.deleteLibraryPlus(w, r, s.booksHTTP, "/api/authors/", "books")
}

func (s *server) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	s.deleteLibraryPlus(w, r, s.booksHTTP, "/api/books/", "books")
}

func (s *server) handleDeleteComicSeries(w http.ResponseWriter, r *http.Request) {
	s.deleteLibraryPlus(w, r, s.comicsHTTP, "/api/series/", "comics")
}

func (s *server) handleDeleteComicIssue(w http.ResponseWriter, r *http.Request) {
	s.deleteLibraryPlus(w, r, s.comicsHTTP, "/api/issues/", "comics")
}

func (s *server) handleDeleteAudiobook(w http.ResponseWriter, r *http.Request) {
	s.deleteLibraryPlus(w, r, s.audiobooksHTTP, "/api/audiobooks/", "audiobooks")
}

func (s *server) deleteLibraryPlus(w http.ResponseWriter, r *http.Request, upstream *url.URL, modulePath string, code string) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "library.id_required"})
		return
	}
	if upstream == nil || upstream.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": code + " module is not connected", "code": "library.unavailable"})
		return
	}
	deleteFiles := queryDeleteFiles(r)
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + modulePath + url.PathEscape(id)
	if deleteFiles {
		q := u.Query()
		q.Set("delete_files", "1")
		u.RawQuery = q.Encode()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
		return
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.remove_failed"})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "not found", "code": code + ".not_found"})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "library.remove_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "delete_files": deleteFiles})
}
