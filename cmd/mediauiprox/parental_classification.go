package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"github.com/Muxcore-Media/userdata-local/parental"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Only the published operator source is authoritative in this slice. TMDB is
// reserved for later provenance work, not a synonym for operator approval.
func catalogClassification(rating, source string, tags []string) parental.Classification {
	c := parental.Classification{Tags: append([]string(nil), tags...), TagsKnown: true}
	if source != "operator" {
		return c
	}
	c.Rating = strings.ToUpper(strings.TrimSpace(rating))
	if c.Rating == "NR" || c.Rating == "UR" {
		c.State = parental.Unrated
	} else if _, ok := parental.RatingLevel(c.Rating); ok {
		c.State = parental.Rated
	}
	return c
}
func movieClassification(m *mgmntv1.MovieItem) parental.Classification {
	return catalogClassification(m.GetContentRating(), m.GetContentRatingSource(), m.GetTagLabels())
}
func seriesClassification(m *tvmgmtv1.TVSeries) parental.Classification {
	return catalogClassification(m.GetContentRating(), m.GetContentRatingSource(), m.GetTagLabels())
}

type catalogClassEntry struct {
	classification parental.Classification
	expires        time.Time
}
type catalogClassifier struct {
	s     *server
	now   func() time.Time
	mu    sync.Mutex
	cache map[parentalItem]catalogClassEntry
}

func newCatalogClassifier(s *server, now func() time.Time) *catalogClassifier {
	return &catalogClassifier{s: s, now: now, cache: make(map[parentalItem]catalogClassEntry)}
}

// Classify is used only by playback. Cache both the episode mapping and series
// classification as one result, so chained cache hits cannot extend the 30 s
// bound. Never cache an RPC error or an unavailable classification.
func (c *catalogClassifier) Classify(ctx context.Context, item parentalItem) (parental.Classification, error) {
	now := c.now()
	c.mu.Lock()
	e, ok := c.cache[item]
	c.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.classification, nil
	}
	result, err := c.ClassifyFresh(ctx, item)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, item)
	if err != nil || result.State == parental.Unavailable {
		return result, err
	}
	if len(c.cache) >= parentalPolicyCacheMax {
		for key, old := range c.cache {
			if !now.Before(old.expires) {
				delete(c.cache, key)
			}
		}
	}
	if len(c.cache) < parentalPolicyCacheMax {
		c.cache[item] = catalogClassEntry{classification: result, expires: now.Add(parentalPolicyTTL)}
	}
	return result, nil
}

func (c *catalogClassifier) ClassifyFresh(ctx context.Context, item parentalItem) (parental.Classification, error) {
	unavailable := parental.Classification{State: parental.Unavailable}
	if item.ID == "" || item.Kind == "" {
		return unavailable, nil
	}
	ctx, cancel := context.WithTimeout(ctx, parentalPolicyTimeout)
	defer cancel()
	switch item.Kind {
	case "movie":
		if c.s.movies == nil {
			return unavailable, errors.New("movie provider unavailable")
		}
		resp, err := c.s.movies.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: item.ID})
		if status.Code(err) == codes.NotFound {
			return unavailable, nil
		}
		if err != nil {
			return unavailable, err
		}
		if resp.GetMovie() == nil || resp.GetMovie().GetId() != item.ID {
			return unavailable, errors.New("invalid movie identity")
		}
		return movieClassification(resp.GetMovie()), nil
	case "episode":
		if c.s.tv == nil {
			return unavailable, errors.New("TV provider unavailable")
		}
		resp, err := c.s.tv.GetEpisode(ctx, &tvmgmtv1.GetEpisodeRequest{EpisodeId: item.ID})
		if status.Code(err) == codes.NotFound {
			return unavailable, nil
		}
		if err != nil {
			return unavailable, err
		}
		ep := resp.GetEpisode()
		if ep == nil || ep.GetId() != item.ID || ep.GetSeriesId() == "" {
			return unavailable, errors.New("invalid episode identity")
		}
		item = parentalItem{Kind: "series", ID: ep.GetSeriesId()}
		fallthrough
	case "series":
		if c.s.tv == nil {
			return unavailable, errors.New("TV provider unavailable")
		}
		resp, err := c.s.tv.GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: item.ID})
		if status.Code(err) == codes.NotFound {
			return unavailable, nil
		}
		if err != nil {
			return unavailable, err
		}
		if resp.GetSeries() == nil || resp.GetSeries().GetId() != item.ID {
			return unavailable, errors.New("invalid series identity")
		}
		return seriesClassification(resp.GetSeries()), nil
	default:
		return unavailable, nil
	}
}

type parentalBrowseKey struct{}

