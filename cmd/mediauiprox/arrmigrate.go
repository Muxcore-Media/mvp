package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Household copy of admin-ui/arrmigrate (fetch + remap + import). Keep behavior in lockstep.

type arrItem struct {
	Source             string
	ArrID              int
	Title              string
	Year               int
	TMDBID             int
	TVDBID             int
	MusicBrainzID      string
	Monitored          bool
	QualityProfileName string
	RootFolderPath     string
}

type arrResult struct {
	DryRun   bool
	Fetched  int
	Imported int
	Skipped  int
	Errors   []string
	Items    []arrItem
}

type arrMovieImporter interface {
	ImportMovie(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (id string, err error)
}

type arrTVImporter interface {
	ImportSeries(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (id string, err error)
}

type arrMusicImporter interface {
	ImportArtist(ctx context.Context, name, musicbrainzID, qualityProfileID, rootFolder string, monitored bool) (id string, err error)
}

type arrClient struct {
	HTTP *http.Client
}

func (c *arrClient) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *arrClient) fetchRadarr(ctx context.Context, baseURL, apiKey string) ([]arrItem, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("radarr base URL and API key are required")
	}
	profiles, err := c.fetchQualityProfiles(ctx, baseURL+"/api/v3/qualityprofile", apiKey)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, baseURL+"/api/v3/movie", apiKey)
	if err != nil {
		return nil, err
	}
	return parseRadarrMovies(body, profiles)
}

func (c *arrClient) fetchSonarr(ctx context.Context, baseURL, apiKey string) ([]arrItem, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("sonarr base URL and API key are required")
	}
	profiles, err := c.fetchQualityProfiles(ctx, baseURL+"/api/v3/qualityprofile", apiKey)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, baseURL+"/api/v3/series", apiKey)
	if err != nil {
		return nil, err
	}
	return parseSonarrSeries(body, profiles)
}

func (c *arrClient) fetchLidarr(ctx context.Context, baseURL, apiKey string) ([]arrItem, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("lidarr base URL and API key are required")
	}
	profiles, err := c.fetchQualityProfiles(ctx, baseURL+"/api/v1/qualityprofile", apiKey)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, baseURL+"/api/v1/artist", apiKey)
	if err != nil {
		return nil, err
	}
	return parseLidarrArtists(body, profiles)
}

func (c *arrClient) fetchQualityProfiles(ctx context.Context, url, apiKey string) (map[int]string, error) {
	body, err := c.get(ctx, url, apiKey)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("parse quality profiles: %w", err)
	}
	out := make(map[int]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

func (c *arrClient) get(ctx context.Context, url, apiKey string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		snip := string(body)
		if len(snip) > 200 {
			snip = snip[:200]
		}
		return nil, fmt.Errorf("arr API %s: HTTP %d: %s", url, resp.StatusCode, snip)
	}
	return body, nil
}

func parseRadarrMovies(body []byte, profiles map[int]string) ([]arrItem, error) {
	var movies []struct {
		ID               int    `json:"id"`
		Title            string `json:"title"`
		Year             int    `json:"year"`
		TmdbID           int    `json:"tmdbId"`
		Monitored        bool   `json:"monitored"`
		QualityProfileID int    `json:"qualityProfileId"`
		RootFolderPath   string `json:"rootFolderPath"`
		Path             string `json:"path"`
	}
	if err := json.Unmarshal(body, &movies); err != nil {
		return nil, fmt.Errorf("parse radarr movies: %w", err)
	}
	out := make([]arrItem, 0, len(movies))
	for _, mv := range movies {
		root := strings.TrimSpace(mv.RootFolderPath)
		if root == "" {
			root = parentDir(mv.Path)
		}
		out = append(out, arrItem{
			Source:             "radarr",
			ArrID:              mv.ID,
			Title:              mv.Title,
			Year:               mv.Year,
			TMDBID:             mv.TmdbID,
			Monitored:          mv.Monitored,
			QualityProfileName: profiles[mv.QualityProfileID],
			RootFolderPath:     root,
		})
	}
	return out, nil
}

func parseSonarrSeries(body []byte, profiles map[int]string) ([]arrItem, error) {
	var series []struct {
		ID               int    `json:"id"`
		Title            string `json:"title"`
		Year             int    `json:"year"`
		TmdbID           int    `json:"tmdbId"`
		TvdbID           int    `json:"tvdbId"`
		Monitored        bool   `json:"monitored"`
		QualityProfileID int    `json:"qualityProfileId"`
		RootFolderPath   string `json:"rootFolderPath"`
		Path             string `json:"path"`
	}
	if err := json.Unmarshal(body, &series); err != nil {
		return nil, fmt.Errorf("parse sonarr series: %w", err)
	}
	out := make([]arrItem, 0, len(series))
	for _, s := range series {
		root := strings.TrimSpace(s.RootFolderPath)
		if root == "" {
			root = parentDir(s.Path)
		}
		out = append(out, arrItem{
			Source:             "sonarr",
			ArrID:              s.ID,
			Title:              s.Title,
			Year:               s.Year,
			TMDBID:             s.TmdbID,
			TVDBID:             s.TvdbID,
			Monitored:          s.Monitored,
			QualityProfileName: profiles[s.QualityProfileID],
			RootFolderPath:     root,
		})
	}
	return out, nil
}

