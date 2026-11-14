package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WantFormats are the formats a wish can accept: the ones the shop watcher tracks.
var WantFormats = []string{"Blu-ray", "UHD"}

// ErrExists is returned when creating something that is already there.
var ErrExists = errors.New("already exists")

// Want is a wishlist entry.
type Want struct {
	ID    int64 `json:"id"`
	Title Title `json:"title"`
	// Season is the one season of a series wished for (a season box); nil
	// for a movie or the whole series.
	Season        *int      `json:"season"`
	Formats       []string  `json:"formats"`
	MaxPriceCents *int64    `json:"max_price_cents"`
	Notes         string    `json:"notes"`
	CreatedAt     time.Time `json:"created_at"`
	// OwnedFormats are the formats of the discs already holding this title
	// (or, for a season wish, this season of it).
	OwnedFormats []string `json:"owned_formats"`
	// CollectedOn is the day a captured disc fulfilled the wish (YYYY-MM-DD),
	// or nil while it is still wanted.
	CollectedOn *string `json:"collected_on"`
}

// WantEdit holds the fields of a want that can be changed.
type WantEdit struct {
	Formats       []string
	MaxPriceCents *int64
	Notes         string
}

// CreateWant adds a title, or one season of a series, to the wishlist. The
// title must have a TMDB id. It returns ErrExists when it is already wished
// for. Wishing again for a collected one (an upgrade, say) makes its wish a
// new one.
func (s *Store) CreateWant(ctx context.Context, title TitleInput, season *int, edit WantEdit, now time.Time) (*Want, error) {
	if title.TMDBID == nil {
		return nil, errors.New("a wanted title needs a TMDB id")
	}
	if season != nil && title.MediaType != MediaTV {
		return nil, errors.New("only a series has seasons")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	titleID, err := upsertMatchedTitle(ctx, tx, title)
	if err != nil {
		return nil, fmt.Errorf("save title %q: %w", title.Title, err)
	}
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO wants (title_id, season, formats, max_price_cents, notes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (title_id, IFNULL(season, -1)) DO UPDATE SET
		     formats = excluded.formats, max_price_cents = excluded.max_price_cents,
		     notes = excluded.notes, created_at = excluded.created_at, collected_on = NULL
		   WHERE wants.collected_on IS NOT NULL
		 RETURNING id`,
		titleID, season, strings.Join(edit.Formats, ","), edit.MaxPriceCents, edit.Notes,
		now.UTC().Format(time.RFC3339)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExists
	}
	if err != nil {
		return nil, fmt.Errorf("insert want: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.GetWant(ctx, id)
}

// UpdateWant replaces the editable fields of a want.
func (s *Store) UpdateWant(ctx context.Context, id int64, edit WantEdit) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE wants SET formats = ?, max_price_cents = ?, notes = ? WHERE id = ?`,
		strings.Join(edit.Formats, ","), edit.MaxPriceCents, edit.Notes, id)
	if err != nil {
		return fmt.Errorf("update want: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWant removes a title from the wishlist. The title itself is kept.
func (s *Store) DeleteWant(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM wants WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete want: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetWant returns one want.
func (s *Store) GetWant(ctx context.Context, id int64) (*Want, error) {
	wants, err := s.queryWants(ctx, `WHERE w.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(wants) == 0 {
		return nil, ErrNotFound
	}
	return wants[0], nil
}

// ListWants returns the whole wishlist, newest first, collected wishes included.
func (s *Store) ListWants(ctx context.Context) ([]*Want, error) {
	return s.queryWants(ctx, `ORDER BY w.id DESC`)
}

func (s *Store) queryWants(ctx context.Context, clause string, args ...any) ([]*Want, error) {
	// Discs still waiting for identification have no format yet and are left out.
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.season, w.formats, w.max_price_cents, w.notes, w.created_at, w.collected_on,
		        t.id, t.tmdb_id, t.media_type, t.title, t.original_title, t.year, t.poster_path,
		        (SELECT group_concat(DISTINCT i.format)
		           FROM item_titles it JOIN items i ON i.id = it.item_id
		          WHERE it.title_id = t.id AND i.format IS NOT NULL
		            AND (w.season IS NULL OR EXISTS (SELECT 1 FROM item_title_seasons s
		                  WHERE s.item_id = it.item_id AND s.title_id = it.title_id AND s.season = w.season)))
		   FROM wants w JOIN titles t ON t.id = w.title_id `+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("query wants: %w", err)
	}
	defer rows.Close()

	wants := []*Want{}
	for rows.Next() {
		var w Want
		var formats, createdAt string
		var owned sql.NullString
		if err := rows.Scan(&w.ID, &w.Season, &formats, &w.MaxPriceCents, &w.Notes, &createdAt, &w.CollectedOn,
			&w.Title.ID, &w.Title.TMDBID, &w.Title.MediaType, &w.Title.Title, &w.Title.OriginalTitle,
			&w.Title.Year, &w.Title.PosterPath, &owned); err != nil {
			return nil, fmt.Errorf("query wants: %w", err)
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("want %d created_at: %w", w.ID, err)
		}
		w.CreatedAt = t
		w.Formats = splitList(formats)
		w.OwnedFormats = splitList(owned.String)
		wants = append(wants, &w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query wants: %w", err)
	}
	return wants, nil
}

func splitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// collectWants marks the wishes for the titles on an identified item as
// collected on the day the disc was captured: buying the disc fulfils them,
// whatever its format. A season wish needs a disc holding that season. Only
// wishes made before the disc was captured count, so a later wish (an upgrade
// of a disc already owned) survives edits to, or re-identification of, the
// old disc.
func collectWants(ctx context.Context, tx *sql.Tx, itemID int64) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE wants SET collected_on = (SELECT date(captured_at, 'localtime') FROM items WHERE id = ?)
		  WHERE collected_on IS NULL
		    AND title_id IN (SELECT title_id FROM item_titles WHERE item_id = ?)
		    AND (season IS NULL OR season IN (SELECT season FROM item_title_seasons
		          WHERE item_id = ? AND title_id = wants.title_id))
		    AND created_at <= (SELECT captured_at FROM items WHERE id = ?)`, itemID, itemID, itemID, itemID)
	if err != nil {
		return fmt.Errorf("collect wants: %w", err)
	}
	return nil
}
