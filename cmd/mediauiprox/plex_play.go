package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
)

func plexDetailsURL(baseURL, machineID, ratingKey string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	machine := strings.TrimSpace(machineID)
	key := strings.TrimSpace(ratingKey)
	if base == "" || machine == "" || key == "" {
		return ""
	}
	return base + "/web/index.html#!/server/" + machine + "/details?key=/library/metadata/" + key
}

func (s *server) handlePlexPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	ratingKey := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("rating_key"), r.URL.Query().Get("ratingKey")))
	if ratingKey == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "rating_key required", "code": "plex.rating_key_required"})
		return
	}
	if s.plex == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "plex bridge is not connected", "code": "plex.unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	play, err := s.plex.PlayURL(ctx, &plexv1.PlayURLRequest{RatingKey: ratingKey})
	if err == nil && strings.TrimSpace(play.GetUrl()) != "" {
		writeJSON(w, map[string]any{"url": play.GetUrl()})
		return
	}
	st, stErr := s.plex.Status(ctx, &plexv1.StatusRequest{})
	if stErr != nil {
		if err != nil {
			writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "plex.play_failed"})
			return
		}
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": stErr.Error(), "code": "plex.play_failed"})
		return
	}
	if strings.TrimSpace(st.GetBaseUrl()) == "" || strings.TrimSpace(st.GetMachineId()) == "" {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "plex not configured", "code": "plex.not_configured"})
		return
	}
	u := plexDetailsURL(st.GetBaseUrl(), st.GetMachineId(), ratingKey)
	if u == "" {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "empty play url", "code": "plex.empty_url"})
		return
	}
	writeJSON(w, map[string]any{"url": u})
}
