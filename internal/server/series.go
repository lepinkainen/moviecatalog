package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/identify"
	"github.com/lepinkainen/moviecatalog/internal/series"
)

// tmdbCacheTTL is how long a TMDB collection or series is reused. They
// change rarely: a sequel or season is announced or gets its release date.
const tmdbCacheTTL = 24 * time.Hour

// tmdbCache keeps fetched TMDB collections or series in memory.
type tmdbCache[T any] struct {
	mu      sync.Mutex
	entries map[int64]tmdbCacheEntry[T]
}

type tmdbCacheEntry[T any] struct {
	value     T
	fetchedAt time.Time
}

func (c *tmdbCache[T]) get(ctx context.Context, id int64, now time.Time, fetch func(context.Context, int64) (T, error)) (T, error) {
	c.mu.Lock()
	e, ok := c.entries[id]
	c.mu.Unlock()
	if ok && now.Sub(e.fetchedAt) < tmdbCacheTTL {
		return e.value, nil
	}
	value, err := fetch(ctx, id)
	if err != nil {
		return value, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[int64]tmdbCacheEntry[T]{}
	}
	c.entries[id] = tmdbCacheEntry[T]{value, now}
	c.mu.Unlock()
	return value, nil
}

// handleGetSeries returns a TMDB collection with the movies in release order,
// each with the discs it is on.
func (s *Server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if s.tmdb == nil {
		s.writeError(w, http.StatusServiceUnavailable, "series need TMDB (TMDB_API_KEY is not set)")
		return
	}
	now := time.Now()
	collection, err := s.series.get(r.Context(), id, now, s.tmdb.Collection)
	if errors.Is(err, identify.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "series not found")
		return
	}
	if err != nil {
		s.log.Error("tmdb collection", "id", id, "error", err)
		s.writeError(w, http.StatusBadGateway, "could not load the series from TMDB")
		return
	}

	resp, err := series.Build(r.Context(), s.store, collection, now)
	if err != nil {
		s.log.Error("series discs", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not load the series")
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// handleGetShow returns a TMDB series with its seasons in order, each with
// the discs it is on.
func (s *Server) handleGetShow(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if s.tmdb == nil {
		s.writeError(w, http.StatusServiceUnavailable, "seasons need TMDB (TMDB_API_KEY is not set)")
		return
	}
	now := time.Now()
	show, err := s.shows.get(r.Context(), id, now, s.tmdb.Show)
	if errors.Is(err, identify.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "series not found")
		return
	}
	if err != nil {
		s.log.Error("tmdb tv", "id", id, "error", err)
		s.writeError(w, http.StatusBadGateway, "could not load the series from TMDB")
		return
	}

	resp, err := series.BuildShow(r.Context(), s.store, show, now)
	if err != nil {
		s.log.Error("show discs", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not load the series")
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}
