package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"github.com/Muxcore-Media/userdata-local/parental"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Media classification (ADR-0031 §2, roadmap T-M4-01 S5b).
//
// The authoritative classification of a movie or series belongs to its owning
// media module (ADR-0009): content_rating, content_rating_source and
// tag_labels on MovieItem/TVSeries. Nothing here reads request fields, the
// userdata blob, filenames or bridges. Episodes and seasons carry no rating of
// their own; they inherit their series' classification, and the series is
// found through media-tvshows GetEpisode.

// Item kinds the classifier understands. parentalItem.Kind "" is unknown and
// never reaches a module.
const (
	kindMovie   = "movie"
	kindSeries  = "series"
	kindEpisode = "episode"
	// kindMedia names an item whose kind the request does not state (the
	// segments route's media_id). It is looked up as both a movie and an
	// episode; an id found as both is ambiguous and unavailable.
	kindMedia = "media"
)

// Content-rating sources a module may record. Anything else, including an
// empty source next to a rating, reads as unavailable so a malformed or
// forged field can never widen access.
const (
	ratingSourceOperator = "operator"
	ratingSourceTMDB     = "tmdb"
)

// kidsCeilingToken is the ceiling the media modules' ClassificationFilter
// needs when kids_mode has no explicit max_rating ("The BFF resolves kids_mode
// to PG before calling", movies.proto/tvshows.proto). parental.Evaluate owns
// the rule; TestKidsCeilingMatchesEvaluate pins this copy to it.
const kidsCeilingToken = "PG"

// errClassifyNotFound reports that the owning module has no such item (or an
// episode with no resolvable series). It classifies as unavailable (denied,
// not a lookup failure) and is never cached.
var errClassifyNotFound = errors.New("item not found")

// classificationFromFields converts a module's rating fields into the shared
// evaluator's Classification. Tags are known because the module returned the
// item: a failed tag lookup fails the whole RPC instead (modules never return
// an item that "looks untagged because a query failed").
func classificationFromFields(rating, source string, tagLabels []string) parental.Classification {
	c := parental.Classification{State: parental.Unavailable, Tags: tagLabels, TagsKnown: true}
	if s := strings.TrimSpace(source); s != ratingSourceOperator && s != ratingSourceTMDB {
		return c
	}
	token := strings.ToUpper(strings.TrimSpace(rating))
	switch token {
	case "NR", "UR":
		c.State = parental.Unrated
	default:
		if _, ok := parental.RatingLevel(token); ok {
			c.State, c.Rating = parental.Rated, token
		}
	}
	return c
}

func movieClassification(m *mgmntv1.MovieItem) parental.Classification {
	return classificationFromFields(m.GetContentRating(), m.GetContentRatingSource(), m.GetTagLabels())
}

func seriesClassification(s *tvmgmtv1.TVSeries) parental.Classification {
	return classificationFromFields(s.GetContentRating(), s.GetContentRatingSource(), s.GetTagLabels())
}

// mediaClassifier looks classifications up in media-movies and media-tvshows.
// It reads the server's module clients at call time so construction order does
// not matter; a missing client is a lookup failure (503), never "unrated".
type mediaClassifier struct {
	s *server
}

func newMediaClassifier(s *server) *mediaClassifier { return &mediaClassifier{s: s} }

func (c *mediaClassifier) Classify(ctx context.Context, item parentalItem) (parental.Classification, error) {
	switch item.Kind {
	case kindMovie:
		return c.movie(ctx, item.ID)
	case kindSeries:
		return c.series(ctx, item.ID)
	case kindEpisode:
		return c.episode(ctx, item.ID)
	case kindMedia:
		return c.media(ctx, item.ID)
	}
	return parental.Classification{}, errClassifyNotFound
}

func (c *mediaClassifier) movie(ctx context.Context, id string) (parental.Classification, error) {
	if c.s == nil || c.s.movies == nil {
		return parental.Classification{}, errors.New("media-movies client unavailable")
	}
	resp, err := c.s.movies.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: id})
	if err != nil {
		return parental.Classification{}, lookupError("movie", id, err)
	}
	m := resp.GetMovie()
	// The module must answer for the id that was asked about.
	if m == nil || m.GetId() != id {
		return parental.Classification{}, errClassifyNotFound
	}
	return movieClassification(m), nil
}

