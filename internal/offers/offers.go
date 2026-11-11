// Package offers looks up shop offers for wanted titles from cdon-watcher.
package offers

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Offer is one shop listing matched to a TMDB movie.
type Offer struct {
	TMDBID int64 `json:"-"`
	// HistoryURL is the watcher's price history page for the listing, when known.
	HistoryURL *string `json:"history_url"`
	StoreKey   string  `json:"store_key"`
	Store      string  `json:"store"`
	Title      string  `json:"title"`
	// Format is in the catalogue's terms: Blu-ray or UHD.
	Format            string `json:"format"`
	Condition         string `json:"condition"` // new, used or unknown
	PriceCents        int64  `json:"price_cents"`
	RegularPriceCents *int64 `json:"regular_price_cents"`
	Currency          string `json:"currency"`
	// Availability is in_stock, supplier_order, preorder, out_of_stock or unknown.
	Availability string    `json:"availability"`
	URL          string    `json:"url"`
	ImageURL     *string   `json:"image_url"`
	SeenAt       time.Time `json:"seen_at"`
}

// InStock is the availability of an offer that can be bought right away.
const InStock = "in_stock"

// Movie is what the watcher knows about one wished-for movie.
type Movie struct {
	// SearchedAt is when the watcher last searched the shops for the movie;
	// nil when it has not yet, or when the source does not track searches.
	SearchedAt *time.Time
	// Offers are the current listings, cheapest first.
	Offers []Offer
}

// Source finds offers for TMDB movie ids.
type Source interface {
	// Lookup returns an entry for every requested movie.
	Lookup(ctx context.Context, tmdbIDs []int64) (map[int64]Movie, error)
	// TracksSearches reports whether Movie.SearchedAt is known. Without it, an
	// empty result can't be told apart from a movie not searched for yet.
	TracksSearches() bool
}

// watcherFormats maps the watcher's format codes to the catalogue's names.
var watcherFormats = map[string]string{"bd": "Blu-ray", "4k": "UHD"}

// Freshness is how recently the watcher must have seen a listing for it to
// count. It matches the watcher's own rule for hiding stale rows.
const Freshness = 48 * time.Hour

// WatcherDB reads offers straight from the watcher's SQLite file, as a
// development fallback for WatcherAPI. It depends on the watcher's schema and
// does not know about searches.
type WatcherDB struct {
	db  *sql.DB
	now func() time.Time
}

// OpenWatcherDB opens the watcher's database read-only. The file is not
// touched until the first lookup.
func OpenWatcherDB(path string) (*WatcherDB, error) {
	q := url.Values{}
	q.Set("mode", "ro")
	// The watcher writes in rollback-journal mode, so a lookup during its import waits for the lock.
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "query_only(1)")
	conn, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open watcher database: %w", err)
	}
	return &WatcherDB{db: conn, now: time.Now}, nil
}

// Close closes the database.
func (w *WatcherDB) Close() error {
	return w.db.Close()
}

// Ping checks that the file opens and has the watcher's product table.
func (w *WatcherDB) Ping(ctx context.Context) error {
	var n int
	if err := w.db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM store_products LIMIT 1)`).Scan(&n); err != nil {
		return fmt.Errorf("watcher database: %w", err)
	}
	return nil
}

// watcherTime is how the watcher stores timestamps: naive UTC.
const watcherTime = "2006-01-02 15:04:05.999999"

// TracksSearches implements Source.
func (w *WatcherDB) TracksSearches() bool { return false }

// Lookup implements Source.
func (w *WatcherDB) Lookup(ctx context.Context, tmdbIDs []int64) (map[int64]Movie, error) {
	found := map[int64]Movie{}
	if len(tmdbIDs) == 0 {
		return found, nil
	}
	for _, id := range tmdbIDs {
		found[id] = Movie{Offers: []Offer{}}
	}
	args := make([]any, 0, len(tmdbIDs)+1)
	args = append(args, w.now().UTC().Add(-Freshness).Format(watcherTime))
	for _, id := range tmdbIDs {
		args = append(args, id)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(tmdbIDs)), ",")

	rows, err := w.db.QueryContext(ctx,
		`SELECT p.tmdb_id, s.key, s.name, p.raw_title, p.format, p.condition,
		        p.current_price_cents, p.current_regular_price_cents, p.currency,
		        p.current_availability, p.edition_url, p.image_url, CAST(p.last_seen_at AS TEXT)
		   FROM store_products p JOIN stores s ON s.id = p.store_id
		  WHERE p.active AND NOT p.is_collection
		    AND p.format IN ('bd', '4k')
		    AND p.last_seen_at >= ?
		    AND p.tmdb_id IN (`+placeholders+`)
		  ORDER BY p.current_price_cents, p.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query watcher offers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var o Offer
		var format, seenAt string
		if err := rows.Scan(&o.TMDBID, &o.StoreKey, &o.Store, &o.Title, &format, &o.Condition,
			&o.PriceCents, &o.RegularPriceCents, &o.Currency, &o.Availability, &o.URL, &o.ImageURL,
			&seenAt); err != nil {
			return nil, fmt.Errorf("query watcher offers: %w", err)
		}
		o.Format = watcherFormats[format]
		t, err := time.Parse(watcherTime, seenAt)
		if err != nil {
			return nil, fmt.Errorf("offer last_seen_at %q: %w", seenAt, err)
		}
		o.SeenAt = t
		m := found[o.TMDBID]
		m.Offers = append(m.Offers, o)
		found[o.TMDBID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query watcher offers: %w", err)
	}
	return found, nil
}
