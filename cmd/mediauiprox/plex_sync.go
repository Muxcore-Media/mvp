package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
)

func (s *server) handlePlexSyncLists(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if s.plex == nil {
		writeJSON(w, map[string]any{
			"available": false,
			"lists":     []any{},
			"total":     0,
		})
		return
	}
	q := r.URL.Query()
	refresh := strings.EqualFold(strings.TrimSpace(q.Get("refresh")), "1") ||
		strings.EqualFold(strings.TrimSpace(q.Get("refresh")), "true")
	userID := firstNonEmpty(q.Get("userId"), q.Get("user_id"))
	clientID := firstNonEmpty(q.Get("clientId"), q.Get("client_id"))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resp, err := s.plex.ListSyncLists(ctx, &plexv1.ListSyncListsRequest{
		UserId:   strings.TrimSpace(userID),
		ClientId: strings.TrimSpace(clientID),
		Refresh:  refresh,
	})
	if err != nil {
		writeJSON(w, map[string]any{
			"available": false,
			"error":     err.Error(),
			"lists":     []any{},
			"total":     0,
		})
		return
	}
	lists := make([]map[string]any, 0, len(resp.GetLists()))
	for _, list := range resp.GetLists() {
		if list == nil {
			continue
		}
		items := make([]map[string]any, 0, len(list.GetItems()))
		for _, item := range list.GetItems() {
			if item == nil {
				continue
			}
			items = append(items, map[string]any{
				"id":                   item.GetId(),
				"title":                item.GetTitle(),
				"rootTitle":            item.GetRootTitle(),
				"metadataType":         item.GetMetadataType(),
				"contentType":          item.GetContentType(),
				"mediaType":            item.GetMediaType(),
				"ratingKey":            item.GetRatingKey(),
				"state":                item.GetState(),
				"failure":              item.GetFailure(),
				"itemsCount":           item.GetItemsCount(),
				"itemsCompleteCount":   item.GetItemsCompleteCount(),
				"itemsDownloadedCount": item.GetItemsDownloadedCount(),
				"totalSizeBytes":       item.GetTotalSizeBytes(),
				"videoResolution":      item.GetVideoResolution(),
			})
		}
		lists = append(lists, map[string]any{
			"id":               list.GetId(),
			"clientIdentifier": list.GetClientIdentifier(),
			"deviceUserId":     list.GetDeviceUserId(),
			"deviceName":       list.GetDeviceName(),
			"devicePlatform":   list.GetDevicePlatform(),
			"deviceProduct":    list.GetDeviceProduct(),
			"items":            items,
		})
	}
	writeJSON(w, map[string]any{
		"available":         true,
		"machineIdentifier": resp.GetMachineIdentifier(),
		"updatedAt":         resp.GetUpdatedAt(),
		"lists":             lists,
		"total":             len(lists),
	})
}
