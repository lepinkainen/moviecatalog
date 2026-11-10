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
	// Token usage of the LLM calls in this step; nil when there were none.
	InputTokens  *int64
	OutputTokens *int64
	CostUSD      *float64 // nil when the model's price is not configured
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
		`INSERT INTO identification_runs (item_id, step, model, request, response, error,
		     input_tokens, output_tokens, cost_usd, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ItemID, run.Step, nullable(run.Model), nullable(run.Request), nullable(run.Response),
		nullable(run.Error), run.InputTokens, run.OutputTokens, run.CostUSD,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("record %s run: %w", run.Step, err)
	}
	return nil
}

// ErrStale is returned when an item was changed by hand, re-queued or deleted
// while it was being identified; the identification result is discarded.
var ErrStale = errors.New("item changed during identification")

// updateCurrent runs an UPDATE that must only apply while the item is still
// queued at the given revision, and returns ErrStale when it no longer is.
func updateCurrent(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, query string, args ...any) error {
	res, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStale
	}
	return nil
}

// SaveIdentification replaces the identification of an item that the worker
// picked up at the given revision. Items that look like a copy of another
// item (same barcode, or same titles in the same format) are sent to review
// along with anything listed in ReviewReasons.
func (s *Store) SaveIdentification(ctx context.Context, itemID, revision int64, ident Identification) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	// Written first so the transaction holds the write lock before anything is read.
	if err := updateCurrent(ctx, tx,
		`UPDATE items SET format = ?, barcode = ?, confidence = ?, notes = ?, checkpoint = NULL
		  WHERE id = ? AND revision = ? AND status = ?`,
		nullable(ident.Format), nullable(ident.Barcode), nullable(ident.Confidence), ident.Notes,
		itemID, revision, StatusCaptured); err != nil {
		if errors.Is(err, ErrStale) {
			return err
		}
		return fmt.Errorf("update item: %w", err)
	}

	titleIDs, allMatched, err := replaceTitles(ctx, tx, itemID, ident.Titles)
	if err != nil {
		return err
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
		`UPDATE items SET status = ?, review_reason = ? WHERE id = ?`,
		status, nullable(strings.Join(reasons, "; ")), itemID); err != nil {
		return fmt.Errorf("update item: %w", err)
	}
	return tx.Commit()
}

// SaveCheckpoint stores the intermediate result of an identification that
// started at the given revision.
func (s *Store) SaveCheckpoint(ctx context.Context, itemID, revision int64, checkpoint string) error {
	err := updateCurrent(ctx, s.db,
		`UPDATE items SET checkpoint = ? WHERE id = ? AND revision = ? AND status = ?`,
		checkpoint, itemID, revision, StatusCaptured)
	if err != nil && !errors.Is(err, ErrStale) {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	return err
}

// Checkpoint returns the stored intermediate result of an item's
// identification, or "" when there is none.
func (s *Store) Checkpoint(ctx context.Context, itemID int64) (string, error) {
	var checkpoint sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT checkpoint FROM items WHERE id = ?`, itemID).Scan(&checkpoint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load checkpoint: %w", err)
	}
	return checkpoint.String, nil
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

// RecordFailure counts a failed identification attempt of an item picked up
// at the given revision. After maxAttempts the item is sent to review instead
// of being retried again; maxAttempts 1 sends it to review straight away.
func (s *Store) RecordFailure(ctx context.Context, itemID, revision int64, reason string, maxAttempts int) error {
	err := updateCurrent(ctx, s.db,
		`UPDATE items SET attempts = attempts + 1,
		        status = CASE WHEN attempts + 1 >= ? THEN ? ELSE status END,
		        review_reason = CASE WHEN attempts + 1 >= ? THEN ? ELSE review_reason END
		  WHERE id = ? AND revision = ? AND status = ?`,
		maxAttempts, StatusNeedsReview, maxAttempts, "identification failed: "+reason,
		itemID, revision, StatusCaptured)
	if err != nil && !errors.Is(err, ErrStale) {
		return fmt.Errorf("record failure: %w", err)
	}
	return err
}

// ResetForIdentification queues an item to be identified again from its photos.
// Any identification already under way for it is discarded.
func (s *Store) ResetForIdentification(ctx context.Context, itemID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE items SET status = ?, attempts = 0, review_reason = NULL, checkpoint = NULL,
		        revision = revision + 1
		  WHERE id = ?`, StatusCaptured, itemID)
	if err != nil {
		return fmt.Errorf("reset item: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RunRecord is a stored identification step, as shown in an item's log.
type RunRecord struct {
	ID           int64    `json:"id"`
	Step         string   `json:"step"`
	Model        *string  `json:"model"`
	Request      *string  `json:"request"`
	Response     *string  `json:"response"`
	Error        *string  `json:"error"`
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd"`
	CreatedAt    string   `json:"created_at"`
}

// RunsCost sums the estimated cost of all of an item's runs, retries included.
func (s *Store) RunsCost(ctx context.Context, itemID int64) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0) FROM identification_runs WHERE item_id = ?`, itemID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("runs cost: %w", err)
	}
	return total, nil
}

// ListRuns returns an item's identification steps, newest first.
func (s *Store) ListRuns(ctx context.Context, itemID int64) ([]RunRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, step, model, request, response, error, input_tokens, output_tokens, cost_usd, created_at
		   FROM identification_runs WHERE item_id = ? ORDER BY id DESC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	runs := []RunRecord{}
	for rows.Next() {
		var r RunRecord
		if err := rows.Scan(&r.ID, &r.Step, &r.Model, &r.Request, &r.Response, &r.Error,
			&r.InputTokens, &r.OutputTokens, &r.CostUSD, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("list runs: %w", err)
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return runs, nil
}
