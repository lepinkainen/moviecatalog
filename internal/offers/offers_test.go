package offers

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// watcherSchema is the part of cdon-watcher's schema that WatcherDB reads.
const watcherSchema = `
CREATE TABLE stores (id INTEGER PRIMARY KEY, "key" VARCHAR NOT NULL, name VARCHAR NOT NULL);
CREATE TABLE store_products (
	id INTEGER PRIMARY KEY, store_id INTEGER NOT NULL, raw_title VARCHAR NOT NULL,
	edition_url VARCHAR NOT NULL, format VARCHAR NOT NULL, condition VARCHAR NOT NULL,
	image_url VARCHAR, is_collection BOOLEAN NOT NULL, tmdb_id INTEGER,
	current_price_cents INTEGER NOT NULL, current_regular_price_cents INTEGER,
	currency VARCHAR NOT NULL, current_availability VARCHAR NOT NULL,
	last_seen_at DATETIME NOT NULL, active BOOLEAN NOT NULL);
INSERT INTO stores VALUES (1, 'ax', 'Levykauppa Äx'), (2, 'cdon', 'CDON');
`

func TestWatcherDBOffers(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	fresh := "2026-11-11 11:46:17.377763"
	stale := "2026-11-08 11:00:00.000000"

	path := filepath.Join(t.TempDir(), "catalog.db")
	setup, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(watcherSchema); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id, store          int
		title, format      string
		collection, active bool
		tmdb               any
		price              int
		regular            any
		availability, seen string
	}{
		{1, 1, "Up 4K", "4k", false, true, 14160, 1999, 2499, "in_stock", fresh},
		{2, 2, "Up", "bd", false, true, 14160, 899, nil, "in_stock", fresh},
		{3, 1, "Up (stale)", "bd", false, true, 14160, 500, nil, "in_stock", stale},
		{4, 1, "Up (gone)", "bd", false, false, 14160, 500, nil, "in_stock", fresh},
		{5, 1, "Pixar box", "bd", true, true, 14160, 500, nil, "in_stock", fresh},
		{6, 1, "Up DVD", "dvd", false, true, 14160, 300, nil, "in_stock", fresh},
		{7, 1, "Coco", "bd", false, true, 354912, 1299, nil, "supplier_order", fresh},
		{8, 1, "Unmatched", "bd", false, true, nil, 100, nil, "in_stock", fresh},
	}
	for _, r := range rows {
		if _, err := setup.Exec(`INSERT INTO store_products VALUES (?, ?, ?, ?, ?, 'new', NULL, ?, ?, ?, ?, 'EUR', ?, ?, ?)`,
			r.id, r.store, r.title, "https://example.com/"+r.title, r.format, r.collection, r.tmdb,
			r.price, r.regular, r.availability, r.seen, r.active); err != nil {
			t.Fatal(err)
		}
	}
	setup.Close()

	src, err := OpenWatcherDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	src.now = func() time.Time { return now }

	got, err := src.Lookup(context.Background(), []int64{14160, 354912, 999})
	if err != nil {
		t.Fatal(err)
	}
	up := got[14160].Offers
	if len(up) != 2 {
		t.Fatalf("Up offers = %+v, want the two fresh, active, single-movie Blu-ray/UHD listings", up)
	}
	if up[0].Title != "Up" || up[0].Format != "Blu-ray" || up[0].PriceCents != 899 || up[0].Store != "CDON" {
		t.Errorf("cheapest Up offer = %+v, want CDON's Blu-ray at 899", up[0])
	}
	if up[1].Format != "UHD" || up[1].RegularPriceCents == nil || *up[1].RegularPriceCents != 2499 {
		t.Errorf("second Up offer = %+v, want the UHD with its regular price", up[1])
	}
	if want := time.Date(2026, 11, 11, 11, 46, 17, 377763000, time.UTC); !up[0].SeenAt.Equal(want) {
		t.Errorf("seen at = %v, want %v", up[0].SeenAt, want)
	}
	if coco := got[354912].Offers; len(coco) != 1 || coco[0].Availability != "supplier_order" {
		t.Errorf("Coco offers = %+v, want one supplier-order listing", coco)
	}
	if m, ok := got[999]; !ok || len(m.Offers) != 0 || m.SearchedAt != nil {
		t.Errorf("movie no shop lists = %+v (present %v), want an empty entry", m, ok)
	}

	if err := src.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}

	// The database is opened read-only.
	if _, err := src.db.Exec(`DELETE FROM store_products`); err == nil {
		t.Errorf("write to the watcher database succeeded, want it rejected")
	}
}

func TestWatcherDBMissingFile(t *testing.T) {
	src, err := OpenWatcherDB(filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.Ping(context.Background()); err == nil {
		t.Errorf("Ping of a missing database succeeded, want an error")
	}
	if _, err := src.Lookup(context.Background(), []int64{1}); err == nil {
		t.Errorf("lookup in a missing database succeeded, want an error")
	}
}
