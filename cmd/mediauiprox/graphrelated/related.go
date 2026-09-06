// Package graphrelated implements GET /api/graph/related for the media-ui BFF.
//
// The handler is isolated from package main so it can be tested without the
// sibling-module replace paths in muxcore-mvp's go.mod.
package graphrelated

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	upstreamTimeout = 8 * time.Second
	maxLimit        = 50
	maxBodyBytes    = 1 << 20
)

// tmdbExternalID matches the TMDB keys media-ui-app sends and media-graph ingest stores.
var tmdbExternalID = regexp.MustCompile(`^tmdb:(movie|tv):[1-9][0-9]*$`)

// Handle returns GET /api/graph/related.
//
// Query: id=tmdb:{movie|tv}:{n} (or external_id=…), optional limit / rel / depth.
// Forwards to media-graph GET /api/graph/related?external_id=… (default :9731).
// Soft-fails with HTTP 200 {items:[], available:false} when graph is unset or unreachable.
func Handle(base *url.URL, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusMethodNotAllowed, "api.method_not_allowed", "method not allowed")
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			id = strings.TrimSpace(r.URL.Query().Get("external_id"))
		}
		if !tmdbExternalID.MatchString(id) {
			writeErr(w, http.StatusBadRequest, "graph.invalid_id", "id must be tmdb:movie:{n} or tmdb:tv:{n}")
			return
		}
		if base == nil || strings.TrimSpace(base.String()) == "" {
			writeJSON(w, http.StatusOK, relatedResponse{Items: []relatedItem{}, Available: false})
			return
		}

		q := url.Values{}
		q.Set("external_id", id)
		if lim := parseLimit(r.URL.Query().Get("limit")); lim > 0 {
			q.Set("limit", strconv.Itoa(lim))
		}
		if rel := strings.TrimSpace(r.URL.Query().Get("rel")); rel != "" {
			q.Set("rel", rel)
		}
		if depth := strings.TrimSpace(r.URL.Query().Get("depth")); depth != "" {
			q.Set("depth", depth)
		}

		u := *base
		u.Path = strings.TrimSuffix(u.Path, "/") + "/api/graph/related"
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
		if err != nil {
			writeJSON(w, http.StatusOK, relatedResponse{Items: []relatedItem{}, Available: false})
			return
		}
		req.Header.Set("Accept", "application/json")
		if tok := strings.TrimSpace(token); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}

		resp, err := upstreamClient().Do(req)
		if err != nil {
			writeJSON(w, http.StatusOK, relatedResponse{Items: []relatedItem{}, Available: false})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))

		// Title not in the graph store: rail can stay visible but empty.
		if resp.StatusCode == http.StatusNotFound {
			writeJSON(w, http.StatusOK, relatedResponse{Items: []relatedItem{}, Available: true})
			return
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			writeJSON(w, http.StatusOK, relatedResponse{Items: []relatedItem{}, Available: false})
			return
		}

		var raw graphRelatedResponse
		if err := json.Unmarshal(body, &raw); err != nil {
			writeJSON(w, http.StatusOK, relatedResponse{Items: []relatedItem{}, Available: false})
			return
		}
		items := mapRelated(raw)
		if lim := parseLimit(r.URL.Query().Get("limit")); lim > 0 && len(items) > lim {
			items = items[:lim]
		}
		writeJSON(w, http.StatusOK, relatedResponse{Items: items, Available: true})
	}
}

type relatedResponse struct {
	Items     []relatedItem `json:"items"`
	Available bool          `json:"available"`
}

type relatedItem struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Year          int     `json:"year,omitempty"`
	Overview      string  `json:"overview,omitempty"`
	Poster        string  `json:"poster,omitempty"`
	VoteAvg       float64 `json:"vote_avg,omitempty"`
	MediaType     string  `json:"media_type"`
	Relation      string  `json:"relation,omitempty"`
	ContentRating string  `json:"content_rating,omitempty"`
}

type graphRelatedResponse struct {
	Related []graphRelatedEdge `json:"related"`
	Items   []graphRelatedEdge `json:"items"`
}

