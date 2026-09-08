package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

type renamePlanItem struct {
	FileID       string `json:"file_id,omitempty"`
	EpisodeID    string `json:"episode_id,omitempty"`
	Title        string `json:"title,omitempty"`
	CurrentPath  string `json:"current_path"`
	NewPath      string `json:"new_path"`
	NewFilename  string `json:"new_filename"`
	Changed      bool   `json:"changed"`
	Quality      string `json:"quality,omitempty"`
	mediaType    string
	movieID      string
	original     string
	year         int32
	season       int32
	episode      int32
	absolute     int32
	episodeTitle string
	airDate      string
	imdbID       string
	tmdbID       string
	sizeBytes    int64
	container    string
}

func (s *server) handleRenamePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.rename == nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}})
		return
	}
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	items, err := s.buildRenamePlan(ctx, strings.TrimSpace(q.Get("movie_id")), strings.TrimSpace(q.Get("tv_id")), strings.TrimSpace(q.Get("episode_id")))
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}})
		return
	}
	writeJSON(w, map[string]any{"available": true, "items": renamePlanPublic(items)})
}

func (s *server) handleRenameExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.rename == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "rename unavailable", "code": "rename.unavailable"})
		return
	}
	var body struct {
		MovieID   string `json:"movie_id"`
		TVID      string `json:"tv_id"`
		EpisodeID string `json:"episode_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "code": "rename.invalid_json"})
		return
	}
	if strings.TrimSpace(body.MovieID) == "" && strings.TrimSpace(body.TVID) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "movie_id or tv_id required", "code": "rename.target_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	items, err := s.buildRenamePlan(ctx, strings.TrimSpace(body.MovieID), strings.TrimSpace(body.TVID), strings.TrimSpace(body.EpisodeID))
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "rename.preview_failed"})
		return
	}
	renamed := 0
	errors := 0
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := map[string]any{
			"file_id":      item.FileID,
			"episode_id":   item.EpisodeID,
			"title":        item.Title,
			"current_path": item.CurrentPath,
			"new_path":     item.NewPath,
			"new_filename": item.NewFilename,
			"changed":      item.Changed,
			"renamed":      false,
		}
		if !item.Changed {
			out = append(out, row)
			continue
		}
		resp, execErr := s.rename.Execute(ctx, &renamev1.ExecuteRequest{
			FilePath:       item.CurrentPath,
			MediaType:      item.mediaType,
			Title:          item.Title,
			Year:           item.year,
			SeasonNumber:   item.season,
			EpisodeNumber:  item.episode,
			AbsoluteNumber: item.absolute,
			Quality:        item.Quality,
			OriginalTitle:  item.original,
			ImdbId:         item.imdbID,
			TmdbId:         item.tmdbID,
			EpisodeTitle:   item.episodeTitle,
			AirDate:        item.airDate,
		})
		if execErr != nil || resp == nil || !resp.GetSuccess() {
			errors++
			msg := "rename failed"
			if execErr != nil {
				msg = execErr.Error()
			} else if resp != nil && resp.GetError() != "" {
				msg = resp.GetError()
			}
			row["error"] = msg
			out = append(out, row)
			continue
		}
		newPath := resp.GetNewPath()
		if newPath == "" {
			newPath = item.NewPath
		}
		if err := s.recordRenamedFile(ctx, item, newPath); err != nil {
			errors++
			row["error"] = err.Error()
			out = append(out, row)
			continue
		}
		renamed++
		row["renamed"] = true
		row["changed"] = false
		row["current_path"] = newPath
		row["new_path"] = newPath
		row["new_filename"] = filepath.Base(newPath)
		out = append(out, row)
	}
	writeJSON(w, map[string]any{"available": true, "renamed": renamed, "errors": errors, "items": out})
}

func renamePlanPublic(items []renamePlanItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"file_id":      item.FileID,
			"episode_id":   item.EpisodeID,
			"title":        item.Title,
			"current_path": item.CurrentPath,
			"new_path":     item.NewPath,
			"new_filename": item.NewFilename,
			"changed":      item.Changed,
			"quality":      item.Quality,
		})
	}
	return out
}

func (s *server) buildRenamePlan(ctx context.Context, movieID, tvID, episodeID string) ([]renamePlanItem, error) {
	if movieID != "" {
		return s.buildMovieRenamePlan(ctx, movieID)
	}
	if tvID != "" {
		return s.buildTVRenamePlan(ctx, tvID, episodeID)
	}
	if episodeID != "" {
		return nil, errRenameTargetRequired
	}
	return nil, errRenameTargetRequired
}

var errRenameTargetRequired = errString("movie_id or tv_id required")

type errString string

func (e errString) Error() string { return string(e) }

func (s *server) buildMovieRenamePlan(ctx context.Context, movieID string) ([]renamePlanItem, error) {
	if s.movies == nil {
		return nil, errString("movies unavailable")
	}
	got, err := s.movies.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: movieID})
	if err != nil {
		return nil, err
	}
	movie := got.GetMovie()
	if movie == nil {
		return nil, errString("movie not found")
	}
	listed, err := s.movies.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: movieID})
	if err != nil {
		return nil, err
	}
	items := make([]renamePlanItem, 0, len(listed.GetFiles()))
	for _, file := range listed.GetFiles() {
		if file == nil || file.GetFilePath() == "" {
			continue
		}
		current := resolveAbsMediaPath(movie.GetRootFolderPath(), file.GetFilePath())
		if current == "" {
			current = file.GetFilePath()
		}
		quality := strings.TrimSpace(file.GetQuality())
		preview, err := s.rename.Preview(ctx, &renamev1.PreviewRequest{
			FilePath:      current,
			MediaType:     "movie",
			Title:         movie.GetTitle(),
			Year:          movie.GetYear(),
			Quality:       quality,
			Extension:     filepath.Ext(current),
			OriginalTitle: movie.GetOriginalTitle(),
			ImdbId:        movie.GetImdbId(),
			TmdbId:        strconv.Itoa(int(movie.GetTmdbId())),
		})
		if err != nil {
			return nil, err
		}
		newPath := preview.GetNewPath()
		items = append(items, renamePlanItem{
			FileID:      file.GetId(),
			Title:       movie.GetTitle(),
			CurrentPath: current,
			NewPath:     newPath,
			NewFilename: firstNonEmpty(preview.GetNewFilename(), filepath.Base(newPath)),
			Changed:     !sameRenamePath(current, newPath),
			Quality:     quality,
			mediaType:   "movie",
			movieID:     movieID,
			original:    movie.GetOriginalTitle(),
			year:        movie.GetYear(),
			imdbID:      movie.GetImdbId(),
			tmdbID:      strconv.Itoa(int(movie.GetTmdbId())),
			sizeBytes:   file.GetSizeBytes(),
			container:   file.GetContainer(),
		})
	}
	return items, nil
}

func (s *server) buildTVRenamePlan(ctx context.Context, tvID, episodeID string) ([]renamePlanItem, error) {
	if s.tv == nil {
		return nil, errString("tv unavailable")
	}
	got, err := s.tv.GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: tvID})
	if err != nil {
		return nil, err
	}
	show := got.GetSeries()
	if show == nil {
		return nil, errString("show not found")
	}
	items := make([]renamePlanItem, 0)
	for _, season := range show.GetSeasons() {
		if season == nil {
			continue
		}
		for _, ep := range season.GetEpisodes() {
			if ep == nil || !ep.GetHasFile() || ep.GetId() == "" || ep.GetId() == "_list" {
				continue
			}
			if episodeID != "" && ep.GetId() != episodeID {
				continue
			}
			filePath, fileID, quality := s.lookupEpisodeRenameFile(ctx, ep.GetId())
			if filePath == "" {
				continue
			}
			preview, err := s.rename.Preview(ctx, &renamev1.PreviewRequest{
				FilePath:       filePath,
				MediaType:      "tv",
				Title:          show.GetName(),
				Year:           show.GetYear(),
				SeasonNumber:   ep.GetSeasonNumber(),
				EpisodeNumber:  ep.GetEpisodeNumber(),
				AbsoluteNumber: ep.GetAbsoluteNumber(),
				Quality:        quality,
				Extension:      filepath.Ext(filePath),
				EpisodeTitle:   ep.GetName(),
				AirDate:        ep.GetAirDate(),
				TmdbId:         strconv.Itoa(int(show.GetTmdbId())),
			})
			if err != nil {
				return nil, err
			}
			newPath := preview.GetNewPath()
			items = append(items, renamePlanItem{
				FileID:       fileID,
				EpisodeID:    ep.GetId(),
				Title:        show.GetName(),
				CurrentPath:  filePath,
				NewPath:      newPath,
				NewFilename:  firstNonEmpty(preview.GetNewFilename(), filepath.Base(newPath)),
				Changed:      !sameRenamePath(filePath, newPath),
				Quality:      quality,
				mediaType:    "tv",
				original:     show.GetName(),
				year:         show.GetYear(),
				season:       ep.GetSeasonNumber(),
				episode:      ep.GetEpisodeNumber(),
				absolute:     ep.GetAbsoluteNumber(),
				episodeTitle: ep.GetName(),
				airDate:      ep.GetAirDate(),
				tmdbID:       strconv.Itoa(int(show.GetTmdbId())),
			})
		}
	}
	return items, nil
}

func (s *server) lookupEpisodeRenameFile(ctx context.Context, episodeID string) (filePath, fileID, quality string) {
	if episodeID == "" || s.tvHTTP == nil {
		return "", "", ""
	}
	u := *s.tvHTTP
	u.Path = strings.TrimRight(s.tvHTTP.Path, "/") + "/api/episodes/" + url.PathEscape(episodeID) + "/file"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", ""
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return "", "", ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", ""
	}
	var body struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
		Quality  string `json:"quality"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return "", "", ""
	}
	return body.FilePath, body.FileID, body.Quality
}

