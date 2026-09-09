package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

const maxHouseholdArtworkBytes = 20 << 20

func artworkTypeLabel(t mediaadminv1.ArtworkType) string {
	switch t {
	case mediaadminv1.ArtworkType_ARTWORK_TYPE_POSTER:
		return "poster"
	case mediaadminv1.ArtworkType_ARTWORK_TYPE_BACKGROUND:
		return "background"
	case mediaadminv1.ArtworkType_ARTWORK_TYPE_LOGO:
		return "logo"
	case mediaadminv1.ArtworkType_ARTWORK_TYPE_BANNER:
		return "banner"
	case mediaadminv1.ArtworkType_ARTWORK_TYPE_THUMB:
		return "thumb"
	case mediaadminv1.ArtworkType_ARTWORK_TYPE_STILL:
		return "still"
	default:
		return ""
	}
}

// householdArtworkURL rewrites module artwork URLs (often http://module/images/…)
// onto the SPA-facing /images/{movies|tv}/ prefix.
func householdArtworkURL(kind, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.Index(raw, "/images/"); i >= 0 {
		rest := strings.TrimPrefix(raw[i+len("/images/"):], "/")
		prefix := "movies/"
		if kind == "tv" {
			prefix = "tv/"
		}
		if kind == "music" {
			prefix = "music/"
		}
		if strings.HasPrefix(rest, "movies/") || strings.HasPrefix(rest, "tv/") || strings.HasPrefix(rest, "music/") {
			return "/images/" + rest
		}
		return "/images/" + prefix + rest
	}
	return consumerImageURL(kind, raw)
}

func publicArtworkInfo(kind string, art *mediaadminv1.ArtworkInfo) map[string]any {
	if art == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":        art.GetId(),
		"item_id":   art.GetItemId(),
		"type":      artworkTypeLabel(art.GetType()),
		"url":       householdArtworkURL(kind, art.GetUrl()),
		"width":     art.GetWidth(),
		"height":    art.GetHeight(),
		"mime_type": art.GetMimeType(),
	}
}

func (s *server) listItemArtwork(ctx context.Context, client mediaadminv1.MediaAdminServiceClient, kind, itemID string) map[string]any {
	if client == nil {
		return map[string]any{"available": false, "items": []any{}}
	}
	resp, err := client.ListArtwork(ctx, &mediaadminv1.ListArtworkRequest{Id: itemID})
	if err != nil {
		return map[string]any{"available": false, "items": []any{}, "error": err.Error()}
	}
	items := make([]map[string]any, 0, len(resp.GetArtwork()))
	for _, art := range resp.GetArtwork() {
		if art == nil {
			continue
		}
		items = append(items, publicArtworkInfo(kind, art))
	}
	return map[string]any{"available": true, "items": items}
}

func (s *server) handleListMovieArtwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "artwork.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, s.listItemArtwork(ctx, s.moviesAdmin, "movies", id))
}

func (s *server) handleListTVArtwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "artwork.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, s.listItemArtwork(ctx, s.tvAdmin, "tv", id))
}

func parseArtworkType(raw string) mediaadminv1.ArtworkType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "poster", "1":
		return mediaadminv1.ArtworkType_ARTWORK_TYPE_POSTER
	case "background", "backdrop", "2":
		return mediaadminv1.ArtworkType_ARTWORK_TYPE_BACKGROUND
	case "logo", "3":
		return mediaadminv1.ArtworkType_ARTWORK_TYPE_LOGO
	case "banner", "4":
		return mediaadminv1.ArtworkType_ARTWORK_TYPE_BANNER
	case "thumb", "5":
		return mediaadminv1.ArtworkType_ARTWORK_TYPE_THUMB
	case "still", "6":
		return mediaadminv1.ArtworkType_ARTWORK_TYPE_STILL
	default:
		return mediaadminv1.ArtworkType_ARTWORK_TYPE_UNSPECIFIED
	}
}

