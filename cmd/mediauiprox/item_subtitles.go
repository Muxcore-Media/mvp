package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
)

const maxHouseholdSubtitleBytes = 2 << 20

func publicSubtitleFile(f *subtv1.SubtitleFile) map[string]any {
	if f == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                f.GetId(),
		"media_file_id":     f.GetMediaFileId(),
		"language":          f.GetLanguage(),
		"format":            strings.TrimPrefix(f.GetFormat(), "."),
		"forced":            f.GetForced(),
		"hearing_impaired":  f.GetHearingImpaired(),
		"source":            f.GetSource(),
		"provider":          f.GetProvider(),
		"score":             f.GetScore(),
		"size_bytes":        f.GetSizeBytes(),
		"filename":          filepath.Base(f.GetFilePath()),
	}
}

func publicSubtitleFileTarget(id, title string, season, episode int32) map[string]any {
	return map[string]any{
		"id":      id,
		"title":   title,
		"season":  season,
		"episode": episode,
	}
}

func (s *server) collectSubtitleFiles(ctx context.Context, fileIDs []string) []map[string]any {
	items := make([]map[string]any, 0)
	if s.subtitles == nil {
		return items
	}
	seen := map[string]bool{}
	for _, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" || seen[fileID] {
			continue
		}
		seen[fileID] = true
		resp, err := s.subtitles.ListSubtitles(ctx, &subtv1.ListSubtitlesRequest{
			MediaFileId: fileID,
			Page:        1,
			PageSize:    100,
		})
		if err != nil {
			continue
		}
		for _, row := range resp.GetSubtitles() {
			if row == nil || row.GetId() == "" {
				continue
			}
			items = append(items, publicSubtitleFile(row))
		}
	}
	return items
}

