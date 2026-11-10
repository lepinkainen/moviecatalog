package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Media types of a title.
const (
	MediaMovie = "movie"
	MediaTV    = "tv"
)

// Run is the raw record of one identification step.
type Run struct {
	ItemID   int64
	Step     string
	Model    string
	Request  string
	Response string
	Error    string
}

// TitleInput is a movie or series found on an item. TMDBID is nil when no
// catalogue match was found.
type TitleInput struct {
	TMDBID        *int64
	MediaType     string
	Title         string
	OriginalTitle string
	Year          *int
	PosterPath    *string
}

// Identification is the outcome of the pipeline for one item.
type Identification struct {
	Format        string
	Barcode       string // empty when unreadable
	Confidence    string
	Notes         string
	ReviewReasons []string // non-empty sends the item to review
	Titles        []TitleInput
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NextCaptured returns the oldest item still waiting for identification, or ErrNotFound.
func (s *Store) NextCaptured(ctx context.Context) (*Item, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM items WHERE status = ? ORDER BY id LIMIT 1`, StatusCaptured).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("next captured item: %w", err)
	}
	return s.GetItem(ctx, id)
}

// AddRun stores the raw input and output of one identification step.
func (s *Store) AddRun(ctx context.Context, run Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO identification_runs (item_id, step, model, request, response, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ItemID, run.Step, nullable(run.Model), nullable(run.Request), nullable(run.Response),
		nullable(run.Error), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("record %s run: %w", run.Step, err)
	}
	return nil
}

// SaveIdentification replaces an item's identification. Items that look like
// a copy of another item (same barcode, or same titles in the same format)
// are sent to review along with anything listed in ReviewReasons.
func (s *Store) SaveIdentification(ctx context.Context, itemID int64, ident Identification) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM item_titles WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear titles: %w", err)
	}
	// Unmatched guesses from an earlier run belong to nothing once unlinked.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM titles WHERE tmdb_id IS NULL AND id NOT IN (SELECT title_id FROM item_titles)`); err != nil {
		return fmt.Errorf("clear unmatched titles: %w", err)
	}

	var titleIDs []int64
	allMatched := len(ident.Titles) > 0
	for pos, t := range ident.Titles {
		var id int64
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
			return fmt.Errorf("save title %q: %w", t.Title, err)
		}
		if slices.Contains(titleIDs, id) {
			continue // the same catalogue entry listed twice
		}
		titleIDs = append(titleIDs, id)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_titles (item_id, title_id, position) VALUES (?, ?, ?)`, itemID, id, pos); err != nil {
			return fmt.Errorf("link title %q: %w", t.Title, err)
		}
	}

	reasons := slices.Clone(ident.ReviewReasons)
	dupes, err := findDuplicates(ctx, tx, itemID, ident, titleIDs, allMatched)
	if err != nil {
		return err
	}
	for _, d := range dupes {
		reasons = append(reasons, d)
	}

	status := StatusIdentified
	if len(reasons) > 0 {
		status = StatusNeedsReview
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET status = ?, format = ?, barcode = ?, confidence = ?, notes = ?, review_reason = ?
		 WHERE id = ?`,
		status, nullable(ident.Format), nullable(ident.Barcode), nullable(ident.Confidence), ident.Notes,
		nullable(strings.Join(reasons, "; ")), itemID); err != nil {
		return fmt.Errorf("update item: %w", err)
	}
	return tx.Commit()
}

// findDuplicates describes other items that look like the same release.
func findDuplicates(ctx context.Context, tx *sql.Tx, itemID int64, ident Identification, titleIDs []int64, allMatched bool) ([]string, error) {
	var reasons []string
	seen := map[int64]bool{}

	if ident.Barcode != "" {
		ids, err := queryIDs(ctx, tx, `SELECT id FROM items WHERE barcode = ? AND id != ? ORDER BY id`, ident.Barcode, itemID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = true
			reasons = append(reasons, fmt.Sprintf("same barcode as item #%d", id))
		}
	}

	// Unmatched titles are unique rows, so only fully matched items can be compared.
	if allMatched && ident.Format != "" {
		rows, err := tx.QueryContext(ctx,
			`SELECT it.item_id, it.title_id FROM item_titles it JOIN items i ON i.id = it.item_id
			  WHERE i.format = ? AND i.id != ? ORDER BY it.item_id`, ident.Format, itemID)
		if err != nil {
			return nil, fmt.Errorf("find duplicates: %w", err)
		}
		defer rows.Close()
		others := map[int64][]int64{}
		var order []int64
		for rows.Next() {
			var other, title int64
			if err := rows.Scan(&other, &title); err != nil {
				return nil, fmt.Errorf("find duplicates: %w", err)
			}
			if _, ok := others[other]; !ok {
				order = append(order, other)
			}
			others[other] = append(others[other], title)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("find duplicates: %w", err)
		}
		mine := titleKey(titleIDs)
		for _, other := range order {
			if !seen[other] && titleKey(others[other]) == mine {
				reasons = append(reasons, fmt.Sprintf("same titles and format as item #%d", other))
			}
		}
	}
	return reasons, nil
}

func titleKey(ids []int64) string {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	parts := make([]string, len(sorted))
	for i, id := range sorted {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func queryIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ids: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("query ids: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RecordFailure counts a failed identification attempt. After maxAttempts the
// item is sent to review instead of being retried again.
func (s *Store) RecordFailure(ctx context.Context, itemID int64, reason string, maxAttempts int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE items SET attempts = attempts + 1,
		        status = CASE WHEN attempts + 1 >= ? THEN ? ELSE status END,
		        review_reason = CASE WHEN attempts + 1 >= ? THEN ? ELSE review_reason END
		  WHERE id = ?`,
		maxAttempts, StatusNeedsReview, maxAttempts, "identification failed: "+reason, itemID)
	if err != nil {
		return fmt.Errorf("record failure: %w", err)
	}
	return nil
}

// ResetForIdentification queues an item to be identified again from its photos.
func (s *Store) ResetForIdentification(ctx context.Context, itemID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE items SET status = ?, attempts = 0, review_reason = NULL WHERE id = ?`, StatusCaptured, itemID)
	if err != nil {
		return fmt.Errorf("reset item: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
