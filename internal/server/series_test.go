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
