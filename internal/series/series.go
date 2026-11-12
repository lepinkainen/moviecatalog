// Package series joins a TMDB collection with the discs that hold its movies,
// for the series lists in the app and in the static export.
package series

import (
	"context"
	"sort"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/identify"
)

// Series is a TMDB collection with its movies in release order.
type Series struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Overview   string  `json:"overview"`
	PosterPath *string `json:"poster_path"`
	Parts      []Part  `json:"parts"`
}

// Part is one movie of a series.
type Part struct {
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

// TMDBIDs returns the TMDB ids of a collection's movies.
func TMDBIDs(c *identify.Collection) []int64 {
	ids := make([]int64, len(c.Parts))
	for i, p := range c.Parts {
		ids[i] = p.ID
	}
	return ids
}

// Build returns a collection with the discs holding each movie and whether it
// is on the wishlist.
func Build(ctx context.Context, store *db.Store, c *identify.Collection, now time.Time) (Series, error) {
	ids := TMDBIDs(c)
	copies, err := store.OwnedCopies(ctx, ids)
	if err != nil {
		return Series{}, err
	}
	wanted, err := store.WantedMovies(ctx, ids)
	if err != nil {
		return Series{}, err
	}
	return Assemble(c, copies, wanted, now), nil
}

// Assemble joins a collection with the given discs (best format first) and
// wishes. wanted may be nil.
func Assemble(c *identify.Collection, copies []db.OwnedCopy, wanted map[int64]bool, now time.Time) Series {
	today := now.Format("2006-01-02")
	s := Series{ID: c.ID, Name: db.SeriesName(c.Name), Overview: c.Overview, PosterPath: c.PosterPath,
		Parts: make([]Part, 0, len(c.Parts))}
	for _, p := range c.Parts {
		part := Part{TMDBID: p.ID, Title: p.Title, Year: p.Year, ReleaseDate: p.ReleaseDate,
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
		s.Parts = append(s.Parts, part)
	}
	// TMDB does not keep parts in order. Unannounced dates go last.
	sort.SliceStable(s.Parts, func(i, j int) bool {
		a, b := s.Parts[i].ReleaseDate, s.Parts[j].ReleaseDate
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	return s
}
