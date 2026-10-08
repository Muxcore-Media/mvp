package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"github.com/Muxcore-Media/userdata-local/parental"
)

// C-LIST support (ADR-0031 §1.2, §2.1). A restricted principal's list request
// is narrowed at the owning module with its classification_filter, so
// pagination and total count only visible items, and every returned item is
// re-evaluated here with parental.Evaluate; an item that fails is dropped.
// Items the module returns without classification fields (an older module that
// ignores the filter) read as unavailable and are dropped too, so a module that
// cannot filter yields an empty list rather than hidden titles.

// filterFields maps the restricted rules onto the media modules'
// ClassificationFilter fields. kids_mode with no explicit ceiling becomes PG.
func (r parentalRestriction) filterFields() (maxRating string, allowUnrated bool, blocked, allowed []string) {
	rules := r.policy.Rules
	if rules == nil {
		return "", false, nil, nil
	}
	maxRating = rules.MaxRating
	if maxRating == "" && rules.KidsMode {
		maxRating = kidsCeilingToken
	}
	return maxRating, rules.AllowUnrated, rules.BlockedTags, rules.AllowedTags
}

func (r parentalRestriction) movieFilter() *mgmntv1.ClassificationFilter {
	max, unrated, blocked, allowed := r.filterFields()
	return &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: max, AllowUnrated: unrated, BlockedTags: blocked, AllowedTags: allowed}
}

func (r parentalRestriction) tvFilter() *tvmgmtv1.ClassificationFilter {
	max, unrated, blocked, allowed := r.filterFields()
	return &tvmgmtv1.ClassificationFilter{Enabled: true, MaxRating: max, AllowUnrated: unrated, BlockedTags: blocked, AllowedTags: allowed}
}

func (r parentalRestriction) allowsMovie(m *mgmntv1.MovieItem) bool {
	return m != nil && parental.Evaluate(r.policy, movieClassification(m)).Allowed
}

func (r parentalRestriction) allowsSeries(sr *tvmgmtv1.TVSeries) bool {
	return sr != nil && parental.Evaluate(r.policy, seriesClassification(sr)).Allowed
}

// visibleMovies keeps the movies the restriction allows. Without a
// restriction the list is returned unchanged.
func visibleMovies(ctx context.Context, in []*mgmntv1.MovieItem) []*mgmntv1.MovieItem {
	restr, ok := parentalRestrictionFrom(ctx)
	if !ok {
		return in
	}
	out := make([]*mgmntv1.MovieItem, 0, len(in))
	for _, m := range in {
		if restr.allowsMovie(m) {
			out = append(out, m)
		}
	}
	if len(out) != len(in) {
		log.Printf("parental: dropped %d movies the restriction does not allow (module filter or collection read)", len(in)-len(out))
	}
	return out
}

func visibleSeries(ctx context.Context, in []*tvmgmtv1.TVSeries) []*tvmgmtv1.TVSeries {
	restr, ok := parentalRestrictionFrom(ctx)
	if !ok {
		return in
	}
	out := make([]*tvmgmtv1.TVSeries, 0, len(in))
	for _, sr := range in {
		if restr.allowsSeries(sr) {
			out = append(out, sr)
		}
	}
	if len(out) != len(in) {
		log.Printf("parental: dropped %d series the restriction does not allow (module filter or collection read)", len(in)-len(out))
	}
	return out
}

// listMoviesRequest is the ListMovies request for the caller: narrowed at the
// module when the request context carries a restriction.
func listMoviesRequest(ctx context.Context, req *mgmntv1.ListMoviesRequest) *mgmntv1.ListMoviesRequest {
	if restr, ok := parentalRestrictionFrom(ctx); ok {
		req.ClassificationFilter = restr.movieFilter()
	}
	return req
}

// maxRestrictedCollections bounds the per-collection GetCollectionMovies
// calls a restricted GET /api/collections makes. A library with more
// collections cannot be listed completely inside the request budget, so it
// fails closed (503 parental.classification_unavailable) instead of returning
// a partial list.
const maxRestrictedCollections = 1000

// restrictedCollections derives the collection list for a restricted
// principal from the movies it may see. ListMovies does not carry collection
// membership (the module's list query leaves collection_id/collection_name
// empty), and ListCollections counts every movie, hidden ones included, and
// names a collection after any of them. So ListCollections supplies only the
// collection IDs and the monitored flag, and GetCollectionMovies, which does
// carry classification and membership, supplies the movies: a collection
// exists for this principal only through a visible movie, its name comes from
// a visible movie, and its count is the number of visible movies in it.
// Any failed call, an oversized library or a module answer for another
// collection fails the whole list; there is never a partial result.
func (s *server) restrictedCollections(ctx context.Context, restr parentalRestriction) ([]map[string]any, error) {
	all, err := s.movies.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{})
	if err != nil {
		return nil, err
	}
	summaries := all.GetCollections()
	if len(summaries) > maxRestrictedCollections {
		return nil, fmt.Errorf("%d collections exceed the restricted listing bound of %d", len(summaries), maxRestrictedCollections)
	}
	type visible struct {
		id        int32
		name      string
		count     int
		monitored bool
	}
	var found []visible
	seen := map[int32]bool{}
	for _, c := range summaries {
		id := c.GetCollectionId()
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		resp, err := s.movies.GetCollectionMovies(ctx, &mgmntv1.GetCollectionMoviesRequest{CollectionId: id})
		if err != nil {
			return nil, err
		}
		if resp.GetCollectionId() != id {
			return nil, fmt.Errorf("media-movies answered for collection %d when asked for %d", resp.GetCollectionId(), id)
		}
		name, count := "", 0
		for _, m := range visibleMovies(ctx, resp.GetMovies()) {
			if m.GetCollectionId() != id {
				return nil, fmt.Errorf("media-movies returned a movie of collection %d for collection %d", m.GetCollectionId(), id)
			}
			count++
			if n := m.GetCollectionName(); n > name {
				name = n
			}
		}
		if count > 0 {
			found = append(found, visible{id: id, name: name, count: count, monitored: c.GetMonitored()})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].name != found[j].name {
			return found[i].name < found[j].name
		}
		return found[i].id < found[j].id
	})
	items := make([]map[string]any, 0, len(found))
	for _, v := range found {
		items = append(items, map[string]any{
			"id":          strconv.Itoa(int(v.id)),
			"name":        v.name,
			"movie_count": v.count,
			"monitored":   v.monitored,
		})
	}
	return items, nil
}

// writeListGatewayError answers a failed module list call. For a restricted
// principal the list is the classification, so a module failure is
// 503 parental.classification_unavailable (ADR-0031 §3), never an empty or
// partial list; everyone else keeps the 502 gateway error.
func writeListGatewayError(w http.ResponseWriter, ctx context.Context, err error, code string) {
	if _, restricted := parentalRestrictionFrom(ctx); restricted {
		log.Printf("parental: restricted list failed at the owning module: %v", err)
		writeParentalError(w, errParentalClassif, "")
		return
	}
	writeAPIError(w, http.StatusBadGateway, err.Error(), code)
}
