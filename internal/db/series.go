package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Series is the TMDB collection a movie belongs to.
type Series struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// SeriesName turns a TMDB collection name into a series name:
// "Indiana Jones Collection" → "Indiana Jones".
func SeriesName(collection string) string {
	name := strings.TrimSpace(collection)
	if trimmed := strings.TrimSpace(strings.TrimSuffix(name, " Collection")); trimmed != "" {
		return trimmed
	}
	return name
}

// UncheckedMovie is a matched movie whose TMDB details have not been looked up yet.
type UncheckedMovie struct {
	TitleID int64
	TMDBID  int64
}

// NextUncheckedMovie returns a matched movie whose collection has not been
// looked up yet, or ErrNotFound when there is none.
func (s *Store) NextUncheckedMovie(ctx context.Context) (*UncheckedMovie, error) {
	var m UncheckedMovie
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tmdb_id FROM titles
		  WHERE media_type = ? AND tmdb_id IS NOT NULL AND details_checked_at IS NULL
		  ORDER BY id LIMIT 1`, MediaMovie).Scan(&m.TitleID, &m.TMDBID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("next unchecked movie: %w", err)
	}
	return &m, nil
}

// SaveSeries records the collection of a title (nil when it is in none) and
// marks the title as looked up.
func (s *Store) SaveSeries(ctx context.Context, titleID int64, series *Series, now time.Time) error {
	var id *int64
	var name *string
	if series != nil {
		id, name = &series.ID, &series.Name
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE titles SET collection_id = ?, collection_name = ?, details_checked_at = ? WHERE id = ?`,
		id, name, now.UTC().Format(time.RFC3339), titleID)
	if err != nil {
		return fmt.Errorf("save series: %w", err)
	}
	return nil
}

// OwnedCopy is one disc holding a movie.
type OwnedCopy struct {
	TitleID int64   `json:"-"`
	TMDBID  int64   `json:"-"`
	ItemID  int64   `json:"item_id"`
	Format  *string `json:"format"`
	Status  string  `json:"status"`
}

// OwnedCopies returns the discs holding any of the given TMDB movies, best
// format first. Items still waiting for identification are left out, as in
// the library.
func (s *Store) OwnedCopies(ctx context.Context, tmdbIDs []int64) ([]OwnedCopy, error) {
	if len(tmdbIDs) == 0 {
		return nil, nil
	}
	args := []any{MediaMovie, StatusCaptured}
	for _, id := range tmdbIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.tmdb_id, i.id, i.format, i.status
		   FROM titles t
		   JOIN item_titles it ON it.title_id = t.id
		   JOIN items i ON i.id = it.item_id
		  WHERE t.media_type = ? AND i.status != ?
		    AND t.tmdb_id IN (?`+strings.Repeat(", ?", len(tmdbIDs)-1)+`)
		  ORDER BY CASE i.format WHEN 'UHD' THEN 0 WHEN 'Blu-ray' THEN 1 WHEN 'DVD' THEN 2 ELSE 3 END, i.id`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("owned copies: %w", err)
	}
	defer rows.Close()

	var copies []OwnedCopy
	for rows.Next() {
		var c OwnedCopy
		if err := rows.Scan(&c.TitleID, &c.TMDBID, &c.ItemID, &c.Format, &c.Status); err != nil {
			return nil, fmt.Errorf("owned copies: %w", err)
		}
		copies = append(copies, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("owned copies: %w", err)
	}
	return copies, nil
}

// WantedMovies returns which of the given TMDB movies are on the wishlist and
// not yet collected.
func (s *Store) WantedMovies(ctx context.Context, tmdbIDs []int64) (map[int64]bool, error) {
	wanted := map[int64]bool{}
	if len(tmdbIDs) == 0 {
		return wanted, nil
	}
	args := []any{MediaMovie}
	for _, id := range tmdbIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.tmdb_id FROM wants w JOIN titles t ON t.id = w.title_id
		  WHERE w.collected_on IS NULL AND t.media_type = ? AND t.tmdb_id IN (?`+strings.Repeat(", ?", len(tmdbIDs)-1)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("wanted movies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("wanted movies: %w", err)
		}
		wanted[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wanted movies: %w", err)
	}
	return wanted, nil
}
