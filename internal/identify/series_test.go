package identify

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

// seriesFixture saves movies by hand, as the review form does, and returns a
// finder that looks them up through the fake TMDB.
func seriesFixture(t *testing.T, movies map[int64]string) (*fixture, *SeriesFinder) {
	t.Helper()
	fx := newFixture(t)
	ctx := context.Background()
	for tmdbID, title := range movies {
		item, err := fx.store.CreateItem(ctx, time.Now(), func(int64) ([]db.NewImage, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		titles := []db.TitleInput{{TMDBID: &tmdbID, MediaType: db.MediaMovie, Title: title}}
		if err := fx.store.ApplyReview(ctx, item.ID, db.ItemEdit{Titles: &titles}); err != nil {
			t.Fatal(err)
		}
	}
	return fx, NewSeriesFinder(fx.store, fx.worker.tmdb, fx.worker.log)
}

func (fx *fixture) seriesOf(t *testing.T, tmdbID int64) *db.Series {
	t.Helper()
	items, err := fx.store.ListItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		for _, ti := range it.Titles {
			if ti.TMDBID != nil && *ti.TMDBID == tmdbID {
				return ti.Series
			}
		}
	}
	t.Fatalf("movie %d not in the catalogue", tmdbID)
	return nil
}

func TestSeriesFinder(t *testing.T) {
	fx, finder := seriesFixture(t, map[int64]string{85: "Raiders of the Lost Ark", 680: "Pulp Fiction", 1: "Gone"})
	fx.fakes.collections = map[string]any{
		"85":  map[string]any{"id": 84, "name": "Indiana Jones Collection", "poster_path": "/ij.jpg"},
		"680": nil,
	}
	ctx := context.Background()
	for range 3 {
		if processed, err := finder.ProcessNext(ctx); !processed || err != nil {
			t.Fatalf("ProcessNext = %v, %v", processed, err)
		}
	}
	if processed, err := finder.ProcessNext(ctx); processed || err != nil {
		t.Fatalf("after all are checked: ProcessNext = %v, %v; want nothing to do", processed, err)
	}

	if s := fx.seriesOf(t, 85); s == nil || s.ID != 84 || s.Name != "Indiana Jones" {
		t.Errorf("Raiders series = %+v, want Indiana Jones (84)", s)
	}
	if s := fx.seriesOf(t, 680); s != nil {
		t.Errorf("Pulp Fiction series = %+v, want none", s)
	}
	if s := fx.seriesOf(t, 1); s != nil {
		t.Errorf("series of a movie TMDB does not know = %+v, want none", s)
	}
}

func TestSeriesFinderRetriesAfterFailure(t *testing.T) {
	fx, finder := seriesFixture(t, map[int64]string{85: "Raiders of the Lost Ark"})
	fx.fakes.collections = map[string]any{"85": map[string]any{"id": 84, "name": "Indiana Jones Collection"}}
	ctx := context.Background()

	for _, status := range []int{http.StatusServiceUnavailable, http.StatusUnauthorized} {
		fx.fakes.movieStatus = status
		processed, err := finder.ProcessNext(ctx)
		if !processed || err == nil {
			t.Fatalf("status %d: ProcessNext = %v, %v; want an error", status, processed, err)
		}
		if transient := isTransient(err); transient != (status == http.StatusServiceUnavailable) {
			t.Errorf("status %d: transient = %v", status, transient)
		}
	}

	fx.fakes.movieStatus = 0
	if processed, err := finder.ProcessNext(ctx); !processed || err != nil {
		t.Fatalf("after recovery: ProcessNext = %v, %v", processed, err)
	}
	if s := fx.seriesOf(t, 85); s == nil || s.ID != 84 {
		t.Errorf("series = %+v, want 84 once TMDB answers", s)
	}
}

func TestSeriesName(t *testing.T) {
	for in, want := range map[string]string{
		"Indiana Jones Collection": "Indiana Jones",
		"Alien Anthology":          "Alien Anthology",
		"Collection":               "Collection",
	} {
		if got := db.SeriesName(in); got != want {
			t.Errorf("SeriesName(%q) = %q, want %q", in, got, want)
		}
	}
}