func (c *mediaClassifier) series(ctx context.Context, id string) (parental.Classification, error) {
	if c.s == nil || c.s.tv == nil {
		return parental.Classification{}, errors.New("media-tvshows client unavailable")
	}
	resp, err := c.s.tv.GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: id})
	if err != nil {
		return parental.Classification{}, lookupError("series", id, err)
	}
	sr := resp.GetSeries()
	if sr == nil || sr.GetId() != id {
		return parental.Classification{}, errClassifyNotFound
	}
	return seriesClassification(sr), nil
}

// episode maps an episode to its owning series (GetEpisode) and returns the
// series' classification. An episode whose response names another id, or no
// series, is an ambiguous mapping and fails closed.
func (c *mediaClassifier) episode(ctx context.Context, id string) (parental.Classification, error) {
	if c.s == nil || c.s.tv == nil {
		return parental.Classification{}, errors.New("media-tvshows client unavailable")
	}
	resp, err := c.s.tv.GetEpisode(ctx, &tvmgmtv1.GetEpisodeRequest{EpisodeId: id})
	if err != nil {
		return parental.Classification{}, lookupError("episode", id, err)
	}
	ep := resp.GetEpisode()
	if ep == nil || ep.GetId() != id || strings.TrimSpace(ep.GetSeriesId()) == "" {
		return parental.Classification{}, errClassifyNotFound
	}
	return c.series(ctx, ep.GetSeriesId())
}

// media classifies an id of unstated kind. Exactly one owner may claim it.
func (c *mediaClassifier) media(ctx context.Context, id string) (parental.Classification, error) {
	mc, merr := c.movie(ctx, id)
	ec, eerr := c.episode(ctx, id)
	mFound, eFound := merr == nil, eerr == nil
	for _, err := range []error{merr, eerr} {
		if err != nil && !errors.Is(err, errClassifyNotFound) {
			return parental.Classification{}, err
		}
	}
	switch {
	case mFound && eFound:
		return parental.Classification{}, fmt.Errorf("media id %q is both a movie and an episode: %w", id, errClassifyNotFound)
	case mFound:
		return mc, nil
	case eFound:
		return ec, nil
	}
	return parental.Classification{}, errClassifyNotFound
}

// lookupError maps a module error: NotFound is a missing item, anything else
// is a failed lookup.
func lookupError(kind, id string, err error) error {
	if status.Code(err) == codes.NotFound {
		return errClassifyNotFound
	}
	return fmt.Errorf("classify %s %q: %w", kind, id, err)
}

// playbackClassificationTTL bounds the playback classification cache (ADR-0031
// §4).
const (
	playbackClassificationTTL = 30 * time.Second
	playbackClassificationMax = 4096
)

type classificationCacheEntry struct {
	c       parental.Classification
	expires time.Time
}

// cachingClassifier is the playback classification cache (ADR-0031 §4): keyed
// by item kind and ID, at most 30 s, used only for C-PLAY lookups (HLS
// segments, direct ranges). List, detail and per-item reads never use it. Only
// successful lookups are stored; a lookup error, including a missing item, is
// never cached.
type cachingClassifier struct {
	inner parentalClassifier
	now   func() time.Time
	ttl   time.Duration

	mu sync.Mutex
	m  map[string]classificationCacheEntry
}

func newCachingClassifier(inner parentalClassifier, now func() time.Time) *cachingClassifier {
	return &cachingClassifier{inner: inner, now: now, ttl: playbackClassificationTTL, m: map[string]classificationCacheEntry{}}
}

func classificationCacheKey(item parentalItem) string { return item.Kind + "\x00" + item.ID }

func (c *cachingClassifier) Classify(ctx context.Context, item parentalItem) (parental.Classification, error) {
	key := classificationCacheKey(item)
	now := c.now()
	c.mu.Lock()
	if e, ok := c.m[key]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.c, nil
	}
	c.mu.Unlock()

	cls, err := c.inner.Classify(ctx, item)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		delete(c.m, key)
		return cls, err
	}
	if len(c.m) >= playbackClassificationMax {
		for k, old := range c.m {
			if !now.Before(old.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= playbackClassificationMax {
			return cls, nil // still full of live entries: skip caching, never fail open
		}
	}
	c.m[key] = classificationCacheEntry{c: cls, expires: now.Add(c.ttl)}
	return cls, nil
}
