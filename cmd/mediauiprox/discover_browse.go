package main

import (
	"net/http"
	"strconv"
	"strings"

	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
)

type discoverBrowseResult struct {
	ID        int32   `json:"id"`
	Title     string  `json:"title"`
	Year      int32   `json:"year"`
	Overview  string  `json:"overview"`
	Poster    string  `json:"poster"`
	VoteAvg   float64 `json:"voteAvg"`
	MediaType string  `json:"mediaType"`
}

func (s *server) handleDiscoverBrowse(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) != 3 {
		return false
	}
	category := strings.ToLower(parts[0])
	mediaType, ok := discoverMediaType(parts[1])
	if !ok {
		http.NotFound(w, r)
		return true
	}
	switch category {
	case "trending":
		window := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("window")))
		if window == "" {
			window = "week"
		}
		s.handleDiscoverTrending(w, r, mediaType, window)
	case "popular":
		s.handleDiscoverPopular(w, r, mediaType)
	default:
		return false
	}
	return true
}

func (s *server) handleDiscoverTrending(w http.ResponseWriter, r *http.Request, mediaType metadatav1.TrendingMediaType, window string) {
	tw := metadatav1.TrendingTimeWindow_TRENDING_TIME_WINDOW_WEEK
	if window == "day" {
		tw = metadatav1.TrendingTimeWindow_TRENDING_TIME_WINDOW_DAY
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	resp, err := s.metadata.ListTrending(r.Context(), &metadatav1.ListTrendingRequest{
		MediaType:  mediaType,
		TimeWindow: tw,
		Page:       int32(page),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "discover.gateway_error", "results": []discoverBrowseResult{}})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"results": mapDiscoverBrowseResults(resp.GetResults())})
}

func (s *server) handleDiscoverPopular(w http.ResponseWriter, r *http.Request, mediaType metadatav1.TrendingMediaType) {
	popType := metadatav1.MediaType_MEDIA_TYPE_MOVIE
	if mediaType == metadatav1.TrendingMediaType_TRENDING_MEDIA_TYPE_TV {
		popType = metadatav1.MediaType_MEDIA_TYPE_TV
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	resp, err := s.metadata.ListPopular(r.Context(), &metadatav1.ListPopularRequest{
		Type: popType,
		Page: int32(page),
	})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "discover.gateway_error", "results": []discoverBrowseResult{}})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"results": mapDiscoverBrowseResults(resp.GetResults())})
}

func discoverMediaType(kind string) (metadatav1.TrendingMediaType, bool) {
	switch kind {
	case "movie", "movies":
		return metadatav1.TrendingMediaType_TRENDING_MEDIA_TYPE_MOVIE, true
	case "tv", "series", "show", "shows":
		return metadatav1.TrendingMediaType_TRENDING_MEDIA_TYPE_TV, true
	default:
		return metadatav1.TrendingMediaType_TRENDING_MEDIA_TYPE_UNSPECIFIED, false
	}
}

func mapDiscoverBrowseResults(results []*metadatav1.SearchResult) []discoverBrowseResult {
	out := make([]discoverBrowseResult, 0, len(results))
	for _, r := range results {
		if item, ok := mapDiscoverBrowseResult(r); ok {
			out = append(out, item)
		}
	}
	return out
}

func mapDiscoverBrowseResult(r *metadatav1.SearchResult) (discoverBrowseResult, bool) {
	if r == nil {
		return discoverBrowseResult{}, false
	}
	title := strings.TrimSpace(r.GetTitle())
	if title == "" {
		title = strings.TrimSpace(r.GetName())
	}
	if title == "" {
		return discoverBrowseResult{}, false
	}
	year := discoverYear(r.GetReleaseDate())
	if year == 0 {
		year = discoverYear(r.GetFirstAirDate())
	}
	kind := "movie"
	if r.GetMediaType() == metadatav1.MediaType_MEDIA_TYPE_TV {
		kind = "tv"
	}
	return discoverBrowseResult{
		ID:        r.GetId(),
		Title:     title,
		Year:      year,
		Overview:  r.GetOverview(),
		Poster:    r.GetPosterPath(),
		VoteAvg:   r.GetVoteAverage(),
		MediaType: kind,
	}, true
}