func parseLidarrArtists(body []byte, profiles map[int]string) ([]arrItem, error) {
	var artists []struct {
		ID               int    `json:"id"`
		ArtistName       string `json:"artistName"`
		ForeignArtistID  string `json:"foreignArtistId"`
		Monitored        bool   `json:"monitored"`
		QualityProfileID int    `json:"qualityProfileId"`
		RootFolderPath   string `json:"rootFolderPath"`
		Path             string `json:"path"`
	}
	if err := json.Unmarshal(body, &artists); err != nil {
		return nil, fmt.Errorf("parse lidarr artists: %w", err)
	}
	out := make([]arrItem, 0, len(artists))
	for _, ar := range artists {
		root := strings.TrimSpace(ar.RootFolderPath)
		if root == "" {
			root = parentDir(ar.Path)
		}
		out = append(out, arrItem{
			Source:             "lidarr",
			ArrID:              ar.ID,
			Title:              ar.ArtistName,
			MusicBrainzID:      ar.ForeignArtistID,
			Monitored:          ar.Monitored,
			QualityProfileName: profiles[ar.QualityProfileID],
			RootFolderPath:     root,
		})
	}
	return out, nil
}

func parentDir(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return ""
	}
	return path[:i]
}

func remapArrRoot(path, fromPrefix, toRoot string) string {
	path = canonicalArrPath(path)
	fromPrefix = canonicalArrPath(fromPrefix)
	toRoot = canonicalArrPath(toRoot)
	if toRoot == "" {
		return path
	}
	if fromPrefix == "" {
		return toRoot
	}
	if path == fromPrefix {
		return toRoot
	}
	if strings.HasPrefix(path, fromPrefix+"/") {
		return toRoot + path[len(fromPrefix):]
	}
	return path
}

func remapArrItems(items []arrItem, fromPrefix, toRoot string) []arrItem {
	if canonicalArrPath(fromPrefix) == "" && canonicalArrPath(toRoot) == "" {
		return items
	}
	out := make([]arrItem, len(items))
	copy(out, items)
	for i := range out {
		out[i].RootFolderPath = remapArrRoot(out[i].RootFolderPath, fromPrefix, toRoot)
	}
	return out
}

func canonicalArrPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimRight(p, "/")
}

func runArrImport(ctx context.Context, items []arrItem, dryRun bool, movies arrMovieImporter, tv arrTVImporter, music arrMusicImporter, resolveProfile func(ctx context.Context, name string) string) arrResult {
	res := arrResult{DryRun: dryRun, Fetched: len(items), Items: items}
	if dryRun {
		return res
	}
	for _, it := range items {
		profileID := ""
		if resolveProfile != nil && it.QualityProfileName != "" {
			profileID = resolveProfile(ctx, it.QualityProfileName)
		}
		var err error
		switch it.Source {
		case "radarr":
			if it.TMDBID <= 0 {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("%s %q: missing tmdb id (tvdb=%d)", it.Source, it.Title, it.TVDBID))
				continue
			}
			if movies == nil {
				err = fmt.Errorf("movies module unavailable")
			} else {
				_, err = movies.ImportMovie(ctx, it.Title, it.Year, it.TMDBID, profileID, it.RootFolderPath, it.Monitored)
			}
		case "sonarr":
			if it.TMDBID <= 0 {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("%s %q: missing tmdb id (tvdb=%d)", it.Source, it.Title, it.TVDBID))
				continue
			}
			if tv == nil {
				err = fmt.Errorf("tvshows module unavailable")
			} else {
				_, err = tv.ImportSeries(ctx, it.Title, it.Year, it.TMDBID, profileID, it.RootFolderPath, it.Monitored)
			}
		case "lidarr":
			if strings.TrimSpace(it.MusicBrainzID) == "" {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("%s %q: missing musicbrainz id", it.Source, it.Title))
				continue
			}
			if music == nil {
				err = fmt.Errorf("music module unavailable")
			} else {
				_, err = music.ImportArtist(ctx, it.Title, it.MusicBrainzID, profileID, it.RootFolderPath, it.Monitored)
			}
		default:
			err = fmt.Errorf("unknown source %q", it.Source)
		}
		if err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("%s %q: %v", it.Source, it.Title, err))
			continue
		}
		res.Imported++
	}
	return res
}
