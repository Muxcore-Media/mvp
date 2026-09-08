package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

type calendarItemJSON struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	Date      string `json:"date"`
	Href      string `json:"href"`
	Monitored bool   `json:"monitored"`
	HasFile   bool   `json:"has_file"`
	Season    int32  `json:"season_number,omitempty"`
	Episode   int32  `json:"episode_number,omitempty"`
	Year      int32  `json:"year,omitempty"`
}

func defaultCalendarWindow() (start, end string) {
	now := time.Now().UTC()
	start = now.AddDate(0, 0, -14).Format("2006-01-02")
	end = now.AddDate(0, 0, 120).Format("2006-01-02")
	return start, end
}

func (s *server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	start := strings.TrimSpace(r.URL.Query().Get("start"))
	end := strings.TrimSpace(r.URL.Query().Get("end"))
	if start == "" || end == "" {
		defStart, defEnd := defaultCalendarWindow()
		if start == "" {
			start = defStart
		}
		if end == "" {
			end = defEnd
		}
	}
	includeUnmon := r.URL.Query().Get("unmonitored") == "1"

	var items []calendarItemJSON
	tvOK, moviesOK := false, false

	if s.tv != nil {
		resp, err := s.tv.GetCalendar(r.Context(), &tvmgmtv1.GetCalendarRequest{
			StartDate: start, EndDate: end, IncludeUnmonitored: includeUnmon,
		})
		if err == nil {
			tvOK = true
			for _, it := range resp.GetItems() {
				if it == nil {
					continue
				}
				epLabel := it.GetEpisodeName()
				if it.GetSeasonNumber() > 0 || it.GetEpisodeNumber() > 0 {
					epLabel = formatEpisodeCode(it.GetSeasonNumber(), it.GetEpisodeNumber())
					if it.GetEpisodeName() != "" {
						epLabel += " · " + it.GetEpisodeName()
					}
				}
				items = append(items, calendarItemJSON{
					Kind:      "tv",
					ID:        it.GetEpisodeId(),
					ParentID:  it.GetSeriesId(),
					Title:     it.GetSeriesName(),
					Subtitle:  epLabel,
					Date:      it.GetAirDate(),
					Href:      "/tv/" + it.GetSeriesId(),
					Monitored: it.GetMonitored(),
					HasFile:   it.GetHasFile(),
					Season:    it.GetSeasonNumber(),
					Episode:   it.GetEpisodeNumber(),
				})
			}
		}
	}

	if s.moviesHTTP != nil {
		u := *s.moviesHTTP
		u.Path = strings.TrimRight(s.moviesHTTP.Path, "/") + "/api/calendar"
		q := url.Values{"start": {start}, "end": {end}}
		if includeUnmon {
			q.Set("unmonitored", "1")
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
		if err == nil {
			resp, err := upstreamClient.Do(req)
			if err == nil {
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode == http.StatusOK {
					var body struct {
						Items []movieCalendarJSON `json:"items"`
					}
					if json.NewDecoder(resp.Body).Decode(&body) == nil {
						moviesOK = true
						for _, it := range body.Items {
							items = append(items, calendarItemJSON{
								Kind:      "movie",
								ID:        it.ID,
								ParentID:  it.ParentID,
								Title:     it.Title,
								Subtitle:  it.Subtitle,
								Date:      it.Date,
								Href:      "/movies/" + it.ID,
								Monitored: it.Monitored,
								HasFile:   it.HasFile,
								Year:      it.Year,
							})
						}
					}
				}
			}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Date == items[j].Date {
			return items[i].Title < items[j].Title
		}
		return items[i].Date < items[j].Date
	})
	if items == nil {
		items = []calendarItemJSON{}
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     len(items),
		"start":     start,
		"end":       end,
		"available": tvOK || moviesOK,
	})
}

type movieCalendarJSON struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle"`
	Date      string `json:"date"`
	Monitored bool   `json:"monitored"`
	HasFile   bool   `json:"has_file"`
	Year      int32  `json:"year"`
}

func formatEpisodeCode(season, episode int32) string {
	return fmt.Sprintf("S%02dE%02d", season, episode)
}
