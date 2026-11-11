package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/offers"
)

// fakeOffers serves fixed offers, or fails with err.
type fakeOffers struct {
	movies map[int64]offers.Movie
	err    error
	asked  []int64
}

func (f *fakeOffers) Lookup(_ context.Context, ids []int64) (map[int64]offers.Movie, error) {
	f.asked = ids
	return f.movies, f.err
}

func (f *fakeOffers) TracksSearches() bool { return true }

func newWishlistServer(t *testing.T, src offers.Source) (*httptest.Server, *db.Store) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := db.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, dataDir, testUI, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if src != nil {
		srv.SetOffers(src)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

func send(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

type wishlistResponse struct {
	Wants []struct {
		db.Want
		Offers []struct {
			offers.Offer
			Deal bool `json:"deal"`
		} `json:"offers"`
		SearchedAt *time.Time `json:"searched_at"`
	} `json:"wants"`
	Offers struct {
		Configured bool    `json:"configured"`
		Searches   bool    `json:"searches"`
		Error      *string `json:"error"`
	} `json:"offers"`
}

const upTitle = `{"tmdb_id": 14160, "media_type": "movie", "title": "Up", "original_title": "Up", "year": 2009, "poster_path": "/up.jpg"}`

func TestWishlistCRUD(t *testing.T) {
	ts, _ := newWishlistServer(t, nil)

	resp := send(t, http.MethodPost, ts.URL+"/api/wants", `{"title": `+upTitle+`}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	want := decode[db.Want](t, resp)
	if want.Title.Title != "Up" || strings.Join(want.Formats, ",") != "Blu-ray,UHD" || want.MaxPriceCents != nil {
		t.Errorf("created = %+v, want Up accepting both formats with no cap", want)
	}

	if resp := send(t, http.MethodPost, ts.URL+"/api/wants", `{"title": `+upTitle+`}`); resp.StatusCode != http.StatusConflict {
		t.Errorf("second create status = %d, want 409", resp.StatusCode)
	}

	resp = send(t, http.MethodPut, ts.URL+"/api/wants/1",
		`{"formats": ["UHD", "Blu-ray"], "max_price_cents": 1500, "notes": " steelbook? "}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", resp.StatusCode)
	}
	want = decode[db.Want](t, resp)
	if strings.Join(want.Formats, ",") != "Blu-ray,UHD" || want.MaxPriceCents == nil || *want.MaxPriceCents != 1500 ||
		want.Notes != "steelbook?" {
		t.Errorf("updated = %+v, want formats in fixed order, cap 1500 and trimmed notes", want)
	}

	list := decode[wishlistResponse](t, get(t, ts.URL+"/api/wants"))
	if len(list.Wants) != 1 || list.Offers.Configured || list.Wants[0].Offers != nil {
		t.Errorf("list = %+v, want one wish and no offers (no source configured)", list)
	}

	if resp := send(t, http.MethodDelete, ts.URL+"/api/wants/1", ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", resp.StatusCode)
	}
	if resp := send(t, http.MethodDelete, ts.URL+"/api/wants/1", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", resp.StatusCode)
	}
}

func TestWishlistRejectsBadInput(t *testing.T) {
	ts, _ := newWishlistServer(t, nil)
	for name, body := range map[string]string{
		"no tmdb id":     `{"title": {"tmdb_id": null, "media_type": "movie", "title": "Up"}}`,
		"dvd":            `{"title": ` + upTitle + `, "formats": ["DVD"]}`,
		"no formats":     `{"title": ` + upTitle + `, "formats": []}`,
		"negative cap":   `{"title": ` + upTitle + `, "max_price_cents": -1}`,
		"unknown field":  `{"title": ` + upTitle + `, "price": 10}`,
		"bad media type": `{"title": {"tmdb_id": 1, "media_type": "game", "title": "Up"}}`,
	} {
		if resp := send(t, http.MethodPost, ts.URL+"/api/wants", body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
}

func TestWishlistOffers(t *testing.T) {
	price := func(cents int64) *int64 { return &cents }
	searched := time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC)
	src := &fakeOffers{movies: map[int64]offers.Movie{
		14160: {SearchedAt: &searched, Offers: []offers.Offer{
			{TMDBID: 14160, Store: "CDON", Format: "Blu-ray", PriceCents: 899, Availability: offers.InStock},
			{TMDBID: 14160, Store: "Äx", Format: "UHD", PriceCents: 1299, Availability: "supplier_order"},
			{TMDBID: 14160, Store: "Äx", Format: "UHD", PriceCents: 1999, RegularPriceCents: price(2499), Availability: offers.InStock},
		}},
	}}
	ts, store := newWishlistServer(t, src)

	// Owned on DVD; the wish is an upgrade to UHD under €15.
	ctx := context.Background()
	if _, err := store.CreateItem(ctx, time.Now(), func(int64) ([]db.NewImage, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	tmdbID := int64(14160)
	dvd := "DVD"
	if err := store.ApplyReview(ctx, 1, db.ItemEdit{Format: &dvd, Titles: &[]db.TitleInput{
		{TMDBID: &tmdbID, MediaType: db.MediaMovie, Title: "Up"}}}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"title": ` + upTitle + `, "formats": ["UHD"], "max_price_cents": 1500}`,
		// A series never has offers, and is not looked up.
		`{"title": {"tmdb_id": 1399, "media_type": "tv", "title": "Game of Thrones"}}`,
	} {
		if resp := send(t, http.MethodPost, ts.URL+"/api/wants", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want 201", body, resp.StatusCode)
		}
	}
	if strings.Join(db.WantFormats, ",") != "Blu-ray,UHD" {
		t.Fatalf("db.WantFormats = %v, was modified by a request", db.WantFormats)
	}

	list := decode[wishlistResponse](t, get(t, ts.URL+"/api/wants"))
	if !list.Offers.Configured || !list.Offers.Searches || list.Offers.Error != nil {
		t.Fatalf("offers status = %+v, want configured, tracking searches, without error", list.Offers)
	}
	if len(src.asked) != 1 || src.asked[0] != 14160 {
		t.Errorf("looked up %v, want only the movie", src.asked)
	}
	up, got := list.Wants[1], list.Wants[0]
	if up.SearchedAt == nil || !up.SearchedAt.Equal(searched) {
		t.Errorf("searched at = %v, want %v", up.SearchedAt, searched)
	}
	if got.SearchedAt != nil {
		t.Errorf("series searched at = %v, want null", got.SearchedAt)
	}
	if strings.Join(up.OwnedFormats, ",") != "DVD" {
		t.Errorf("owned formats = %v, want DVD", up.OwnedFormats)
	}
	if len(up.Offers) != 2 {
		t.Fatalf("Up offers = %+v, want the two UHD ones", up.Offers)
	}
	if up.Offers[0].Deal {
		t.Errorf("supplier-order offer is a deal, want only in-stock offers to count")
	}
	if up.Offers[1].Deal {
		t.Errorf("offer at 1999 is a deal under a 1500 cap")
	}
	if got.Offers == nil || len(got.Offers) != 0 {
		t.Errorf("series offers = %v, want an empty list", got.Offers)
	}

	// Raising the cap makes the in-stock UHD a deal.
	send(t, http.MethodPut, ts.URL+"/api/wants/1", `{"formats": ["UHD"], "max_price_cents": 2000}`)
	list = decode[wishlistResponse](t, get(t, ts.URL+"/api/wants"))
	if o := list.Wants[1].Offers; !o[1].Deal || o[0].Deal {
		t.Errorf("offers after raising the cap = %+v, want only the in-stock UHD as a deal", o)
	}

	// A failing source leaves the wishlist working.
	src.err = errors.New("database is locked")
	list = decode[wishlistResponse](t, get(t, ts.URL+"/api/wants"))
	if list.Offers.Error == nil || len(list.Wants) != 2 || list.Wants[0].Offers != nil {
		t.Errorf("list with a failing source = %+v, want both wishes, null offers and an error", list)
	}
}

func TestSync(t *testing.T) {
	ts, store := newWishlistServer(t, nil)
	ctx := context.Background()

	// Item 1: Cobra on Blu-ray. Item 2: Cobra again on DVD. Item 3: a series.
	// Item 4: still waiting for identification.
	movie := func(id int64, name string) db.TitleInput {
		return db.TitleInput{TMDBID: &id, MediaType: db.MediaMovie, Title: name}
	}
	tv := int64(1399)
	for i, c := range []struct {
		format string
		titles []db.TitleInput
	}{
		{"Blu-ray", []db.TitleInput{movie(9874, "Cobra")}},
		{"DVD", []db.TitleInput{movie(9874, "Cobra")}},
		{"Blu-ray", []db.TitleInput{{TMDBID: &tv, MediaType: db.MediaTV, Title: "Game of Thrones"}}},
		{"", nil},
	} {
		if _, err := store.CreateItem(ctx, time.Now(), func(int64) ([]db.NewImage, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
		if c.format == "" {
			continue
		}
		if err := store.ApplyReview(ctx, int64(i+1), db.ItemEdit{Format: &c.format, Titles: &c.titles}); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{
		`{"title": ` + upTitle + `, "formats": ["UHD"], "max_price_cents": 2000}`,
		`{"title": {"tmdb_id": 1399, "media_type": "tv", "title": "Game of Thrones"}}`,
	} {
		if resp := send(t, http.MethodPost, ts.URL+"/api/wants", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d", body, resp.StatusCode)
		}
	}

	got := decode[syncResponse](t, get(t, ts.URL+"/api/sync"))
	if len(got.Owned) != 1 || got.Owned[0].TMDBID != 9874 {
		t.Fatalf("owned = %+v, want only Cobra (no series, no unidentified items)", got.Owned)
	}
	if f := got.Owned[0].Formats; len(f) != 2 || !slices.Contains(f, "DVD") || !slices.Contains(f, "Blu-ray") {
		t.Errorf("Cobra formats = %v, want DVD and Blu-ray", f)
	}
	if len(got.Wanted) != 1 {
		t.Fatalf("wanted = %+v, want only the movie", got.Wanted)
	}
	w := got.Wanted[0]
	if w.TMDBID != 14160 || w.Title != "Up" || w.Year == nil || *w.Year != 2009 ||
		strings.Join(w.Formats, ",") != "UHD" || w.MaxPriceCents == nil || *w.MaxPriceCents != 2000 {
		t.Errorf("wanted = %+v, want Up (2009) in UHD under 2000", w)
	}
}

func TestOnlyInStockIsADeal(t *testing.T) {
	cap := int64(2000)
	id := int64(1)
	want := &db.Want{Title: db.Title{TMDBID: &id, MediaType: db.MediaMovie}, Formats: []string{"Blu-ray"}, MaxPriceCents: &cap}
	for availability, deal := range map[string]bool{
		"in_stock": true, "supplier_order": false, "preorder": false, "out_of_stock": false, "unknown": false,
	} {
		got := matchingOffers(want, []offers.Offer{{Format: "Blu-ray", PriceCents: 999, Availability: availability}})
		if len(got) != 1 || got[0].Deal != deal {
			t.Errorf("%s under the cap: %+v, want shown with deal=%v", availability, got, deal)
		}
	}
}
