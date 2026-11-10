package identify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

// TMDB searches The Movie Database (API v3).
type TMDB struct {
	APIKey  string
	BaseURL string // defaults to https://api.themoviedb.org/3
	HTTP    *http.Client
}

// Candidate is one TMDB search result.
type Candidate struct {
	ID            int64   `json:"tmdb_id"`
	MediaType     string  `json:"media_type"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          *int    `json:"year"`
	PosterPath    *string `json:"-"`
	Overview      string  `json:"overview"`
}

// Search returns the first page of results for a movie or series title.
// year, when given, restricts movies to that release year and series to
// that first-air year.
func (t *TMDB) Search(ctx context.Context, mediaType, query string, year *int) ([]Candidate, error) {
	params := url.Values{"api_key": {t.APIKey}, "query": {query}, "include_adult": {"false"}}
	if year != nil {
		if mediaType == db.MediaTV {
			params.Set("first_air_date_year", strconv.Itoa(*year))
		} else {
			params.Set("year", strconv.Itoa(*year))
		}
	}
	baseURL := t.BaseURL
	if baseURL == "" {
		baseURL = "https://api.themoviedb.org/3"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/search/"+mediaType+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.HTTP.Do(req)
	if err != nil {
		// The URL in a transport error would include the API key.
		return nil, transient(fmt.Errorf("tmdb search %q: request failed", query))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, transient(fmt.Errorf("tmdb search %q: %w", query, err))
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("tmdb search %q: %s: %s", query, resp.Status, truncate(string(body), 200))
		if transientStatus(resp.StatusCode) {
			err = transient(err)
		}
		return nil, err
	}

	var parsed struct {
		Results []struct {
			ID            int64   `json:"id"`
			Title         string  `json:"title"`
			Name          string  `json:"name"`
			OriginalTitle string  `json:"original_title"`
			OriginalName  string  `json:"original_name"`
			ReleaseDate   string  `json:"release_date"`
			FirstAirDate  string  `json:"first_air_date"`
			PosterPath    *string `json:"poster_path"`
			Overview      string  `json:"overview"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("tmdb search %q: %w", query, err)
	}

	candidates := make([]Candidate, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		c := Candidate{ID: r.ID, MediaType: mediaType, PosterPath: r.PosterPath, Overview: truncate(r.Overview, 200)}
		date := r.ReleaseDate
		if mediaType == db.MediaTV {
			c.Title, c.OriginalTitle, date = r.Name, r.OriginalName, r.FirstAirDate
		} else {
			c.Title, c.OriginalTitle = r.Title, r.OriginalTitle
		}
		if len(date) >= 4 {
			if y, err := strconv.Atoi(date[:4]); err == nil {
				c.Year = &y
			}
		}
		candidates = append(candidates, c)
	}
	return candidates, nil
}
