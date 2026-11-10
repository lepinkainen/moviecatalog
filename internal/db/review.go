package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// Formats an item can have.
var Formats = []string{"DVD", "Blu-ray", "UHD"}

// ItemEdit is a correction made by hand. Nil fields are left as they are.
type ItemEdit struct {
	Format *string
	Notes  *string
	Titles *[]TitleInput
}

// ApplyReview stores a manual correction and marks the item as identified:
// a person has looked at it, so nothing is left to review. An empty edit
// simply approves the item as it stands.
func (s *Store) ApplyReview(ctx context.Context, itemID int64, edit ItemEdit) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM items WHERE id = ?`, itemID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find item: %w", err)
	}

	if edit.Titles != nil {
		if _, _, err := replaceTitles(ctx, tx, itemID, *edit.Titles); err != nil {
			return err
		}
	}
	if edit.Format != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET format = ? WHERE id = ?`, *edit.Format, itemID); err != nil {
			return fmt.Errorf("update format: %w", err)
		}
	}
	if edit.Notes != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET notes = ? WHERE id = ?`, *edit.Notes, itemID); err != nil {
			return fmt.Errorf("update notes: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET status = ?, review_reason = NULL, revision = revision + 1 WHERE id = ?`,
		StatusIdentified, itemID); err != nil {
		return fmt.Errorf("mark reviewed: %w", err)
	}
	return tx.Commit()
}

// replaceTitles makes titles the complete list of what an item contains.
// It returns the ids of the linked title rows and whether every title has
// a TMDB match.
func replaceTitles(ctx context.Context, tx *sql.Tx, itemID int64, titles []TitleInput) ([]int64, bool, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_titles WHERE item_id = ?`, itemID); err != nil {
		return nil, false, fmt.Errorf("clear titles: %w", err)
	}
	// Titles without a TMDB id belong to one item only; once unlinked they are garbage.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM titles WHERE tmdb_id IS NULL AND id NOT IN (SELECT title_id FROM item_titles)`); err != nil {
		return nil, false, fmt.Errorf("clear unmatched titles: %w", err)
	}

	var ids []int64
	allMatched := len(titles) > 0
	for pos, t := range titles {
		var id int64
		var err error
		if t.TMDBID != nil {
			err = tx.QueryRowContext(ctx,
				`INSERT INTO titles (tmdb_id, media_type, title, original_title, year, poster_path)
				 VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT (media_type, tmdb_id) DO UPDATE SET
				     title = excluded.title, original_title = excluded.original_title,
				     year = excluded.year, poster_path = excluded.poster_path
				 RETURNING id`,
				*t.TMDBID, t.MediaType, t.Title, nullable(t.OriginalTitle), t.Year, t.PosterPath).Scan(&id)
		} else {
			allMatched = false
			err = tx.QueryRowContext(ctx,
				`INSERT INTO titles (media_type, title, original_title, year) VALUES (?, ?, ?, ?) RETURNING id`,
				t.MediaType, t.Title, nullable(t.OriginalTitle), t.Year).Scan(&id)
		}
		if err != nil {
			return nil, false, fmt.Errorf("save title %q: %w", t.Title, err)
		}
		if slices.Contains(ids, id) {
			continue // the same catalogue entry listed twice
		}
		ids = append(ids, id)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_titles (item_id, title_id, position) VALUES (?, ?, ?)`, itemID, id, pos); err != nil {
			return nil, false, fmt.Errorf("link title %q: %w", t.Title, err)
		}
	}
	return ids, allMatched, nil
}
