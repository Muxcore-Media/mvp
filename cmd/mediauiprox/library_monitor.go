package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *server) handlePatchBookAuthor(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryMonitored(w, r, s.booksHTTP, "/api/authors/", "books")
}

func (s *server) handlePatchBook(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryMonitored(w, r, s.booksHTTP, "/api/books/", "books")
}

func (s *server) handlePatchComicSeries(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryMonitored(w, r, s.comicsHTTP, "/api/series/", "comics")
}

func (s *server) handlePatchComicIssue(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryMonitored(w, r, s.comicsHTTP, "/api/issues/", "comics")
}

func (s *server) handlePatchAudiobook(w http.ResponseWriter, r *http.Request) {
	s.patchLibraryMonitored(w, r, s.audiobooksHTTP, "/api/audiobooks/", "audiobooks")
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
	payload, err := json.Marshal(map[string]any{"monitored": *body.Monitored})
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + modulePath + url.PathEscape(id)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u.String(), bytes.NewReader(payload))
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "monitor.failed"})
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
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "monitor.failed"})
		return
	}
	s.syncWantedFromLibraryPatch(ctx, id, body)
	writeJSON(w, map[string]any{"id": id, "monitored": *body.Monitored})
}
