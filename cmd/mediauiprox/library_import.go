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

func (s *server) handleImportBook(w http.ResponseWriter, r *http.Request) {
	s.importLibraryPlusFile(w, r, s.booksHTTP, "/api/books/", "/import", "books")
}

func (s *server) handleImportComicIssue(w http.ResponseWriter, r *http.Request) {
	s.importLibraryPlusFile(w, r, s.comicsHTTP, "/api/issues/", "/import", "comics")
}

func (s *server) handleImportAudiobook(w http.ResponseWriter, r *http.Request) {
	s.importLibraryPlusFile(w, r, s.audiobooksHTTP, "/api/audiobooks/", "/import", "audiobooks")
}

func (s *server) importLibraryPlusFile(w http.ResponseWriter, r *http.Request, upstream *url.URL, modulePrefix, moduleSuffix, code string) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "import.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Path string `json:"path"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	path := strings.TrimSpace(body.Path)
	if id == "" || path == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and path are required", "code": "import.path_required"})
		return
	}
	if upstream == nil || upstream.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": code + " module is not connected", "code": "import.unavailable"})
		return
	}
	payload, err := json.Marshal(map[string]any{"path": path})
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "import.failed"})
		return
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + modulePrefix + url.PathEscape(id) + moduleSuffix
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "import.failed"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "import.failed"})
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
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "import.failed"})
		return
	}
	writeJSON(w, rewritePlusImportPayload(code, raw))
}

func rewritePlusImportPayload(kind string, raw []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{"imported": true}
	}
	id, _ := m["id"].(string)
	if id == "" {
		return m
	}
	switch kind {
	case "books":
		m["stream_url"] = "/stream/books/" + url.PathEscape(id)
	case "audiobooks":
		m["stream_url"] = "/stream/audiobooks/" + url.PathEscape(id)
	case "comics":
		if m["has_file"] == true || strings.TrimSpace(asString(m["path"])) != "" {
			m["stream_url"] = "/stream/comics/" + url.PathEscape(id)
		}
	}
	return m
}
