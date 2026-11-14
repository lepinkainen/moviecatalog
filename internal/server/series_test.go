package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/identify"
	"github.com/lepinkainen/moviecatalog/internal/series"
)

func TestSeries(t *testing.T) {
	fetches := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collection/84" {
			http.NotFound(w, r)
			return
		}
		fetches++
		// Out of order, as TMDB returns them, with an unannounced sequel.
		json.NewEncoder(w).Encode(map[string]any{
			"id": 84, "name": "Indiana Jones Collection", "overview": "Adventures.",
			"parts": []map[string]any{
				{"id": 89, "title": "Indiana Jones and the Last Crusade", "release_date": "1989-05-24"},
				{"id": 999, "title": "Indiana Jones 6", "release_date": ""},
				{"id": 85, "title": "Raiders of the Lost Ark", "release_date": "1981-06-12", "poster_path": "/r.jpg"},
				{"id": 87, "title": "Indiana Jones and the Temple of Doom", "release_date": "1984-05-23"},
			},
		})
	}))
	t.Cleanup(fake.Close)

	dataDir := t.TempDir()
	store, err := db.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, dataDir, testUI, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetTMDB(&identify.TMDB{APIKey: "k", BaseURL: fake.URL, HTTP: fake.Client()})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Raiders on DVD and UHD, Last Crusade still waiting for identification.
	ctx := context.Background()
	own := func(tmdbID int64, title, format string) {
		item, err := store.CreateItem(ctx, time.Now(), func(int64) ([]db.NewImage, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		if format == "" {
			return
		}
		titles := []db.TitleInput{{TMDBID: &tmdbID, MediaType: db.MediaMovie, Title: title}}
		if err := store.ApplyReview(ctx, item.ID, db.ItemEdit{Format: &format, Titles: &titles}); err != nil {
			t.Fatal(err)
		}
	}
	own(85, "Raiders of the Lost Ark", "DVD")
	own(85, "Raiders of the Lost Ark", "UHD")
	own(89, "Indiana Jones and the Last Crusade", "")
	wish := `{"title": {"tmdb_id": 87, "media_type": "movie", "title": "Indiana Jones and the Temple of Doom"}}`
	if resp := send(t, http.MethodPost, ts.URL+"/api/wants", wish); resp.StatusCode != http.StatusCreated {
		t.Fatalf("wish: status = %d", resp.StatusCode)
	}

	got := decode[series.Series](t, get(t, ts.URL+"/api/series/84"))
	if got.Name != "Indiana Jones" || got.Overview != "Adventures." {
		t.Errorf("series = %q (%q), want Indiana Jones", got.Name, got.Overview)
	}
	order := []int64{85, 87, 89, 999}
	if len(got.Parts) != len(order) {
		t.Fatalf("parts = %+v, want %d", got.Parts, len(order))
	}
	for i, p := range got.Parts {
		if p.TMDBID != order[i] {
			t.Errorf("part %d = %d, want %d (release order, unannounced last)", i, p.TMDBID, order[i])
		}
	}
	raiders := got.Parts[0]
	if raiders.TitleID == nil || len(raiders.Copies) != 2 || *raiders.Copies[0].Format != "UHD" {
		t.Errorf("Raiders = %+v, want two copies, UHD first", raiders)
	}
	if raiders.Year == nil || *raiders.Year != 1981 || !raiders.Released {
		t.Errorf("Raiders year = %v, released = %v", raiders.Year, raiders.Released)
	}
	for i, p := range got.Parts {
		if p.Wanted != (p.TMDBID == 87) {
			t.Errorf("part %d (%d): wanted = %v, want only Temple of Doom", i, p.TMDBID, p.Wanted)
		}
	}
	if crusade := got.Parts[2]; crusade.TitleID != nil || len(crusade.Copies) != 0 {
		t.Errorf("Last Crusade = %+v, want not owned (its disc is not identified)", crusade)
	}
	if sequel := got.Parts[3]; sequel.Released || sequel.Year != nil {
		t.Errorf("unannounced sequel = %+v, want unreleased without a year", sequel)
	}

	get(t, ts.URL+"/api/series/84")
	if fetches != 1 {
		t.Errorf("TMDB fetched %d times, want the cached collection reused", fetches)
	}
	if resp := get(t, ts.URL+"/api/series/1"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown series: status = %d, want 404", resp.StatusCode)
	}
}

func TestSeriesWithoutKey(t *testing.T) {
	ts, _ := newTestServer(t)
	if resp := get(t, ts.URL+"/api/series/84"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when no TMDB key is configured", resp.StatusCode)
	}
}

func TestShow(t *testing.T) {
	fetches := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1432" {
			http.NotFound(w, r)
			return
		}
		fetches++
		json.NewEncoder(w).Encode(map[string]any{
			"id": 1432, "name": "Veronica Mars", "status": "Ended",
			"seasons": []map[string]any{
				{"season_number": 0, "name": "Specials", "air_date": "2004-09-01", "episode_count": 2},
				{"season_number": 1, "name": "Season 1", "air_date": "2004-09-22", "episode_count": 22},
				{"season_number": 2, "name": "Season 2", "air_date": "2005-09-28", "episode_count": 22},
				{"season_number": 3, "name": "Season 3", "air_date": "2006-10-03", "episode_count": 20},
				{"season_number": 5, "name": "Season 5", "air_date": "", "episode_count": 0},
			},
		})
	}))
	t.Cleanup(fake.Close)

	dataDir := t.TempDir()
	store, err := db.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, dataDir, testUI, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetTMDB(&identify.TMDB{APIKey: "k", BaseURL: fake.URL, HTTP: fake.Client()})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx := context.Background()
	own := func(format string, seasons ...int) int64 {
		item, err := store.CreateItem(ctx, time.Now(), func(int64) ([]db.NewImage, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		id := int64(1432)
		titles := []db.TitleInput{{TMDBID: &id, MediaType: db.MediaTV, Title: "Veronica Mars", Seasons: seasons}}
		if err := store.ApplyReview(ctx, item.ID, db.ItemEdit{Format: &format, Titles: &titles}); err != nil {
			t.Fatal(err)
		}
		return item.ID
	}
	s1 := own("DVD", 1)
	s3 := own("DVD", 3, 4)
	unsorted := own("Blu-ray")
	vm, two := int64(1432), 2
	if _, err := store.CreateWant(ctx, db.TitleInput{TMDBID: &vm, MediaType: db.MediaTV, Title: "Veronica Mars"}, &two,
		db.WantEdit{Formats: db.WantFormats}, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := decode[series.Show](t, get(t, ts.URL+"/api/shows/1432"))
	if got.Name != "Veronica Mars" || got.Status != "Ended" {
		t.Errorf("show = %q (%q)", got.Name, got.Status)
	}
	type row struct {
		number, copies int
		released       bool
	}
	want := []row{{1, 1, true}, {2, 0, true}, {3, 1, true}, {4, 1, true}, {5, 0, false}}
	if len(got.Seasons) != len(want) {
		t.Fatalf("seasons = %+v, want %d (no unowned specials, season 4 added from the disc)", got.Seasons, len(want))
	}
	for i, w := range want {
		s := got.Seasons[i]
		if s.Number != w.number || len(s.Copies) != w.copies || s.Released != w.released {
			t.Errorf("season %d = %+v, want %+v", i, s, w)
		}
	}
	if got.Seasons[0].Copies[0].ItemID != s1 || got.Seasons[2].Copies[0].ItemID != s3 {
		t.Errorf("seasons 1 and 3 copies = %+v, %+v", got.Seasons[0].Copies, got.Seasons[2].Copies)
	}
	for _, season := range got.Seasons {
		if season.Wanted != (season.Number == 2) {
			t.Errorf("season %d wanted = %v, want only season 2 on the wishlist", season.Number, season.Wanted)
		}
	}
	if len(got.Unsorted) != 1 || got.Unsorted[0].ItemID != unsorted {
		t.Errorf("unsorted = %+v, want the disc without seasons", got.Unsorted)
	}

	get(t, ts.URL+"/api/shows/1432")
	if fetches != 1 {
		t.Errorf("TMDB fetched %d times, want the cached series reused", fetches)
	}
	if resp := get(t, ts.URL+"/api/shows/1"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown series: status = %d, want 404", resp.StatusCode)
	}
}
