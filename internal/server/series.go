package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/identify"
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

type seriesResponse struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	Overview   string       `json:"overview"`
	PosterPath *string      `json:"poster_path"`
	Parts      []seriesPart `json:"parts"`
}

type seriesPart struct {
	TMDBID      int64   `json:"tmdb_id"`
	Title       string  `json:"title"`
	Year        *int    `json:"year"`
	ReleaseDate string  `json:"release_date"`
	PosterPath  *string `json:"poster_path"`
	// Released is false for announced movies, which do not count as missing.
	Released bool `json:"released"`
	// TitleID is the catalogue's title for an owned movie, for linking to the library.
	TitleID *int64         `json:"title_id"`
	Copies  []db.OwnedCopy `json:"copies"`
	// Wanted says the movie is on the wishlist.
	Wanted bool `json:"wanted"`
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

	ids := make([]int64, len(collection.Parts))
	for i, p := range collection.Parts {
		ids[i] = p.ID
	}
	copies, err := s.store.OwnedCopies(r.Context(), ids)
	if err != nil {
		s.log.Error("series copies", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not load the series")
		return
	}
	wanted, err := s.store.WantedMovies(r.Context(), ids)
	if err != nil {
		s.log.Error("series wants", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not load the series")
		return
	}

	today := now.Format("2006-01-02")
	resp := seriesResponse{ID: collection.ID, Name: db.SeriesName(collection.Name), Overview: collection.Overview,
		PosterPath: collection.PosterPath, Parts: make([]seriesPart, 0, len(collection.Parts))}
	for _, p := range collection.Parts {
		part := seriesPart{TMDBID: p.ID, Title: p.Title, Year: p.Year, ReleaseDate: p.ReleaseDate,
			PosterPath: p.PosterPath, Released: p.ReleaseDate != "" && p.ReleaseDate <= today, Copies: []db.OwnedCopy{},
			Wanted: wanted[p.ID]}
		for _, c := range copies {
			if c.TMDBID == p.ID {
				part.Copies = append(part.Copies, c)
				if part.TitleID == nil {
					part.TitleID = &c.TitleID
				}
			}
		}
		resp.Parts = append(resp.Parts, part)
	}
	// TMDB does not keep parts in order. Unannounced dates go last.
	sort.SliceStable(resp.Parts, func(i, j int) bool {
		a, b := resp.Parts[i].ReleaseDate, resp.Parts[j].ReleaseDate
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	s.writeJSON(w, http.StatusOK, resp)
}
