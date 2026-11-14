package identify

import (
	"context"
	"encoding/json"
	"errors"
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
	params := url.Values{"query": {query}, "include_adult": {"false"}}
	if year != nil {
		if mediaType == db.MediaTV {
			params.Set("first_air_date_year", strconv.Itoa(*year))
		} else {
			params.Set("year", strconv.Itoa(*year))
		}
	}
	body, err := t.get(ctx, "/search/"+mediaType, params, fmt.Sprintf("tmdb search %q", query))
	if err != nil {
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
		c.Year = yearOf(date)
		candidates = append(candidates, c)
	}
	return candidates, nil
}

// yearOf returns the year of a TMDB date ("1981-06-12"), or nil when it has none.
func yearOf(date string) *int {
	if len(date) < 4 {
		return nil
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return nil
	}
	return &y
}

// ErrNotFound is returned when TMDB has no entry with the requested id.
var ErrNotFound = errors.New("not found in TMDB")

// get fetches a TMDB API path and returns the body of a 200 response. what
// names the request in errors.
func (t *TMDB) get(ctx context.Context, path string, params url.Values, what string) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", t.APIKey)
	baseURL := t.BaseURL
	if baseURL == "" {
		baseURL = "https://api.themoviedb.org/3"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.HTTP.Do(req)
	if err != nil {
		// The URL in a transport error would include the API key.
		return nil, transient(fmt.Errorf("%s: request failed", what))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, transient(fmt.Errorf("%s: %w", what, err))
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("%s: %s: %s", what, resp.Status, truncate(string(body), 200))
		if transientStatus(resp.StatusCode) {
			err = transient(err)
		}
		return nil, err
	}
	return body, nil
}

// Movie returns the TMDB collection a movie belongs to, or nil when it is in none.
func (t *TMDB) Movie(ctx context.Context, id int64) (*db.Series, error) {
	what := fmt.Sprintf("tmdb movie %d", id)
	body, err := t.get(ctx, "/movie/"+strconv.FormatInt(id, 10), nil, what)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Collection *struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"belongs_to_collection"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if parsed.Collection == nil || parsed.Collection.ID == 0 {
		return nil, nil
	}
	return &db.Series{ID: parsed.Collection.ID, Name: parsed.Collection.Name}, nil
}

// Collection is a TMDB collection: the movies of a series.
type Collection struct {
	ID         int64
	Name       string
	Overview   string
	PosterPath *string
	Parts      []CollectionPart
}

// CollectionPart is one movie of a collection.
type CollectionPart struct {
	ID          int64
	Title       string
	ReleaseDate string // "1981-06-12", or empty when not announced
	Year        *int
	PosterPath  *string
}

// Collection returns a collection with its movies, in TMDB's order.
func (t *TMDB) Collection(ctx context.Context, id int64) (*Collection, error) {
	what := fmt.Sprintf("tmdb collection %d", id)
	body, err := t.get(ctx, "/collection/"+strconv.FormatInt(id, 10), nil, what)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Overview   string  `json:"overview"`
		PosterPath *string `json:"poster_path"`
		Parts      []struct {
			ID          int64   `json:"id"`
			Title       string  `json:"title"`
			ReleaseDate string  `json:"release_date"`
			PosterPath  *string `json:"poster_path"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	c := &Collection{ID: parsed.ID, Name: parsed.Name, Overview: parsed.Overview, PosterPath: parsed.PosterPath}
	for _, p := range parsed.Parts {
		c.Parts = append(c.Parts, CollectionPart{ID: p.ID, Title: p.Title, ReleaseDate: p.ReleaseDate,
			Year: yearOf(p.ReleaseDate), PosterPath: p.PosterPath})
	}
	return c, nil
}

// Show is a TMDB series with its seasons.
type Show struct {
	ID           int64
	Name         string
	OriginalName string
	Year         *int   // first air year
	Status       string // "Returning Series", "Ended", "Canceled", …
	PosterPath   *string
	Seasons      []ShowSeason
}

// ShowSeason is one season of a series. Season 0 holds the specials.
type ShowSeason struct {
	Number       int
	Name         string
	AirDate      string // "2004-09-22", or empty when not announced
	Year         *int
	EpisodeCount int
	PosterPath   *string
}

// Show returns a series with its seasons, in TMDB's order.
func (t *TMDB) Show(ctx context.Context, id int64) (*Show, error) {
	what := fmt.Sprintf("tmdb tv %d", id)
	body, err := t.get(ctx, "/tv/"+strconv.FormatInt(id, 10), nil, what)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		ID           int64   `json:"id"`
		Name         string  `json:"name"`
		OriginalName string  `json:"original_name"`
		FirstAirDate string  `json:"first_air_date"`
		Status       string  `json:"status"`
		PosterPath   *string `json:"poster_path"`
		Seasons      []struct {
			SeasonNumber int     `json:"season_number"`
			Name         string  `json:"name"`
			AirDate      string  `json:"air_date"`
			EpisodeCount int     `json:"episode_count"`
			PosterPath   *string `json:"poster_path"`
		} `json:"seasons"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	s := &Show{ID: parsed.ID, Name: parsed.Name, OriginalName: parsed.OriginalName, Year: yearOf(parsed.FirstAirDate),
		Status: parsed.Status, PosterPath: parsed.PosterPath}
	for _, p := range parsed.Seasons {
		s.Seasons = append(s.Seasons, ShowSeason{Number: p.SeasonNumber, Name: p.Name, AirDate: p.AirDate,
			Year: yearOf(p.AirDate), EpisodeCount: p.EpisodeCount, PosterPath: p.PosterPath})
	}
	return s, nil
}
