package offers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WatcherAPI reads offers from the watcher's HTTP API (GET /api/v2/offers).
// The contract is in the watcher's docs/09_multi_store_plan.md, phase 2.
type WatcherAPI struct {
	base *url.URL
	http *http.Client
}

// NewWatcherAPI returns a client for the watcher at baseURL, for example
// http://shelfwatch:8080.
func NewWatcherAPI(baseURL string, client *http.Client) (*WatcherAPI, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("WATCHER_URL %q must be an http(s) URL such as http://shelfwatch:8080", baseURL)
	}
	return &WatcherAPI{base: u, http: client}, nil
}

// maxResponseBytes caps an offers response; a few hundred wishes are well under it.
const maxResponseBytes = 8 << 20

type apiResponse struct {
	Offers map[string]struct {
		SearchedAt *time.Time `json:"searched_at"`
		Items      []struct {
			ID                int64     `json:"id"`
			StoreKey          string    `json:"store_key"`
			Store             string    `json:"store"`
			Title             string    `json:"title"`
			Format            string    `json:"format"`
			Condition         string    `json:"condition"`
			PriceCents        int64     `json:"price_cents"`
			RegularPriceCents *int64    `json:"regular_price_cents"`
			Currency          string    `json:"currency"`
			Availability      string    `json:"availability"`
			URL               string    `json:"url"`
			ImageURL          *string   `json:"image_url"`
			LastSeenAt        time.Time `json:"last_seen_at"`
		} `json:"items"`
	} `json:"offers"`
}

// Ping checks that the watcher is up and serves the offers endpoint, by
// asking it for no movies.
func (w *WatcherAPI) Ping(ctx context.Context) error {
	parsed, err := w.get(ctx, url.Values{})
	if err != nil {
		return err
	}
	if parsed.Offers == nil {
		return fmt.Errorf("watcher offers: response has no \"offers\" object")
	}
	return nil
}

// Lookup implements Source.
func (w *WatcherAPI) Lookup(ctx context.Context, tmdbIDs []int64) (map[int64]Movie, error) {
	found := map[int64]Movie{}
	if len(tmdbIDs) == 0 {
		return found, nil
	}
	q := url.Values{}
	for _, id := range tmdbIDs {
		q.Add("tmdb_id", strconv.FormatInt(id, 10))
	}
	parsed, err := w.get(ctx, q)
	if err != nil {
		return nil, err
	}

	for _, id := range tmdbIDs {
		found[id] = Movie{Offers: []Offer{}}
	}
	for key, entry := range parsed.Offers {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("watcher offers: movie key %q is not a TMDB id", key)
		}
		m := Movie{SearchedAt: entry.SearchedAt, Offers: []Offer{}}
		for _, it := range entry.Items {
			format, ok := watcherFormats[it.Format]
			if !ok {
				continue // the watcher only promises bd and 4k; skip anything else
			}
			history := w.base.JoinPath("products", strconv.FormatInt(it.ID, 10)).String()
			m.Offers = append(m.Offers, Offer{
				TMDBID: id, HistoryURL: &history, StoreKey: it.StoreKey, Store: it.Store, Title: it.Title,
				Format: format, Condition: it.Condition, PriceCents: it.PriceCents,
				RegularPriceCents: it.RegularPriceCents, Currency: it.Currency, Availability: it.Availability,
				URL: it.URL, ImageURL: it.ImageURL, SeenAt: it.LastSeenAt,
			})
		}
		found[id] = m
	}
	return found, nil
}

// get calls the offers endpoint with the given query and decodes the response.
func (w *WatcherAPI) get(ctx context.Context, q url.Values) (*apiResponse, error) {
	endpoint := w.base.JoinPath("api", "v2", "offers")
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("watcher offers: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("watcher offers: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("watcher offers: %s: %.200s", resp.Status, body)
	}
	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("watcher offers: %w", err)
	}
	return &parsed, nil
}
