package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

func (s *server) handleLibraryMissing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	page := int32(1)
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		page = int32(n)
	}
	pageSize := int32(50)
	if n, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && n > 0 && n <= 200 {
		pageSize = int32(n)
	}
	seriesID := strings.TrimSpace(r.URL.Query().Get("series_id"))
	artistID := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("artist_id"), r.URL.Query().Get("artistId")))
	wantMovies := kind == "" || kind == "all" || kind == "movie" || kind == "movies"
	wantTV := kind == "" || kind == "all" || kind == "tv" || kind == "show" || kind == "shows"
	wantMusic := kind == "" || kind == "all" || kind == "music" || kind == "album" || kind == "albums"
	wantBooks := kind == "" || kind == "all" || kind == "book" || kind == "books"
	wantComics := kind == "" || kind == "all" || kind == "comic" || kind == "comics"
	wantAudiobooks := kind == "" || kind == "all" || kind == "audiobook" || kind == "audiobooks"
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	movies := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	tv := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	music := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	books := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	comics := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	audiobooks := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	if wantMovies {
		movies = s.listMissingMovies(ctx, page, pageSize)
	}
	if wantTV {
		tv = s.listMissingEpisodes(ctx, page, pageSize, seriesID)
	}
	if wantMusic {
		music = s.listMissingAlbums(ctx, page, pageSize, artistID)
	}
	if wantBooks {
		books = s.listMissingBooks(ctx, page, pageSize)
	}
	if wantComics {
		comics = s.listMissingComics(ctx, page, pageSize)
	}
	if wantAudiobooks {
		audiobooks = s.listMissingAudiobooks(ctx, page, pageSize)
	}
	available := movies["available"] == true || tv["available"] == true || music["available"] == true ||
		books["available"] == true || comics["available"] == true || audiobooks["available"] == true
	writeJSON(w, map[string]any{
		"available":  available,
		"movies":     movies,
		"tv":         tv,
		"music":      music,
		"books":      books,
		"comics":     comics,
		"audiobooks": audiobooks,
	})
}

func (s *server) listMissingMovies(ctx context.Context, page, pageSize int32) map[string]any {
	out := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	if s.movies == nil {
		return out
	}
	resp, err := s.movies.ListMissing(ctx, &mgmntv1.ListMissingRequest{Page: page, PageSize: pageSize})
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	items := make([]map[string]any, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		id := strings.TrimSpace(it.GetMovieId())
		items = append(items, map[string]any{
			"kind":             "movie",
			"id":               id,
			"itemId":           id,
			"title":            it.GetTitle(),
			"year":             it.GetYear(),
			"tmdbId":           it.GetTmdbId(),
			"qualityProfileId": it.GetQualityProfileId(),
			"rootFolderPath":   it.GetRootFolderPath(),
			"href":             "/movies/" + id,
		})
	}
	out["available"] = true
	out["items"] = items
	out["total"] = resp.GetTotal()
	out["page"] = resp.GetPage()
	out["page_size"] = resp.GetPageSize()
	return out
}

func (s *server) listMissingEpisodes(ctx context.Context, page, pageSize int32, seriesID string) map[string]any {
	out := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	if s.tv == nil {
		return out
	}
	resp, err := s.tv.ListMissing(ctx, &tvmgmtv1.ListMissingRequest{
		Page:     page,
		PageSize: pageSize,
		SeriesId: seriesID,
	})
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	items := make([]map[string]any, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		series := strings.TrimSpace(it.GetSeriesId())
		items = append(items, map[string]any{
			"kind":             "tv",
			"id":               strings.TrimSpace(it.GetEpisodeId()),
			"itemId":           series,
			"seriesId":         series,
			"title":            it.GetTitle(),
			"year":             it.GetYear(),
			"tmdbId":           it.GetTmdbId(),
			"seasonNumber":     it.GetSeasonNumber(),
			"episodeNumber":    it.GetEpisodeNumber(),
			"absoluteNumber":   it.GetAbsoluteNumber(),
			"airDate":          it.GetAirDate(),
			"qualityProfileId": it.GetQualityProfileId(),
			"seriesType":       it.GetSeriesType(),
			"href":             "/tv/" + series,
		})
	}
	out["available"] = true
	out["items"] = items
	out["total"] = resp.GetTotal()
	out["page"] = resp.GetPage()
	out["page_size"] = resp.GetPageSize()
	return out
}

