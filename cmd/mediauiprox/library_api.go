package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// libraryKind describes an optional non-video library proxied from module HTTP.
type libraryKind struct {
	Name       string // music | books | comics | audiobooks
	Upstream   *url.URL
	ListPath   string // path on module health HTTP
	ItemsKey   string // unused — arrays are wrapped as items
	CodePrefix string
}

func (s *server) registerLibraryRoutes(mux *http.ServeMux) {
	for _, kind := range []libraryKind{
		{Name: "music", Upstream: s.musicHTTP, ListPath: "/api/artists", CodePrefix: "music"},
		{Name: "books", Upstream: s.booksHTTP, ListPath: "/api/authors", CodePrefix: "books"},
		{Name: "comics", Upstream: s.comicsHTTP, ListPath: "/api/series", CodePrefix: "comics"},
		{Name: "audiobooks", Upstream: s.audiobooksHTTP, ListPath: "/api/audiobooks", CodePrefix: "audiobooks"},
	} {
		k := kind
		mux.HandleFunc("GET /api/"+k.Name, s.handleLibraryList(k))
	}
	mux.HandleFunc("POST /api/books", s.handleAddBookAuthor)
	mux.HandleFunc("POST /api/comics", s.handleAddComicSeries)
	mux.HandleFunc("PATCH /api/music/albums/{id}", s.handlePatchMusicAlbum)
	mux.HandleFunc("POST /api/music/albums/{id}/import", s.handleImportMusicAlbum)
	mux.HandleFunc("POST /api/music", s.handleAddMusicArtist)
	mux.HandleFunc("POST /api/music/{id}/albums", s.handleAddMusicAlbum)
	mux.HandleFunc("PATCH /api/music/{id}", s.handlePatchMusicArtist)
	mux.HandleFunc("DELETE /api/music/{id}", s.handleDeleteMusicArtist)
	mux.HandleFunc("POST /api/music/{id}/refresh", s.handleRefreshMusicArtist)
	mux.HandleFunc("GET /api/music/{id}/history", s.handleListMusicHistory)
	mux.HandleFunc("GET /api/music/{id}/artwork", s.handleListMusicArtwork)
	mux.HandleFunc("POST /api/music/{id}/artwork", s.handleReplaceMusicArtwork)
	mux.HandleFunc("GET /api/music/{id}/tags", s.handleGetMusicTags)
	mux.HandleFunc("PUT /api/music/{id}/tags", s.handleSetMusicTags)
	mux.HandleFunc("POST /api/music/{id}/tags", s.handleSetMusicTags)
	mux.HandleFunc("GET /api/music/{id}/files", s.handleListMusicTrackFiles)
	mux.HandleFunc("DELETE /api/music/{id}/files/{fileId}", s.handleDeleteMusicTrackFile)
	mux.HandleFunc("GET /api/music/", s.handleMusicArtistByID)
	mux.HandleFunc("GET /stream/music/", s.handleMusicStream)
	mux.HandleFunc("POST /api/books/works/{id}/import", s.handleImportBook)
	mux.HandleFunc("POST /api/books/{id}/books", s.handleAddBook)
	mux.HandleFunc("PATCH /api/books/works/{id}", s.handlePatchBook)
	mux.HandleFunc("DELETE /api/books/works/{id}", s.handleDeleteBook)
	mux.HandleFunc("PATCH /api/books/{id}", s.handlePatchBookAuthor)
	mux.HandleFunc("DELETE /api/books/{id}", s.handleDeleteBookAuthor)
	mux.HandleFunc("GET /api/books/{id}/history", s.handleListBookHistory)
	mux.HandleFunc("GET /api/books/{id}/artwork", s.handleListBookArtwork)
	mux.HandleFunc("POST /api/books/{id}/artwork", s.handleReplaceBookArtwork)
	mux.HandleFunc("GET /api/books/{id}/tags", s.handleGetBookTags)
	mux.HandleFunc("PUT /api/books/{id}/tags", s.handleSetBookTags)
	mux.HandleFunc("POST /api/books/{id}/tags", s.handleSetBookTags)
	mux.HandleFunc("GET /api/books/", s.handleBookAuthorByID)
	mux.HandleFunc("GET /stream/books/", s.handleBookStream)
	mux.HandleFunc("POST /api/comics/issues/{id}/import", s.handleImportComicIssue)
	mux.HandleFunc("POST /api/comics/{id}/issues", s.handleAddComicIssue)
	mux.HandleFunc("PATCH /api/comics/issues/{id}", s.handlePatchComicIssue)
	mux.HandleFunc("DELETE /api/comics/issues/{id}", s.handleDeleteComicIssue)
	mux.HandleFunc("PATCH /api/comics/{id}", s.handlePatchComicSeries)
	mux.HandleFunc("DELETE /api/comics/{id}", s.handleDeleteComicSeries)
	mux.HandleFunc("GET /api/comics/{id}/artwork", s.handleListComicArtwork)
	mux.HandleFunc("POST /api/comics/{id}/artwork", s.handleReplaceComicArtwork)
	mux.HandleFunc("GET /api/comics/{id}/history", s.handleListComicHistory)
	mux.HandleFunc("GET /api/comics/", s.handleComicSeriesByID)
	mux.HandleFunc("GET /stream/comics/", s.handleComicIssueStream)
	mux.HandleFunc("POST /api/audiobooks", s.handleAddAudiobook)
	mux.HandleFunc("POST /api/audiobooks/{id}/import", s.handleImportAudiobook)
	mux.HandleFunc("PATCH /api/audiobooks/{id}", s.handlePatchAudiobook)
	mux.HandleFunc("DELETE /api/audiobooks/{id}", s.handleDeleteAudiobook)
	mux.HandleFunc("GET /api/audiobooks/{id}/artwork", s.handleListAudiobookArtwork)
	mux.HandleFunc("POST /api/audiobooks/{id}/artwork", s.handleReplaceAudiobookArtwork)
	mux.HandleFunc("GET /api/audiobooks/{id}/history", s.handleListAudiobookHistory)
	mux.HandleFunc("GET /api/audiobooks/", s.handleAudiobookByID)
	mux.HandleFunc("GET /stream/audiobooks/", s.handleAudiobookStream)
}