func restrictedBrowsePolicy(ctx context.Context) (parental.Policy, bool) {
	p, ok := ctx.Value(parentalBrowseKey{}).(parental.Policy)
	return p, ok
}
func movieVisible(ctx context.Context, m *mgmntv1.MovieItem) bool {
	p, restricted := restrictedBrowsePolicy(ctx)
	return !restricted || (m.GetId() != "" && parental.Evaluate(p, movieClassification(m)).Allowed)
}
func seriesVisible(ctx context.Context, m *tvmgmtv1.TVSeries) bool {
	p, restricted := restrictedBrowsePolicy(ctx)
	return !restricted || (m.GetId() != "" && parental.Evaluate(p, seriesClassification(m)).Allowed)
}
func movieClassificationFilter(ctx context.Context) *mgmntv1.ClassificationFilter {
	p, restricted := restrictedBrowsePolicy(ctx)
	if !restricted {
		return nil
	}
	r := p.Rules
	ceiling := r.MaxRating
	if ceiling == "" && r.KidsMode {
		ceiling = "PG"
	}
	return &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: ceiling, AllowUnrated: r.AllowUnrated, BlockedTags: append([]string(nil), r.BlockedTags...), AllowedTags: append([]string(nil), r.AllowedTags...)}
}
func tvClassificationFilter(ctx context.Context) *tvmgmtv1.ClassificationFilter {
	f := movieClassificationFilter(ctx)
	if f == nil {
		return nil
	}
	return &tvmgmtv1.ClassificationFilter{Enabled: f.Enabled, MaxRating: f.MaxRating, AllowUnrated: f.AllowUnrated, BlockedTags: f.BlockedTags, AllowedTags: f.AllowedTags}
}
func writeBrowseFailure(w http.ResponseWriter, r *http.Request, err error, code string) {
	if _, restricted := restrictedBrowsePolicy(r.Context()); restricted {
		writeParentalError(w, errParentalClassif, "")
		return
	}
	writeAPIError(w, http.StatusBadGateway, err.Error(), code)
}

// Detail handlers evaluate their response fields, while per-item handlers get
// a fresh preflight check. Neither path uses the playback classification cache.
func (s *server) parentalBrowseGate(route parentalRoute, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pol, _, active, perr := s.parentalCheck(r)
		if perr != nil {
			writeParentalError(w, perr, "")
			return
		}
		if !active || pol.unrestricted() {
			next.ServeHTTP(w, r)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), parentalBrowseKey{}, pol.policy))
		if route.class == classItem && !route.itemInResponse {
			classifier := s.parental.itemClassifier()
			classify := classifier.Classify
			if fresh, ok := classifier.(interface {
				ClassifyFresh(context.Context, parentalItem) (parental.Classification, error)
			}); ok {
				classify = fresh.ClassifyFresh
			}
			c, err := classify(r.Context(), parentalItemFromAPIPath(r))
			if err != nil {
				writeParentalError(w, errParentalClassif, "")
				return
			}
			if !parental.Evaluate(pol.policy, c).Allowed {
				writeParentalError(w, errParentalBlocked, "")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func parentalItemFromAPIPath(r *http.Request) parentalItem {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" {
		return parentalItem{}
	}
	id := r.PathValue("id")
	if id == "" {
		id = parts[2]
	}
	switch parts[1] {
	case "movies":
		return parentalItem{Kind: "movie", ID: id}
	case "tv":
		return parentalItem{Kind: "series", ID: id}
	case "episodes":
		return parentalItem{Kind: "episode", ID: id}
	}
	return parentalItem{}
}

func checkMovieResponse(w http.ResponseWriter, r *http.Request, id string, m *mgmntv1.MovieItem) bool {
	if _, restricted := restrictedBrowsePolicy(r.Context()); !restricted {
		return true
	}
	if m == nil || m.GetId() != id {
		writeParentalError(w, errParentalClassif, "")
		return false
	}
	if !movieVisible(r.Context(), m) {
		writeParentalError(w, errParentalBlocked, "")
		return false
	}
	return true
}
func checkSeriesResponse(w http.ResponseWriter, r *http.Request, id string, m *tvmgmtv1.TVSeries) bool {
	if _, restricted := restrictedBrowsePolicy(r.Context()); !restricted {
		return true
	}
	if m == nil || m.GetId() != id {
		writeParentalError(w, errParentalClassif, "")
		return false
	}
	if !seriesVisible(r.Context(), m) {
		writeParentalError(w, errParentalBlocked, "")
		return false
	}
	return true
}

func writeItemBrowseFailure(w http.ResponseWriter, r *http.Request, err error, code string) {
	if _, restricted := restrictedBrowsePolicy(r.Context()); restricted && status.Code(err) == codes.NotFound {
		writeParentalError(w, errParentalBlocked, "")
		return
	}
	writeBrowseFailure(w, r, err, code)
}
