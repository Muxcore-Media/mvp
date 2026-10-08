package main

import (
	"context"
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
		log.Printf("parental: dropped %d movies the module returned past its own classification filter", len(in)-len(out))
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
		log.Printf("parental: dropped %d series the module returned past its own classification filter", len(in)-len(out))
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

// restrictedCollections derives the collection list for a restricted
// principal from the visible movies alone. The module's ListCollections counts
// every movie, hidden ones included, and its names can reveal hidden titles, so
// neither is used: a collection exists for this principal only through a
// visible movie, and its count is the number of visible movies in it.
func (s *server) restrictedCollections(ctx context.Context, restr parentalRestriction) ([]map[string]any, error) {
	type agg struct {
		name  string
		count int
	}
	byID := map[int32]*agg{}
	for page := int32(1); ; page++ {
		resp, err := s.movies.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: page, PageSize: 100, ClassificationFilter: restr.movieFilter()})
		if err != nil {
			return nil, err
		}
		for _, m := range resp.GetMovies() {
			if !restr.allowsMovie(m) || m.GetCollectionId() <= 0 {
				continue
			}
			a := byID[m.GetCollectionId()]
			if a == nil {
				a = &agg{name: m.GetCollectionName()}
				byID[m.GetCollectionId()] = a
			}
			a.count++
		}
		if len(resp.GetMovies()) == 0 || int(page)*100 >= int(resp.GetTotal()) {
			break
		}
	}
	monitored := map[int32]bool{}
	if all, err := s.movies.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{}); err == nil {
		for _, c := range all.GetCollections() {
			monitored[c.GetCollectionId()] = c.GetMonitored()
		}
	}
	ids := make([]int, 0, len(byID))
	for id := range byID {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	sort.SliceStable(ids, func(i, j int) bool { return byID[int32(ids[i])].name < byID[int32(ids[j])].name })
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		a := byID[int32(id)]
		items = append(items, map[string]any{
			"id":          strconv.Itoa(id),
			"name":        a.name,
			"movie_count": a.count,
			"monitored":   monitored[int32(id)],
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
