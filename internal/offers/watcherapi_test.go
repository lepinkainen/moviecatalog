package offers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// watcherResponse follows the contract in the watcher's plan: every requested
// id is a key, items are cheapest first, formats are the watcher's codes.
const watcherResponse = `{
  "offers": {
    "9874": {
      "searched_at": "2026-11-11T06:00:00Z",
      "items": [
        {"id": 412, "store_key": "ax", "store": "Levykauppa Äx", "title": "Cobra (4K Ultra HD)",
         "format": "4k", "condition": "new", "price_cents": 2299, "regular_price_cents": 3599,
         "currency": "EUR", "availability": "in_stock",
         "url": "https://www.levykauppax.fi/movie/cobra/#1", "image_url": null,
         "last_seen_at": "2026-11-11T11:46:17Z"},
        {"id": 413, "store_key": "ax", "store": "Levykauppa Äx", "title": "Cobra (DVD)",
         "format": "dvd", "condition": "new", "price_cents": 500, "regular_price_cents": null,
         "currency": "EUR", "availability": "in_stock", "url": "https://example.com/dvd",
         "image_url": null, "last_seen_at": "2026-11-11T11:46:17Z"}
      ]
    },
    "1551": {"searched_at": null, "items": []}
  }
}`

func TestWatcherAPILookup(t *testing.T) {
	var gotPath string
	var gotIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotIDs = r.URL.Path, r.URL.Query()["tmdb_id"]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(watcherResponse))
	}))
	defer srv.Close()

	// A trailing slash on the configured URL must not double up in the paths.
	api, err := NewWatcherAPI(srv.URL+"/", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := api.Lookup(context.Background(), []int64{9874, 1551, 999})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v2/offers" || !slices.Equal(gotIDs, []string{"9874", "1551", "999"}) {
		t.Errorf("request = %s %v, want /api/v2/offers with every id", gotPath, gotIDs)
	}

	cobra := got[9874]
	if cobra.SearchedAt == nil || !cobra.SearchedAt.Equal(time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC)) {
		t.Errorf("Cobra searched at = %v, want 06:00 UTC", cobra.SearchedAt)
	}
	if len(cobra.Offers) != 1 {
		t.Fatalf("Cobra offers = %+v, want only the 4K (the DVD is skipped)", cobra.Offers)
	}
	o := cobra.Offers[0]
	if o.Format != "UHD" || o.PriceCents != 2299 || o.RegularPriceCents == nil || *o.RegularPriceCents != 3599 ||
		o.Store != "Levykauppa Äx" || o.TMDBID != 9874 {
		t.Errorf("Cobra offer = %+v, want Äx's UHD at 2299 (was 3599)", o)
	}
	if o.HistoryURL == nil || *o.HistoryURL != srv.URL+"/products/412" {
		t.Errorf("history URL = %v, want %s/products/412", o.HistoryURL, srv.URL)
	}
	if !o.SeenAt.Equal(time.Date(2026, 11, 11, 11, 46, 17, 0, time.UTC)) {
		t.Errorf("seen at = %v", o.SeenAt)
	}

	if m := got[1551]; m.SearchedAt != nil || m.Offers == nil || len(m.Offers) != 0 {
		t.Errorf("Flatliners = %+v, want not searched and an empty list", m)
	}
	// An id the watcher left out still gets an entry.
	if m, ok := got[999]; !ok || m.SearchedAt != nil || len(m.Offers) != 0 {
		t.Errorf("missing id = %+v (present %v), want an empty entry", m, ok)
	}
}

func TestWatcherAPIErrors(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"server error": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "database is locked", http.StatusInternalServerError)
		},
		"not json": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) },
		"bad key": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"offers": {"cobra": {"searched_at": null, "items": []}}}`))
		},
	} {
		srv := httptest.NewServer(handler)
		api, err := NewWatcherAPI(srv.URL, srv.Client())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := api.Lookup(context.Background(), []int64{1}); err == nil {
			t.Errorf("%s: lookup succeeded, want an error", name)
		}
		srv.Close()
	}
}

func TestWatcherAPINoIDs(t *testing.T) {
	api, err := NewWatcherAPI("http://127.0.0.1:1", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	got, err := api.Lookup(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Errorf("lookup of nothing = %v, %v; want no request and an empty map", got, err)
	}
}

func TestNewWatcherAPIRejectsBadURL(t *testing.T) {
	for _, u := range []string{"", "shelfwatch:8080", "ftp://x", "http://"} {
		if _, err := NewWatcherAPI(u, http.DefaultClient); err == nil || !strings.Contains(err.Error(), "WATCHER_URL") {
			t.Errorf("NewWatcherAPI(%q) error = %v, want a WATCHER_URL error", u, err)
		}
	}
}

func TestWatcherAPIPing(t *testing.T) {
	var gotQuery string
	for name, c := range map[string]struct {
		handler http.HandlerFunc
		ok      bool
	}{
		"up": {func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.Write([]byte(`{"offers": {}}`))
		}, true},
		"server error": {func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}, false},
		"not the offers API": {func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status": "ok"}`))
		}, false},
	} {
		srv := httptest.NewServer(c.handler)
		api, err := NewWatcherAPI(srv.URL, srv.Client())
		if err != nil {
			t.Fatal(err)
		}
		err = api.Ping(context.Background())
		if (err == nil) != c.ok {
			t.Errorf("%s: Ping error = %v, want ok=%v", name, err, c.ok)
		}
		srv.Close()
	}
	if gotQuery != "" {
		t.Errorf("ping query = %q, want no movies asked for", gotQuery)
	}

	// Nothing listening.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	api, _ := NewWatcherAPI(srv.URL, srv.Client())
	if err := api.Ping(context.Background()); err == nil {
		t.Errorf("Ping of a closed server succeeded")
	}
}