func (s *server) recordRenamedFile(ctx context.Context, item renamePlanItem, newPath string) error {
	if item.mediaType == "tv" {
		if s.tv == nil || item.EpisodeID == "" {
			return nil
		}
		if item.FileID != "" {
			if _, err := s.tv.RemoveEpisodeFile(ctx, &tvmgmtv1.RemoveEpisodeFileRequest{
				FileId: item.FileID, EpisodeId: item.EpisodeID, DeleteFiles: false,
			}); err != nil {
				return err
			}
		} else {
			if _, err := s.tv.RemoveEpisodeFile(ctx, &tvmgmtv1.RemoveEpisodeFileRequest{
				EpisodeId: item.EpisodeID, DeleteFiles: false,
			}); err != nil {
				return err
			}
		}
		_, err := s.tv.AddEpisodeFile(ctx, &tvmgmtv1.AddEpisodeFileRequest{
			EpisodeId: item.EpisodeID,
			FilePath:  newPath,
			Quality:   item.Quality,
		})
		return err
	}
	if s.movies == nil || item.movieID == "" || item.FileID == "" {
		return nil
	}
	if _, err := s.movies.RemoveFile(ctx, &mgmntv1.RemoveFileRequest{FileId: item.FileID, DeleteFiles: false}); err != nil {
		return err
	}
	_, err := s.movies.AddFile(ctx, &mgmntv1.AddFileRequest{
		MovieId:   item.movieID,
		FilePath:  newPath,
		Quality:   item.Quality,
		SizeBytes: item.sizeBytes,
		Container: item.container,
	})
	return err
}

func sameRenamePath(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	return a != "." && a != "" && a == b
}
