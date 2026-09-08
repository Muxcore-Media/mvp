package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/protobuf/proto"
)

const (
	migrateReadTimeout = 5 * time.Second
	migratePostTimeout = 2 * time.Minute
	migrateScanTimeout = 45 * time.Second
	migratePreviewCap  = 100
)

type movieImporterAdapter struct {
	client mgmntv1.MovieManagementServiceClient
}

func (a movieImporterAdapter) ImportMovie(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, migrateReadTimeout)
	resp, err := a.client.AddMovie(readCtx, &mgmntv1.AddMovieRequest{
		TmdbId:           int32(tmdbID),
		Title:            title,
		Year:             int32(year),
		QualityProfileId: qualityProfileID,
		RootFolderPath:   rootFolder,
	})
	cancel()
	if err != nil {
		return "", err
	}
	id := resp.GetMovieId()
	if id != "" {
		updCtx, updCancel := context.WithTimeout(ctx, migrateReadTimeout)
		_, _ = a.client.UpdateMovie(updCtx, &mgmntv1.UpdateMovieRequest{
			MovieId:   id,
			Monitored: proto.Bool(monitored),
		})
		updCancel()
	}
	return id, nil
}

type tvImporterAdapter struct {
	client tvmgmtv1.TvManagementServiceClient
}

func (a tvImporterAdapter) ImportSeries(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, migrateReadTimeout)
	resp, err := a.client.AddTVShow(readCtx, &tvmgmtv1.AddTVShowRequest{
		TmdbId:           int32(tmdbID),
		Name:             title,
		Year:             int32(year),
		QualityProfileId: qualityProfileID,
		RootFolderPath:   rootFolder,
	})
	cancel()
	if err != nil {
		return "", err
	}
	id := resp.GetSeriesId()
	if id != "" {
		updCtx, updCancel := context.WithTimeout(ctx, migrateReadTimeout)
		_, _ = a.client.UpdateTVShow(updCtx, &tvmgmtv1.UpdateTVShowRequest{
			SeriesId:  id,
			Monitored: proto.Bool(monitored),
		})
		updCancel()
	}
	return id, nil
}

type musicImporterAdapter struct {
	client musicv1.MusicManagementServiceClient
}

func (a musicImporterAdapter) ImportArtist(ctx context.Context, name, musicbrainzID, qualityProfileID, rootFolder string, monitored bool) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, migrateReadTimeout)
	resp, err := a.client.AddArtist(readCtx, &musicv1.AddArtistRequest{
		Name:             name,
		MusicbrainzId:    musicbrainzID,
		Monitored:        monitored,
		QualityProfileId: qualityProfileID,
		RootFolderPath:   rootFolder,
	})
	cancel()
	if err != nil {
		return "", err
	}
	if ar := resp.GetArtist(); ar != nil {
		return ar.GetId(), nil
	}
	return "", nil
}

func normalizeMigrateService(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "sonarr":
		return "sonarr"
	case "lidarr":
		return "lidarr"
	default:
		return "radarr"
	}
}

func publicArrItem(it arrItem) map[string]any {
	return map[string]any{
		"source":               it.Source,
		"arr_id":               it.ArrID,
		"title":                it.Title,
		"year":                 it.Year,
		"tmdb_id":              it.TMDBID,
		"tvdb_id":              it.TVDBID,
		"musicbrainz_id":       it.MusicBrainzID,
		"monitored":            it.Monitored,
		"quality_profile_name": it.QualityProfileName,
		"root_folder_path":     it.RootFolderPath,
	}
}

func publicArrResult(res arrResult, scanNote string) map[string]any {
	preview := make([]map[string]any, 0, min(len(res.Items), migratePreviewCap))
	for i, it := range res.Items {
		if i >= migratePreviewCap {
			break
		}
		preview = append(preview, publicArrItem(it))
	}
	return map[string]any{
		"dry_run":   res.DryRun,
		"fetched":   res.Fetched,
		"imported":  res.Imported,
		"skipped":   res.Skipped,
		"errors":    res.Errors,
		"preview":   preview,
		"scan_note": scanNote,
	}
}

