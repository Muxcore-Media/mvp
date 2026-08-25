package main

import (
	"encoding/json"
	"io"
	"net/http"
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
	mux.HandleFunc("GET /api/music/", s.handleMusicArtistByID)
	mux.HandleFunc("GET /stream/music/", s.handleMusicStream)
	mux.HandleFunc("GET /api/books/", s.handleBookAuthorByID)
	mux.HandleFunc("GET /stream/books/", s.handleBookStream)
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
	u := *s.musicHTTP
	u.Path = strings.TrimRight(s.musicHTTP.Path, "/") + "/api/tracks/" + url.PathEscape(id) + "/stream"
	proxy := reverseProxy(&u)
	r2 := r.Clone(r.Context())
	r2.URL.Path = u.Path
	proxy.ServeHTTP(w, r2)
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
	u := *s.booksHTTP
	u.Path = strings.TrimRight(s.booksHTTP.Path, "/") + "/api/files/" + url.PathEscape(id) + "/stream"
	proxy := reverseProxy(&u)
	r2 := r.Clone(r.Context())
	r2.URL.Path = u.Path
	proxy.ServeHTTP(w, r2)
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
