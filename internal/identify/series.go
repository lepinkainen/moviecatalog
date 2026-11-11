package identify

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

// seriesErrorDelay spaces out lookups after a failure that is not an outage,
// such as a rejected API key, so it is logged now and then rather than in a loop.
const seriesErrorDelay = 10 * time.Minute

// SeriesFinder looks up which TMDB collection each matched movie belongs to,
// in the background. It covers titles saved by the worker, by hand and from
// the wishlist alike, and fills in titles saved before series existed.
type SeriesFinder struct {
	store *db.Store
	tmdb  *TMDB
	log   *slog.Logger
	wake  chan struct{}
}

// NewSeriesFinder creates a series finder.
func NewSeriesFinder(store *db.Store, tmdb *TMDB, log *slog.Logger) *SeriesFinder {
	return &SeriesFinder{store: store, tmdb: tmdb, log: log, wake: make(chan struct{}, 1)}
}

// Notify tells the finder there may be new titles to look up.
func (f *SeriesFinder) Notify() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Run looks up titles until ctx is cancelled.
func (f *SeriesFinder) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := f.ProcessNext(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && isTransient(err):
			f.log.Warn("series lookup failed, retrying", "error", err)
			sleep(ctx, retryDelay)
		case err != nil:
			f.log.Error("series lookup failed", "error", err)
			sleep(ctx, seriesErrorDelay)
		case !processed:
			select {
			case <-ctx.Done():
			case <-f.wake:
			}
		}
	}
}

// ProcessNext looks up the series of one movie. It reports whether there was one.
func (f *SeriesFinder) ProcessNext(ctx context.Context) (bool, error) {
	m, err := f.store.NextUncheckedMovie(ctx)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	series, err := f.tmdb.Movie(ctx, m.TMDBID)
	if errors.Is(err, ErrNotFound) {
		// Removed from TMDB: there is no series to find, now or later.
		series, err = nil, nil
	}
	if err != nil {
		return true, err
	}
	if err := f.store.SaveSeries(ctx, m.TitleID, series, time.Now()); err != nil {
		return true, err
	}
	if series != nil {
		f.log.Info("series found", "tmdb_id", m.TMDBID, "series", db.SeriesName(series.Name))
	}
	return true, nil
}
