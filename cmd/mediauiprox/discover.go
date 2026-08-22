package main

import (
	"net/http"
	"strconv"
	"strings"

	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
)

type discoverTrailer struct {
	Name       string `json:"name"`
	YoutubeKey string `json:"youtubeKey"`
	URL        string `json:"url"`
}

type discoverCastMember struct {
	ID          int32  `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character,omitempty"`
	ProfilePath string `json:"profilePath,omitempty"`
}

type discoverDetail struct {
	ID        int32                `json:"id"`
	Title     string               `json:"title"`
	Year      int32                `json:"year"`
	Overview  string               `json:"overview"`
	Tagline   string               `json:"tagline"`
	Genres    []string             `json:"genres"`
	Poster    string               `json:"poster"`
	Backdrop  string               `json:"backdrop"`
	VoteAvg   float64              `json:"voteAvg"`
	Runtime   int32                `json:"runtime,omitempty"`
	Status    string               `json:"status,omitempty"`
	MediaType string               `json:"mediaType"`
	Trailer   *discoverTrailer     `json:"trailer,omitempty"`
	Cast      []discoverCastMember `json:"cast,omitempty"`
}

func (s *server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.metadata == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "metadata module unavailable"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/discover/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	kind := strings.ToLower(parts[0])
	id, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch kind {
	case "movie", "movies":
		resp, err := s.metadata.GetMovieDetails(r.Context(), &metadatav1.GetMovieDetailsRequest{
			TmdbId:           int32(id),
			AppendToResponse: []string{"videos", "credits"},
		})
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, mapMovieDiscover(resp))
	case "tv", "series", "show", "shows":
		resp, err := s.metadata.GetTVDetails(r.Context(), &metadatav1.GetTVDetailsRequest{
			TmdbId:           int32(id),
			AppendToResponse: []string{"videos", "credits"},
		})
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, mapTVDiscover(resp))
	default:
		http.NotFound(w, r)
	}
}

func mapMovieDiscover(resp *metadatav1.GetMovieDetailsResponse) discoverDetail {
	title := strings.TrimSpace(resp.GetTitle())
	poster := resp.GetPosterPath()
	if poster == "" {
		poster = resp.GetPosterUrl()
	}
	backdrop := resp.GetBackdropPath()
	if backdrop == "" {
		backdrop = resp.GetBackdropUrl()
	}
	return discoverDetail{
		ID:        resp.GetId(),
		Title:     title,
		Year:      discoverYear(resp.GetReleaseDate()),
		Overview:  resp.GetOverview(),
		Tagline:   resp.GetTagline(),
		Genres:    discoverGenreNames(resp.GetGenres()),
		Poster:    poster,
		Backdrop:  backdrop,
		VoteAvg:   resp.GetVoteAverage(),
		Runtime:   resp.GetRuntime(),
		Status:    resp.GetStatus(),
		MediaType: "movie",
		Trailer:   pickDiscoverTrailer(resp.GetVideos()),
		Cast:      discoverCast(resp.GetCredits()),
	}
}

func mapTVDiscover(resp *metadatav1.GetTVDetailsResponse) discoverDetail {
	title := strings.TrimSpace(resp.GetName())
	poster := resp.GetPosterPath()
	if poster == "" {
		poster = resp.GetPosterUrl()
	}
	backdrop := resp.GetBackdropPath()
	if backdrop == "" {
		backdrop = resp.GetBackdropUrl()
	}
	return discoverDetail{
		ID:        resp.GetId(),
		Title:     title,
		Year:      discoverYear(resp.GetFirstAirDate()),
		Overview:  resp.GetOverview(),
		Tagline:   resp.GetTagline(),
		Genres:    discoverGenreNames(resp.GetGenres()),
		Poster:    poster,
		Backdrop:  backdrop,
		VoteAvg:   resp.GetVoteAverage(),
		Status:    resp.GetStatus(),
		MediaType: "tv",
		Trailer:   pickDiscoverTrailer(resp.GetVideos()),
		Cast:      discoverCast(resp.GetCredits()),
	}
}

func discoverGenreNames(genres []*metadatav1.Genre) []string {
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		if name := strings.TrimSpace(g.GetName()); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func discoverYear(date string) int32 {
	date = strings.TrimSpace(date)
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return int32(y)
}

func pickDiscoverTrailer(videos []*metadatav1.Video) *discoverTrailer {
	type rank struct {
		score int
		v     *metadatav1.Video
	}
	best := rank{score: -1}
	for _, v := range videos {
		if v == nil || strings.ToLower(strings.TrimSpace(v.GetSite())) != "youtube" {
			continue
		}
		key := strings.TrimSpace(v.GetKey())
		if key == "" {
			continue
		}
		score := 20
		switch strings.ToLower(strings.TrimSpace(v.GetType())) {
		case "trailer":
			score = 100
		case "teaser":
			score = 80
		case "clip":
			score = 40
		}
		if score > best.score {
			best = rank{score: score, v: v}
		}
	}
	if best.v == nil {
		return nil
	}
	key := strings.TrimSpace(best.v.GetKey())
	return &discoverTrailer{
		Name:       strings.TrimSpace(best.v.GetName()),
		YoutubeKey: key,
		URL:        "https://www.youtube.com/watch?v=" + key,
	}
}

func discoverCast(credits []*metadatav1.Credits) []discoverCastMember {
	if len(credits) == 0 {
		return nil
	}
	var cast []*metadatav1.CastMember
	for _, c := range credits {
		if c != nil && len(c.GetCast()) > 0 {
			cast = c.GetCast()
			break
		}
	}
	if len(cast) == 0 {
		return nil
	}
	limit := len(cast)
	if limit > 12 {
		limit = 12
	}
	out := make([]discoverCastMember, 0, limit)
	for _, m := range cast[:limit] {
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m.GetName())
		if name == "" {
			continue
		}
		out = append(out, discoverCastMember{
			ID:          m.GetId(),
			Name:        name,
			Character:   strings.TrimSpace(m.GetCharacter()),
			ProfilePath: strings.TrimSpace(m.GetProfilePath()),
		})
	}
	return out
}
