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

func (s *server) handleAddBook(w http.ResponseWriter, r *http.Request) {
	s.addLibraryPlusChild(w, r, s.booksHTTP, "/api/authors/", "/books", "books")
}

func (s *server) handleAddComicIssue(w http.ResponseWriter, r *http.Request) {
	s.addLibraryPlusChild(w, r, s.comicsHTTP, "/api/series/", "/issues", "comics")
}

func (s *server) handleAddBookAuthor(w http.ResponseWriter, r *http.Request) {
	s.addLibraryPlusParent(w, r, s.booksHTTP, "/api/authors", "name", "books")
}

func (s *server) handleAddComicSeries(w http.ResponseWriter, r *http.Request) {
	s.addLibraryPlusParent(w, r, s.comicsHTTP, "/api/series", "title", "comics")
}

func (s *server) handleAddAudiobook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "library.forbidden"})
		return
	}
	var body struct {
		Author    string `json:"author"`
		AuthorID  string `json:"author_id"`
		Title     string `json:"title"`
		Narrator  string `json:"narrator"`
		Year      int32  `json:"year"`
		Monitored *bool  `json:"monitored"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "library.invalid_json"})
			return
		}
	}
	title := strings.TrimSpace(body.Title)
	authorName := strings.TrimSpace(body.Author)
	authorID := strings.TrimSpace(body.AuthorID)
	if title == "" || (authorName == "" && authorID == "") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "author and title are required", "code": "library.title_required"})
		return
	}
	if s.audiobooksHTTP == nil || s.audiobooksHTTP.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "audiobooks module is not connected", "code": "library.unavailable"})
		return
	}
	monitored := true
	if body.Monitored != nil {
		monitored = *body.Monitored
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if authorID == "" {
		authors, err := s.listAudiobookAuthors(ctx, authorName)
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
			return
		}
		for _, a := range authors {
			if strings.EqualFold(strings.TrimSpace(a.Name), authorName) {
				authorID = a.ID
				break
			}
		}
		if authorID == "" {
			created, err := s.postLibraryPlusJSON(ctx, s.audiobooksHTTP, "/api/authors", map[string]any{"name": authorName, "monitored": true})
			if err != nil {
				writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
				return
			}
			authorID, _ = created["id"].(string)
		}
	}
	if authorID == "" {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "could not resolve author", "code": "library.add_failed"})
		return
	}
	item, err := s.postLibraryPlusJSON(ctx, s.audiobooksHTTP, "/api/authors/"+url.PathEscape(authorID)+"/audiobooks", map[string]any{
		"title": title, "narrator": strings.TrimSpace(body.Narrator), "year": body.Year, "monitored": monitored,
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	writeJSON(w, map[string]any{"added": true, "item": item})
}

func (s *server) listAudiobookAuthors(ctx context.Context, query string) ([]struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}, error) {
	u := *s.audiobooksHTTP
	u.Path = strings.TrimRight(s.audiobooksHTTP.Path, "/") + "/api/authors"
	if q := strings.TrimSpace(query); q != "" {
		u.RawQuery = "q=" + url.QueryEscape(q)
	}
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var authors []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &authors); err != nil {
		return nil, err
	}
	return authors, nil
}

func (s *server) postLibraryPlusJSON(ctx context.Context, upstream *url.URL, path string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil || item == nil {
		return map[string]any{}, nil
	}
	return item, nil
}

func (s *server) addLibraryPlusParent(w http.ResponseWriter, r *http.Request, upstream *url.URL, modulePath, field, code string) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "library.forbidden"})
		return
	}
	var body struct {
		Name      string `json:"name"`
		Title     string `json:"title"`
		Publisher string `json:"publisher"`
		Monitored *bool  `json:"monitored"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "library.invalid_json"})
			return
		}
	}
	label := strings.TrimSpace(body.Name)
	if field == "title" {
		label = strings.TrimSpace(body.Title)
		if label == "" {
			label = strings.TrimSpace(body.Name)
		}
	}
	if label == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": field + " is required", "code": "library.name_required"})
		return
	}
	if upstream == nil || upstream.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": code + " module is not connected", "code": "library.unavailable"})
		return
	}
	monitored := true
	if body.Monitored != nil {
		monitored = *body.Monitored
	}
	payload := map[string]any{field: label, "monitored": monitored}
	if pub := strings.TrimSpace(body.Publisher); pub != "" {
		payload["publisher"] = pub
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + modulePath
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(rawPayload))
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "library.add_failed"})
		return
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil || item == nil {
		item = map[string]any{field: label}
	}
	writeJSON(w, map[string]any{"added": true, "item": item})
}

func (s *server) addLibraryPlusChild(w http.ResponseWriter, r *http.Request, upstream *url.URL, modulePrefix, moduleSuffix, code string) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "library.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Title     string `json:"title"`
		Number    string `json:"number"`
		Year      int32  `json:"year"`
		Monitored *bool  `json:"monitored"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "library.invalid_json"})
			return
		}
	}
	title := strings.TrimSpace(body.Title)
	number := strings.TrimSpace(body.Number)
	if id == "" || (title == "" && number == "") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id and title are required", "code": "library.title_required"})
		return
	}
	if upstream == nil || upstream.String() == "" {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": code + " module is not connected", "code": "library.unavailable"})
		return
	}
	monitored := true
	if body.Monitored != nil {
		monitored = *body.Monitored
	}
	payload, err := json.Marshal(map[string]any{"title": title, "number": number, "year": body.Year, "monitored": monitored})
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + modulePrefix + url.PathEscape(id) + moduleSuffix
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := upstreamClient.Do(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "library.add_failed"})
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
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "library.add_failed"})
		return
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil || item == nil {
		item = map[string]any{"title": title}
	}
	writeJSON(w, map[string]any{"added": true, "item": item})
}