func (s *server) listMissingAlbums(ctx context.Context, page, pageSize int32, artistID string) map[string]any {
	out := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	if s.music == nil {
		return out
	}
	resp, err := s.music.ListMissing(ctx, &musicv1.ListMissingRequest{
		Page:     page,
		PageSize: pageSize,
		ArtistId: artistID,
	})
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	items := make([]map[string]any, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		albumID := strings.TrimSpace(it.GetAlbumId())
		artist := strings.TrimSpace(it.GetArtistId())
		items = append(items, map[string]any{
			"kind":             "music",
			"id":               albumID,
			"itemId":           albumID,
			"artistId":         artist,
			"artistName":       it.GetArtistName(),
			"title":            it.GetTitle(),
			"year":             it.GetYear(),
			"musicbrainzId":    it.GetMusicbrainzId(),
			"qualityProfileId": it.GetQualityProfileId(),
			"rootFolderPath":   it.GetRootFolderPath(),
			"href":             "/music/" + artist,
		})
	}
	out["available"] = true
	out["items"] = items
	out["total"] = resp.GetTotal()
	out["page"] = resp.GetPage()
	out["page_size"] = resp.GetPageSize()
	return out
}

func (s *server) listMissingBooks(ctx context.Context, page, pageSize int32) map[string]any {
	return s.listMissingLibraryHTTP(ctx, s.booksHTTP, page, pageSize, func(row map[string]any) map[string]any {
		bookID := firstNonEmpty(asString(row["book_id"]), asString(row["bookId"]), asString(row["id"]))
		authorID := firstNonEmpty(asString(row["author_id"]), asString(row["authorId"]))
		return map[string]any{
			"kind":       "book",
			"id":         bookID,
			"itemId":     bookID,
			"authorId":   authorID,
			"seriesId":   authorID,
			"artistName": firstNonEmpty(asString(row["author_name"]), asString(row["authorName"])),
			"title":      firstNonEmpty(asString(row["title"]), "Untitled"),
			"year":       asInt32(row["year"]),
			"href":       "/books/" + authorID,
		}
	})
}

func (s *server) listMissingComics(ctx context.Context, page, pageSize int32) map[string]any {
	return s.listMissingLibraryHTTP(ctx, s.comicsHTTP, page, pageSize, func(row map[string]any) map[string]any {
		issueID := firstNonEmpty(asString(row["issue_id"]), asString(row["issueId"]), asString(row["id"]))
		seriesID := firstNonEmpty(asString(row["series_id"]), asString(row["seriesId"]))
		return map[string]any{
			"kind":        "comic",
			"id":          issueID,
			"itemId":      issueID,
			"seriesId":    seriesID,
			"artistName":  firstNonEmpty(asString(row["series_name"]), asString(row["seriesName"])),
			"title":       firstNonEmpty(asString(row["title"]), "Untitled"),
			"issueNumber": firstNonEmpty(asString(row["number"]), asString(row["issue_number"])),
			"year":        asInt32(row["year"]),
			"href":        "/comics/" + seriesID,
		}
	})
}

func (s *server) listMissingAudiobooks(ctx context.Context, page, pageSize int32) map[string]any {
	return s.listMissingLibraryHTTP(ctx, s.audiobooksHTTP, page, pageSize, func(row map[string]any) map[string]any {
		id := firstNonEmpty(asString(row["audiobook_id"]), asString(row["audiobookId"]), asString(row["id"]))
		authorID := firstNonEmpty(asString(row["author_id"]), asString(row["authorId"]))
		return map[string]any{
			"kind":       "audiobook",
			"id":         id,
			"itemId":     id,
			"authorId":   authorID,
			"seriesId":   authorID,
			"artistName": firstNonEmpty(asString(row["author_name"]), asString(row["authorName"])),
			"title":      firstNonEmpty(asString(row["title"]), "Untitled"),
			"year":       asInt32(row["year"]),
			"href":       "/audiobooks/" + id,
		}
	})
}

func (s *server) listMissingLibraryHTTP(ctx context.Context, upstream *url.URL, page, pageSize int32, mapItem func(map[string]any) map[string]any) map[string]any {
	out := map[string]any{"available": false, "items": []any{}, "total": 0, "page": page, "page_size": pageSize}
	if upstream == nil || upstream.String() == "" {
		return out
	}
	u := *upstream
	u.Path = strings.TrimRight(upstream.Path, "/") + "/api/missing"
	q := url.Values{}
	q.Set("page", strconv.Itoa(int(page)))
	q.Set("page_size", strconv.Itoa(int(pageSize)))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		out["error"] = msg
		return out
	}
	var parsed struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
		Page  int              `json:"page"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		out["error"] = err.Error()
		return out
	}
	items := make([]map[string]any, 0, len(parsed.Items))
	for _, row := range parsed.Items {
		if row == nil {
			continue
		}
		items = append(items, mapItem(row))
	}
	out["available"] = true
	out["items"] = items
	out["total"] = parsed.Total
	if parsed.Page > 0 {
		out["page"] = parsed.Page
	}
	return out
}

func asInt32(v any) int32 {
	switch t := v.(type) {
	case float64:
		return int32(t)
	case json.Number:
		n, _ := t.Int64()
		return int32(n)
	case int:
		return int32(t)
	case int32:
		return t
	case int64:
		return int32(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return int32(n)
	default:
		return 0
	}
}