func decodeArtworkPayload(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, ","); i >= 0 && strings.Contains(raw[:i], "base64") {
		raw = raw[i+1:]
	}
	return base64.StdEncoding.DecodeString(raw)
}

func (s *server) replaceItemArtwork(ctx context.Context, client mediaadminv1.MediaAdminServiceClient, kind, itemID, filename string, typ mediaadminv1.ArtworkType, data []byte) (map[string]any, int) {
	if client == nil {
		return map[string]any{"error": "media module unavailable", "code": "artwork.unavailable"}, http.StatusServiceUnavailable
	}
	if itemID == "" {
		return map[string]any{"error": "id required", "code": "artwork.id_required"}, http.StatusBadRequest
	}
	if typ == mediaadminv1.ArtworkType_ARTWORK_TYPE_UNSPECIFIED {
		return map[string]any{"error": "artwork type required", "code": "artwork.type_required"}, http.StatusBadRequest
	}
	if len(data) == 0 {
		return map[string]any{"error": "artwork data required", "code": "artwork.data_required"}, http.StatusBadRequest
	}
	if len(data) > maxHouseholdArtworkBytes {
		return map[string]any{"error": "artwork exceeds 20MB", "code": "artwork.too_large"}, http.StatusBadRequest
	}
	stream, err := client.ReplaceArtwork(ctx)
	if err != nil {
		return map[string]any{"error": err.Error(), "code": "artwork.replace_failed"}, http.StatusBadGateway
	}
	msgs := []*mediaadminv1.ReplaceArtworkRequest{
		{Data: &mediaadminv1.ReplaceArtworkRequest_ItemId{ItemId: itemID}},
		{Data: &mediaadminv1.ReplaceArtworkRequest_ArtworkType{ArtworkType: typ}},
		{Data: &mediaadminv1.ReplaceArtworkRequest_Filename{Filename: filename}},
	}
	const chunk = 64 << 10
	for off := 0; off < len(data); off += chunk {
		end := off + chunk
		if end > len(data) {
			end = len(data)
		}
		msgs = append(msgs, &mediaadminv1.ReplaceArtworkRequest{
			Data: &mediaadminv1.ReplaceArtworkRequest_Chunk{Chunk: data[off:end]},
		})
	}
	for _, msg := range msgs {
		if err := stream.Send(msg); err != nil {
			return map[string]any{"error": err.Error(), "code": "artwork.replace_failed"}, http.StatusBadGateway
		}
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return map[string]any{"error": err.Error(), "code": "artwork.replace_failed"}, http.StatusBadGateway
	}
	return map[string]any{"ok": true, "artwork": publicArtworkInfo(kind, resp.GetArtwork())}, http.StatusOK
}

func (s *server) handleReplaceArtwork(w http.ResponseWriter, r *http.Request, client mediaadminv1.MediaAdminServiceClient, kind string) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "artwork.forbidden"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Type     string `json:"type"`
		Filename string `json:"filename"`
		Data     string `json:"data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	data, err := decodeArtworkPayload(body.Data)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid artwork data", "code": "artwork.data_invalid"})
		return
	}
	filename := strings.TrimSpace(body.Filename)
	if filename == "" {
		filename = "artwork.jpg"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, status := s.replaceItemArtwork(ctx, client, kind, id, filename, parseArtworkType(body.Type), data)
	writeJSONStatus(w, status, out)
}

func (s *server) handleReplaceMovieArtwork(w http.ResponseWriter, r *http.Request) {
	s.handleReplaceArtwork(w, r, s.moviesAdmin, "movies")
}

func (s *server) handleReplaceTVArtwork(w http.ResponseWriter, r *http.Request) {
	s.handleReplaceArtwork(w, r, s.tvAdmin, "tv")
}

func (s *server) handleListMusicArtwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "id required", "code": "artwork.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, s.listItemArtwork(ctx, s.musicAdmin, "music", id))
}

func (s *server) handleReplaceMusicArtwork(w http.ResponseWriter, r *http.Request) {
	s.handleReplaceArtwork(w, r, s.musicAdmin, "music")
}
