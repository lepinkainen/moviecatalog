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

// seriesCacheTTL is how long a TMDB collection is reused. Collections change
// rarely: a sequel is announced or gets its release date.
const seriesCacheTTL = 24 * time.Hour

// seriesCache keeps fetched TMDB collections in memory.
type seriesCache struct {
	mu      sync.Mutex
	entries map[int64]seriesCacheEntry
}

type seriesCacheEntry struct {
	collection *identify.Collection
	fetchedAt  time.Time
}

func (c *seriesCache) get(ctx context.Context, tmdb *identify.TMDB, id int64, now time.Time) (*identify.Collection, error) {
	c.mu.Lock()
	e, ok := c.entries[id]
	c.mu.Unlock()
	if ok && now.Sub(e.fetchedAt) < seriesCacheTTL {
		return e.collection, nil
	}
	collection, err := tmdb.Collection(ctx, id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[int64]seriesCacheEntry{}
	}
	c.entries[id] = seriesCacheEntry{collection, now}
	c.mu.Unlock()
	return collection, nil
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
	collection, err := s.series.get(r.Context(), s.tmdb, id, now)
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
