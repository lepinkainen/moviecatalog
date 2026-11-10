package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Item statuses.
const (
	StatusCaptured    = "captured"
	StatusIdentified  = "identified"
	StatusNeedsReview = "needs_review"
)

// Image kinds.
const (
	KindFront = "front"
	KindBack  = "back"
)

// Store provides access to the catalogue database.
type Store struct {
	db *sql.DB
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Item is one physical thing on the shelf.
type Item struct {
	ID         int64     `json:"id"`
	CapturedAt time.Time `json:"captured_at"`
	Status     string    `json:"status"`
	Format     *string   `json:"format"`
	Barcode    *string   `json:"barcode"`
	Notes      string    `json:"notes"`
	Images     []Image   `json:"images"`
}

// Image is a stored photo of an item.
type Image struct {
	ID          int64  `json:"id"`
	ItemID      int64  `json:"-"`
	Kind        string `json:"kind"`
	Path        string `json:"-"` // relative to the data directory
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

// NewImage describes a photo already written to disk, to be recorded for a new item.
type NewImage struct {
	Kind        string
	Path        string
	ContentType string
}

// CreateItem inserts a new captured item. save is called with the new item's
// id while the transaction is open and must write the photos to disk; if it
// fails nothing is recorded.
func (s *Store) CreateItem(ctx context.Context, now time.Time, save func(itemID int64) ([]NewImage, error)) (*Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO items (captured_at, status) VALUES (?, ?)`,
		now.UTC().Format(time.RFC3339), StatusCaptured)
	if err != nil {
		return nil, fmt.Errorf("insert item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("item id: %w", err)
	}

	images, err := save(id)
	if err != nil {
		return nil, err
	}
	for _, img := range images {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO images (item_id, kind, path, content_type) VALUES (?, ?, ?, ?)`,
			id, img.Kind, img.Path, img.ContentType); err != nil {
			return nil, fmt.Errorf("insert image: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.GetItem(ctx, id)
}

const itemColumns = `id, captured_at, status, format, barcode, notes`

func scanItem(row interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var capturedAt string
	if err := row.Scan(&it.ID, &capturedAt, &it.Status, &it.Format, &it.Barcode, &it.Notes); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, capturedAt)
	if err != nil {
		return nil, fmt.Errorf("item %d captured_at: %w", it.ID, err)
	}
	it.CapturedAt = t
	it.Images = []Image{}
	return &it, nil
}

// GetItem returns one item with its images.
func (s *Store) GetItem(ctx context.Context, id int64) (*Item, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx,
		`SELECT `+itemColumns+` FROM items WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get item: %w", err)
	}
	images, err := s.queryImages(ctx, `WHERE item_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	it.Images = append(it.Images, images...)
	return it, nil
}

// ListItems returns all items, newest first, with their images.
func (s *Store) ListItems(ctx context.Context) ([]*Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM items ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	items := []*Item{}
	byID := map[int64]*Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("list items: %w", err)
		}
		items = append(items, it)
		byID[it.ID] = it
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}

	images, err := s.queryImages(ctx, `ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for _, img := range images {
		if it, ok := byID[img.ItemID]; ok {
			it.Images = append(it.Images, img)
		}
	}
	return items, nil
}

// DeleteItem removes an item and everything attached to it.
func (s *Store) DeleteItem(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetImage returns one image record.
func (s *Store) GetImage(ctx context.Context, id int64) (*Image, error) {
	images, err := s.queryImages(ctx, `WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return nil, ErrNotFound
	}
	return &images[0], nil
}

func (s *Store) queryImages(ctx context.Context, clause string, args ...any) ([]Image, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, item_id, kind, path, content_type FROM images `+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("query images: %w", err)
	}
	defer rows.Close()

	var images []Image
	for rows.Next() {
		var img Image
		if err := rows.Scan(&img.ID, &img.ItemID, &img.Kind, &img.Path, &img.ContentType); err != nil {
			return nil, fmt.Errorf("query images: %w", err)
		}
		img.URL = fmt.Sprintf("/api/images/%d", img.ID)
		images = append(images, img)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query images: %w", err)
	}
	return images, nil
}
