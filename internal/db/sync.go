package db

import (
	"context"
	"fmt"
)

// OwnedMovie is a movie on the shelf, with the formats of the discs holding it.
type OwnedMovie struct {
	TMDBID  int64    `json:"tmdb_id"`
	Formats []string `json:"formats"`
}

// ListOwnedMovies returns every movie with a TMDB match that is on at least
// one disc with a known format. Items still waiting for identification have no
// format and are left out; items in review are included, since their format
// and match are usually right.
func (s *Store) ListOwnedMovies(ctx context.Context) ([]OwnedMovie, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.tmdb_id, group_concat(DISTINCT i.format)
		   FROM titles t
		   JOIN item_titles it ON it.title_id = t.id
		   JOIN items i ON i.id = it.item_id
		  WHERE t.media_type = ? AND t.tmdb_id IS NOT NULL AND i.format IS NOT NULL
		  GROUP BY t.tmdb_id
		  ORDER BY t.tmdb_id`, MediaMovie)
	if err != nil {
		return nil, fmt.Errorf("list owned movies: %w", err)
	}
	defer rows.Close()

	owned := []OwnedMovie{}
	for rows.Next() {
		var m OwnedMovie
		var formats string
		if err := rows.Scan(&m.TMDBID, &formats); err != nil {
			return nil, fmt.Errorf("list owned movies: %w", err)
		}
		m.Formats = splitList(formats)
		owned = append(owned, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list owned movies: %w", err)
	}
	return owned, nil
}
