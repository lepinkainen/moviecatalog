// Package offers looks up shop offers for wanted titles from shelfwatch.
package offers

import (
	"context"
	"time"
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

// OutOfStock is the availability of a sold-out offer.
const OutOfStock = "out_of_stock"

// Movie is what the watcher knows about one wished-for movie.
type Movie struct {
	// SearchedAt is when the watcher last searched the shops for the movie;
	// nil when it has not yet.
	SearchedAt *time.Time
	// Offers are the current listings, cheapest first.
	Offers []Offer
}

// Source finds offers for TMDB movie ids.
type Source interface {
	// Lookup returns an entry for every requested movie.
	Lookup(ctx context.Context, tmdbIDs []int64) (map[int64]Movie, error)
}

// watcherFormats maps the watcher's format codes to the catalogue's names.
var watcherFormats = map[string]string{"bd": "Blu-ray", "4k": "UHD"}