type graphRelatedEdge struct {
	Node   *graphNode `json:"node"`
	Rel    string     `json:"rel"`
	Weight float64    `json:"weight"`
	// media-graph encodes store.Node / store.Related without json tags
	// (encoding/json uses the exported field name).
	NodeCap *graphNode `json:"Node"`
	RelCap  string     `json:"Rel"`
	WCap    float64    `json:"Weight"`
}

func (e graphRelatedEdge) node() *graphNode {
	if e.Node != nil {
		return e.Node
	}
	return e.NodeCap
}

func (e graphRelatedEdge) rel() string {
	if e.Rel != "" {
		return e.Rel
	}
	return e.RelCap
}

type graphNode struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	ExternalID string            `json:"external_id"`
	Attrs      map[string]string `json:"attrs"`
	IDCap      string            `json:"ID"`
	KindCap    string            `json:"Kind"`
	TitleCap   string            `json:"Title"`
	ExtCap     string            `json:"ExternalID"`
	AttrsCap   map[string]string `json:"Attrs"`
}

func (n *graphNode) id() string {
	if n == nil {
		return ""
	}
	if n.ID != "" {
		return n.ID
	}
	return n.IDCap
}

func (n *graphNode) kind() string {
	if n == nil {
		return ""
	}
	if n.Kind != "" {
		return n.Kind
	}
	return n.KindCap
}

func (n *graphNode) title() string {
	if n == nil {
		return ""
	}
	if n.Title != "" {
		return n.Title
	}
	return n.TitleCap
}

func (n *graphNode) externalID() string {
	if n == nil {
		return ""
	}
	if n.ExternalID != "" {
		return n.ExternalID
	}
	return n.ExtCap
}

func (n *graphNode) attrs() map[string]string {
	if n == nil {
		return nil
	}
	if n.Attrs != nil {
		return n.Attrs
	}
	return n.AttrsCap
}

func mapRelated(raw graphRelatedResponse) []relatedItem {
	edges := raw.Related
	if len(edges) == 0 {
		edges = raw.Items
	}
	out := make([]relatedItem, 0, len(edges))
	for _, e := range edges {
		n := e.node()
		if n == nil {
			continue
		}
		tmdbID := tmdbNumericID(n)
		if tmdbID == 0 {
			continue
		}
		mt := mediaTypeOf(n)
		if mt == "" {
			continue
		}
		item := relatedItem{
			ID:        tmdbID,
			Title:     n.title(),
			MediaType: mt,
			Relation:  e.rel(),
		}
		a := n.attrs()
		if y := atoi(firstAttr(a, "year", "release_year")); y > 0 {
			item.Year = y
		}
		item.Overview = firstAttr(a, "overview", "plot")
		item.Poster = firstAttr(a, "poster", "poster_path", "poster_url")
		if v, ok := parseFloat(firstAttr(a, "vote_avg", "vote_average", "rating")); ok {
			item.VoteAvg = v
		}
		item.ContentRating = firstAttr(a, "content_rating", "rating_mpaa", "certification")
		out = append(out, item)
	}
	return out
}

func mediaTypeOf(n *graphNode) string {
	kind := strings.ToLower(strings.TrimSpace(n.kind()))
	switch kind {
	case "movie":
		return "movie"
	case "series", "tv", "show":
		return "tv"
	}
	ext := strings.ToLower(n.externalID())
	switch {
	case strings.HasPrefix(ext, "tmdb:movie:"):
		return "movie"
	case strings.HasPrefix(ext, "tmdb:tv:"):
		return "tv"
	}
	return ""
}

func tmdbNumericID(n *graphNode) int64 {
	a := n.attrs()
	for _, key := range []string{"tmdb_id", "movie_id", "series_id"} {
		if id := atoi64(firstAttr(a, key)); id > 0 {
			return id
		}
	}
	ext := n.externalID()
	if i := strings.LastIndex(ext, ":"); i >= 0 && i+1 < len(ext) {
		if id := atoi64(ext[i+1:]); id > 0 {
			return id
		}
	}
	if id := atoi64(n.id()); id > 0 {
		return id
	}
	return 0
}

func parseLimit(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func firstAttr(m map[string]string, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

func upstreamClient() *http.Client {
	return &http.Client{
		Timeout: upstreamTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		},
	}
}