func (s *server) handleMusicStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/stream/music/"), "/")
	if id == "" || s.musicHTTP == nil {
		http.NotFound(w, r)
		return
	}
	proxyUpstream(s.musicHTTP, "/api/tracks/"+url.PathEscape(id)+"/stream", w, r)
}

func (s *server) handleMusicArtistByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/music/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	kind := libraryKind{Name: "music", Upstream: s.musicHTTP, ListPath: "/api/artists", CodePrefix: "music"}
	if kind.Upstream == nil || kind.Upstream.String() == "" {
		writeLibrarySoft(w, kind, "module HTTP URL not configured")
		return
	}
	u := *kind.Upstream
	u.Path = strings.TrimRight(kind.Upstream.Path, "/") + "/api/artists/" + url.PathEscape(id)
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound {
		writeAPIError(w, http.StatusNotFound, "not found", kind.CodePrefix+".not_found")
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeLibrarySoft(w, kind, strings.TrimSpace(string(body)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) handleBookAuthorByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/books/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	kind := libraryKind{Name: "books", Upstream: s.booksHTTP, ListPath: "/api/authors", CodePrefix: "books"}
	if kind.Upstream == nil || kind.Upstream.String() == "" {
		writeLibrarySoft(w, kind, "module HTTP URL not configured")
		return
	}
	u := *kind.Upstream
	u.Path = strings.TrimRight(kind.Upstream.Path, "/") + "/api/authors/" + url.PathEscape(id)
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound {
		writeAPIError(w, http.StatusNotFound, "not found", kind.CodePrefix+".not_found")
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeLibrarySoft(w, kind, strings.TrimSpace(string(body)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) handleComicSeriesByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/comics/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	kind := libraryKind{Name: "comics", Upstream: s.comicsHTTP, ListPath: "/api/series", CodePrefix: "comics"}
	if kind.Upstream == nil || kind.Upstream.String() == "" {
		writeLibrarySoft(w, kind, "module HTTP URL not configured")
		return
	}
	u := *kind.Upstream
	u.Path = strings.TrimRight(kind.Upstream.Path, "/") + "/api/series/" + url.PathEscape(id)
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound {
		writeAPIError(w, http.StatusNotFound, "not found", kind.CodePrefix+".not_found")
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeLibrarySoft(w, kind, strings.TrimSpace(string(body)))
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	writeJSON(w, rewriteComicIssueStreamURLs(payload))
}

func (s *server) handleComicIssueStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/stream/comics/"), "/")
	if id == "" || s.comicsHTTP == nil {
		http.NotFound(w, r)
		return
	}
	proxyUpstream(s.comicsHTTP, "/api/issues/"+url.PathEscape(id)+"/stream", w, r)
}

func (s *server) handleBookStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/stream/books/"), "/")
	if id == "" || s.booksHTTP == nil {
		http.NotFound(w, r)
		return
	}
	proxyUpstream(s.booksHTTP, "/api/files/"+url.PathEscape(id)+"/stream", w, r)
}

func (s *server) handleLibraryList(kind libraryKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIMethodNotAllowed(w)
			return
		}
		if kind.Upstream == nil || kind.Upstream.String() == "" {
			writeLibrarySoft(w, kind, "module HTTP URL not configured")
			return
		}
		u := *kind.Upstream
		u.Path = strings.TrimRight(kind.Upstream.Path, "/") + kind.ListPath
		u.RawQuery = r.URL.RawQuery
		ctx := r.Context()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			writeLibrarySoft(w, kind, err.Error())
			return
		}
		resp, err := upstreamClient.Do(req)
		if err != nil {
			writeLibrarySoft(w, kind, err.Error())
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode != http.StatusOK {
			msg := strings.TrimSpace(string(body))
			if msg == "" {
				msg = resp.Status
			}
			writeLibrarySoft(w, kind, msg)
			return
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(body, &rows); err != nil {
			// Some modules may already return {items:[]}
			var wrapped struct {
				Items []json.RawMessage `json:"items"`
			}
			if err2 := json.Unmarshal(body, &wrapped); err2 != nil {
				writeJSONStatus(w, http.StatusBadGateway, map[string]any{
					"error":     "invalid upstream JSON",
					"code":      kind.CodePrefix + ".bad_payload",
					"available": false,
					"items":     []any{},
					"total":     0,
				})
				return
			}
			rows = wrapped.Items
		}
		items := make([]any, 0, len(rows))
		for _, row := range rows {
			var v any
			if err := json.Unmarshal(row, &v); err == nil {
				if kind.Name == "audiobooks" {
					v = rewriteAudiobookStreamURLs(v)
				}
				items = append(items, v)
			}
		}
		writeJSON(w, map[string]any{
			"items":     items,
			"total":     len(items),
			"page":      1,
			"page_size": len(items),
			"available": true,
			"library":   kind.Name,
		})
	}
}

func (s *server) handleAudiobookByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/audiobooks/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	kind := libraryKind{Name: "audiobooks", Upstream: s.audiobooksHTTP, ListPath: "/api/audiobooks", CodePrefix: "audiobooks"}
	if kind.Upstream == nil || kind.Upstream.String() == "" {
		writeLibrarySoft(w, kind, "module HTTP URL not configured")
		return
	}
	u := *kind.Upstream
	u.Path = strings.TrimRight(kind.Upstream.Path, "/") + "/api/audiobooks/" + url.PathEscape(id)
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeLibrarySoft(w, kind, err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound {
		writeAPIError(w, http.StatusNotFound, "not found", kind.CodePrefix+".not_found")
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeLibrarySoft(w, kind, strings.TrimSpace(string(body)))
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	if m, ok := payload.(map[string]any); ok {
		if ab, ok := m["audiobook"]; ok {
			m["audiobook"] = rewriteAudiobookStreamURLs(ab)
		}
		payload = m
	}
	writeJSON(w, payload)
}

func (s *server) handleAudiobookStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/stream/audiobooks/"), "/")
	if id == "" || s.audiobooksHTTP == nil {
		http.NotFound(w, r)
		return
	}
	proxyUpstream(s.audiobooksHTTP, "/api/files/"+url.PathEscape(id)+"/stream", w, r)
}

// proxyUpstream forwards the request to target's host with an exact path.
// reverseProxy+SetURL joins inbound and target paths, which 502s library streams.
func proxyUpstream(target *url.URL, path string, w http.ResponseWriter, r *http.Request) {
	if target == nil {
		http.NotFound(w, r)
		return
	}
	upstream := *target
	upstream.Path = ""
	upstream.RawPath = ""
	upstream.RawQuery = ""
	upstream.Fragment = ""
	proxy := httputil.NewSingleHostReverseProxy(&upstream)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = upstream.Scheme
	r2.URL.Host = upstream.Host
	r2.URL.Path = path
	r2.URL.RawPath = ""
	r2.RequestURI = ""
	proxy.ServeHTTP(w, r2)
}

func rewriteComicIssueStreamURLs(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	raw, ok := m["issues"].([]any)
	if !ok {
		return m
	}
	issues := make([]any, 0, len(raw))
	for _, row := range raw {
		im, ok := row.(map[string]any)
		if !ok {
			issues = append(issues, row)
			continue
		}
		id, _ := im["id"].(string)
		hasFile := im["has_file"] == true || strings.TrimSpace(asString(im["path"])) != ""
		if id != "" && hasFile {
			im["stream_url"] = "/stream/comics/" + url.PathEscape(id)
		}
		issues = append(issues, im)
	}
	m["issues"] = issues
	return m
}

func rewriteAudiobookStreamURLs(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	var files []any
	switch raw := m["files"].(type) {
	case []any:
		files = raw
	case []map[string]any:
		files = make([]any, len(raw))
		for i := range raw {
			files[i] = raw[i]
		}
	default:
		return m
	}
	first := ""
	for i, raw := range files {
		fm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := fm["id"].(string)
		if id == "" {
			continue
		}
		href := "/stream/audiobooks/" + url.PathEscape(id)
		fm["stream_url"] = href
		files[i] = fm
		if first == "" {
			first = href
		}
	}
	m["files"] = files
	if first != "" {
		m["stream_url"] = first
	}
	return m
}

func writeLibrarySoft(w http.ResponseWriter, kind libraryKind, errMsg string) {
	writeJSON(w, map[string]any{
		"items":       []any{},
		"total":       0,
		"page":        1,
		"page_size":   0,
		"available":   false,
		"coming_soon": true,
		"library":     kind.Name,
		"error":       errMsg,
		"code":        kind.CodePrefix + ".unavailable",
		"message":     "Coming soon — enable the library-plus spool tag (or start media-" + kind.Name + ") to populate this section.",
	})
}