func (s *server) movieSubtitleFileIDs(ctx context.Context, movieID string) []string {
	ids := []string{}
	if s.movies != nil {
		listed, err := s.movies.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: movieID})
		if err == nil {
			for _, f := range listed.GetFiles() {
				if f == nil || strings.TrimSpace(f.GetId()) == "" {
					continue
				}
				ids = append(ids, f.GetId())
			}
		}
	}
	if len(ids) == 0 && s.subtitles != nil {
		if media, err := s.subtitles.GetMedia(ctx, &subtv1.GetMediaRequest{Id: movieID}); err == nil {
			if id := strings.TrimSpace(media.GetItem().GetMediaFileId()); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (s *server) tvSubtitleTargets(ctx context.Context, seriesID string) []map[string]any {
	targets := []map[string]any{}
	seen := map[string]bool{}
	if s.subtitles == nil {
		return targets
	}
	listed, err := s.subtitles.ListMedia(ctx, &subtv1.ListMediaRequest{
		Page:     1,
		PageSize: 100,
		SeriesId: seriesID,
	})
	if err == nil {
		for _, it := range listed.GetItems() {
			if it == nil {
				continue
			}
			id := strings.TrimSpace(it.GetMediaFileId())
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			title := strings.TrimSpace(it.GetTitle())
			if title == "" {
				title = strings.TrimSpace(it.GetSeriesName())
			}
			targets = append(targets, publicSubtitleFileTarget(id, title, it.GetSeason(), it.GetEpisode()))
		}
	}
	if len(targets) == 0 {
		if media, err := s.subtitles.GetMedia(ctx, &subtv1.GetMediaRequest{Id: seriesID}); err == nil {
			if id := strings.TrimSpace(media.GetItem().GetMediaFileId()); id != "" && !seen[id] {
				it := media.GetItem()
				targets = append(targets, publicSubtitleFileTarget(id, it.GetTitle(), it.GetSeason(), it.GetEpisode()))
			}
		}
	}
	return targets
}

func (s *server) handleListMovieSubtitles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "files": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	fileIDs := s.movieSubtitleFileIDs(ctx, id)
	files := make([]map[string]any, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		files = append(files, publicSubtitleFileTarget(fileID, "", 0, 0))
	}
	writeJSON(w, map[string]any{
		"available": true,
		"items":     s.collectSubtitleFiles(ctx, fileIDs),
		"files":     files,
	})
}

func (s *server) handleListTVSubtitles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	if s.subtitles == nil {
		writeJSON(w, map[string]any{"available": false, "items": []any{}, "files": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	files := s.tvSubtitleTargets(ctx, id)
	fileIDs := make([]string, 0, len(files))
	for _, row := range files {
		if id, ok := row["id"].(string); ok {
			fileIDs = append(fileIDs, id)
		}
	}
	writeJSON(w, map[string]any{
		"available": true,
		"items":     s.collectSubtitleFiles(ctx, fileIDs),
		"files":     files,
	})
}

func (s *server) uploadHouseholdSubtitle(ctx context.Context, mediaFileID, language string, forced, hi bool, data []byte) (map[string]any, int) {
	if s.subtitles == nil {
		return map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"}, http.StatusServiceUnavailable
	}
	mediaFileID = strings.TrimSpace(mediaFileID)
	if mediaFileID == "" {
		return map[string]any{"error": "media_file_id required", "code": "subtitles.file_required"}, http.StatusBadRequest
	}
	if len(data) == 0 {
		return map[string]any{"error": "subtitle data required", "code": "subtitles.data_required"}, http.StatusBadRequest
	}
	if len(data) > maxHouseholdSubtitleBytes {
		return map[string]any{"error": "subtitle exceeds 2MB", "code": "subtitles.too_large"}, http.StatusBadRequest
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = "eng"
	}
	stream, err := s.subtitles.Upload(ctx)
	if err != nil {
		return map[string]any{"error": err.Error(), "code": "subtitles.upload_failed"}, http.StatusBadGateway
	}
	meta, err := json.Marshal(map[string]any{
		"media_file_id":     mediaFileID,
		"language":          language,
		"forced":            forced,
		"hearing_impaired":  hi,
	})
	if err != nil {
		return map[string]any{"error": err.Error(), "code": "subtitles.upload_failed"}, http.StatusBadRequest
	}
	if err := stream.Send(&subtv1.UploadRequest{Data: &subtv1.UploadRequest_Metadata{Metadata: string(meta)}}); err != nil {
		return map[string]any{"error": err.Error(), "code": "subtitles.upload_failed"}, http.StatusBadGateway
	}
	const chunk = 64 << 10
	for off := 0; off < len(data); off += chunk {
		end := off + chunk
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&subtv1.UploadRequest{Data: &subtv1.UploadRequest_Chunk{Chunk: data[off:end]}}); err != nil {
			return map[string]any{"error": err.Error(), "code": "subtitles.upload_failed"}, http.StatusBadGateway
		}
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return map[string]any{"error": err.Error(), "code": "subtitles.upload_failed"}, http.StatusBadGateway
	}
	return map[string]any{"ok": true, "subtitle": publicSubtitleFile(resp.GetSubtitle())}, http.StatusOK
}

func (s *server) handleUploadItemSubtitles(w http.ResponseWriter, r *http.Request, resolveFileID func(ctx context.Context) string) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	var body struct {
		MediaFileID     string `json:"media_file_id"`
		Language        string `json:"language"`
		Filename        string `json:"filename"`
		Data            string `json:"data"`
		Forced          bool   `json:"forced"`
		HearingImpaired bool   `json:"hearing_impaired"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	data, err := decodeArtworkPayload(body.Data)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid subtitle data", "code": "subtitles.data_invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	fileID := strings.TrimSpace(body.MediaFileID)
	if fileID == "" {
		fileID = resolveFileID(ctx)
	}
	_ = body.Filename
	out, status := s.uploadHouseholdSubtitle(ctx, fileID, body.Language, body.Forced, body.HearingImpaired, data)
	writeJSONStatus(w, status, out)
}

func (s *server) handleUploadMovieSubtitles(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	s.handleUploadItemSubtitles(w, r, func(ctx context.Context) string {
		ids := s.movieSubtitleFileIDs(ctx, id)
		if len(ids) == 0 {
			return ""
		}
		return ids[0]
	})
}

func (s *server) handleUploadTVSubtitles(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	s.handleUploadItemSubtitles(w, r, func(ctx context.Context) string {
		for _, row := range s.tvSubtitleTargets(ctx, id) {
			if fileID, ok := row["id"].(string); ok && fileID != "" {
				return fileID
			}
		}
		return ""
	})
}

func (s *server) handleDeleteItemSubtitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "subtitles.forbidden"})
		return
	}
	if s.subtitles == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "media-subtitles unavailable", "code": "subtitles.unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "subtitles.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.subtitles.Delete(ctx, &subtv1.DeleteRequest{Id: id}); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "subtitles.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "removed": true, "id": id})
}