func (s *server) resolveMigrateProfile(ctx context.Context, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || s.formats == nil {
		return ""
	}
	list, err := s.formats.ListProfiles(ctx, &formatsv1.ListProfilesRequest{})
	if err != nil {
		return ""
	}
	for _, p := range list.GetProfiles() {
		if p != nil && strings.EqualFold(p.GetName(), name) {
			return p.GetId()
		}
	}
	return ""
}

func (s *server) scanLibraryAfterMigrate(ctx context.Context) string {
	if s.scanner == nil {
		return "Library scan skipped — media-scanner is not connected."
	}
	scanCtx, cancel := context.WithTimeout(ctx, migrateScanTimeout)
	defer cancel()
	resp, err := s.scanner.ScanLibraryRoots(scanCtx, &scannerv1.ScanLibraryRootsRequest{})
	if err != nil {
		return "Library scan failed — " + err.Error()
	}
	return fmt.Sprintf("Library scan complete — found=%d imported=%d skipped=%d",
		resp.GetFilesFound(), resp.GetFilesImported(), resp.GetFilesSkipped())
}

func (s *server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "migrate.forbidden"})
		return
	}
	var body struct {
		Service   string `json:"service"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		DryRun    *bool  `json:"dry_run"`
		RemapFrom string `json:"remap_from"`
		RemapTo   string `json:"remap_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "migrate.invalid_json"})
		return
	}
	service := normalizeMigrateService(body.Service)
	if raw := strings.ToLower(strings.TrimSpace(body.Service)); raw != "" && raw != "radarr" && raw != "sonarr" && raw != "lidarr" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "service must be radarr, sonarr, or lidarr", "code": "migrate.invalid_service"})
		return
	}
	dryRun := true
	if body.DryRun != nil {
		dryRun = *body.DryRun
	}
	baseURL := strings.TrimSpace(body.BaseURL)
	apiKey := strings.TrimSpace(body.APIKey)
	if baseURL == "" || apiKey == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "base_url and api_key are required", "code": "migrate.credentials_required"})
		return
	}
	if !dryRun {
		switch service {
		case "radarr":
			if s.movies == nil {
				writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "movies module unavailable", "code": "migrate.movies_unavailable"})
				return
			}
		case "sonarr":
			if s.tv == nil {
				writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "tvshows module unavailable", "code": "migrate.tv_unavailable"})
				return
			}
		case "lidarr":
			if s.music == nil {
				writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "music module unavailable", "code": "migrate.music_unavailable"})
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), migratePostTimeout)
	defer cancel()
	cli := &arrClient{HTTP: s.arrHTTP}
	var items []arrItem
	var err error
	switch service {
	case "sonarr":
		items, err = cli.fetchSonarr(ctx, baseURL, apiKey)
	case "lidarr":
		items, err = cli.fetchLidarr(ctx, baseURL, apiKey)
	default:
		items, err = cli.fetchRadarr(ctx, baseURL, apiKey)
	}
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "migrate.fetch_failed"})
		return
	}
	items = remapArrItems(items, body.RemapFrom, body.RemapTo)

	var movies arrMovieImporter
	var tv arrTVImporter
	var music arrMusicImporter
	if s.movies != nil {
		movies = movieImporterAdapter{client: s.movies}
	}
	if s.tv != nil {
		tv = tvImporterAdapter{client: s.tv}
	}
	if s.music != nil {
		music = musicImporterAdapter{client: s.music}
	}
	res := runArrImport(ctx, items, dryRun, movies, tv, music, s.resolveMigrateProfile)
	scanNote := ""
	if !dryRun {
		scanNote = s.scanLibraryAfterMigrate(ctx)
	}
	writeJSON(w, publicArrResult(res, scanNote))
}
