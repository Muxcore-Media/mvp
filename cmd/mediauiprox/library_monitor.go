package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *server) handlePatchBookAuthor(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryPlus(w, r, s.booksHTTP, "/api/authors/", "books")
}

func (s *server) handlePatchBook(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryMonitored(w, r, s.booksHTTP, "/api/books/", "books")
}

func (s *server) handlePatchComicSeries(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryPlus(w, r, s.comicsHTTP, "/api/series/", "comics")
}

func (s *server) handlePatchComicIssue(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryMonitored(w, r, s.comicsHTTP, "/api/issues/", "comics")
}

func (s *server) handlePatchAudiobook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	body := readLibraryPatch(r)
	if id == "" || !body.hasLibraryFields() {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and monitored or root_folder_path are required", "code": "monitor.invalid"})
		return
	}
	if s.audiobooksHTTP == nil || s.audiobooksHTTP.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "audiobooks module is not connected", "code": "monitor.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	out := map[string]any{"id": id}
	if body.Monitored != nil {
		if err := s.patchLibraryPlusJSON(ctx, s.audiobooksHTTP, "/api/audiobooks/"+url.PathEscape(id), map[string]any{"monitored": *body.Monitored}); err != nil {
			writeLibraryPlusPatchError(w, err, "audiobooks")
			return
		}
		out["monitored"] = *body.Monitored
		s.syncWantedFromLibraryPatch(ctx, id, libraryPatchBody{Monitored: body.Monitored})
	}
	if body.RootFolderPath != nil {
		path := strings.TrimSpace(*body.RootFolderPath)
		authorID, err := s.audiobookAuthorID(ctx, id)
		if err != nil {
			writeLibraryPlusPatchError(w, err, "audiobooks")
			return
		}
		if err := s.patchLibraryPlusJSON(ctx, s.audiobooksHTTP, "/api/authors/"+url.PathEscape(authorID), map[string]any{"path": path}); err != nil {
			writeLibraryPlusPatchError(w, err, "audiobooks")
			return
		}
		out["root_folder_path"] = path
	}
	writeJSON(w, out)
}

func (s *server) patchLibraryMonitored(w http.ResponseWriter, r *http.Request, upstream *url.URL, modulePath string, code string) {
	if r.Method != http.MethodPatch {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	body := readLibraryPatch(r)
	if id == "" || body.Monitored == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and monitored are required", "code": "monitor.invalid"})
		return
	}
	if upstream == nil || upstream.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": code + " module is not connected", "code": "monitor.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := s.patchLibraryPlusJSON(ctx, upstream, modulePath+url.PathEscape(id), map[string]any{"monitored": *body.Monitored}); err != nil {
		writeLibraryPlusPatchError(w, err, code)
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	writeJSON(w, map[string]any{"id": id, "monitored": *body.Monitored})
}

func (s *server) patchLibraryPlus(w http.ResponseWriter, r *http.Request, upstream *url.URL, modulePath string, code string) {
	if r.Method != http.MethodPatch {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	body := readLibraryPatch(r)
	if id == "" || (body.Monitored == nil && (body.RootFolderPath == nil || strings.TrimSpace(*body.RootFolderPath) == "")) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and monitored or root_folder_path are required", "code": "monitor.invalid"})
		return
	}
	if upstream == nil || upstream.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": code + " module is not connected", "code": "monitor.unavailable"})
		return
	}
	payload := map[string]any{}
	if body.Monitored != nil {
		payload["monitored"] = *body.Monitored
	}
	if body.RootFolderPath != nil {
		payload["path"] = strings.TrimSpace(*body.RootFolderPath)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := s.patchLibraryPlusJSON(ctx, upstream, modulePath+url.PathEscape(id), payload); err != nil {
		writeLibraryPlusPatchError(w, err, code)
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	out := map[string]any{"id": id}
	if body.Monitored != nil {
		out["monitored"] = *body.Monitored
	}
	if body.RootFolderPath != nil {
		out["root_folder_path"] = strings.TrimSpace(*body.RootFolderPath)
	}
	writeJSON(w, out)
}

func (s *server) audiobookAuthorID(ctx context.Context, id string) (string, error) {
	raw, err := s.getLibraryPlusJSON(ctx, s.audiobooksHTTP, "/api/audiobooks/"+url.PathEscape(id))
	if err != nil {
		return "", err
	}
	if author, ok := raw["author"].(map[string]any); ok {
		if aid := strings.TrimSpace(fmt.Sprint(author["id"])); aid != "" && aid != "<nil>" {
			return aid, nil
		}
	}
	if ab, ok := raw["audiobook"].(map[string]any); ok {
		if aid := strings.TrimSpace(fmt.Sprint(ab["author_id"])); aid != "" && aid != "<nil>" {
			return aid, nil
		}
	}
	if aid := strings.TrimSpace(fmt.Sprint(raw["author_id"])); aid != "" && aid != "<nil>" {
		return aid, nil
	}
	return "", errPlaybackMonitorStatus(http.StatusBadGateway, "audiobook author missing")
}

func (s *server) getLibraryPlusJSON(ctx context.Context, upstream *url.URL, path string) (map[string]any, error) {
	if upstream == nil || upstream.String() == "" {
		return nil, errPlaybackMonitorStatus(http.StatusServiceUnavailable, "module is not connected")
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil, errPlaybackMonitorStatus(http.StatusNotFound, "not found")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil || item == nil {
		return nil, fmt.Errorf("invalid upstream JSON")
	}
	return item, nil
}

func (s *server) patchLibraryPlusJSON(ctx context.Context, upstream *url.URL, path string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return errPlaybackMonitorStatus(http.StatusNotFound, "not found")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func writeLibraryPlusPatchError(w http.ResponseWriter, err error, code string) {
	if pe, ok := err.(playbackMonitorStatusError); ok {
		if pe.code == http.StatusNotFound {
			writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "not found", "code": code + ".not_found"})
			return
		}
		writeJSONStatus(w, pe.code, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
}
