package series

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/identify"
)

// Show is a TV series with its seasons in order and the discs holding each.
type Show struct {
	TMDBID       int64  `json:"tmdb_id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	// Year is the first air year.
	Year       *int     `json:"year"`
	Status     string   `json:"status"`
	PosterPath *string  `json:"poster_path"`
	Seasons    []Season `json:"seasons"`
	// Unsorted are discs of the series that do not say which seasons they hold.
	Unsorted []db.OwnedCopy `json:"unsorted"`
}

// Season is one season of a series.
type Season struct {
	Number       int     `json:"number"`
	Name         string  `json:"name"`
	Year         *int    `json:"year"`
	EpisodeCount int     `json:"episode_count"`
	PosterPath   *string `json:"poster_path"`
	// Released is false for announced seasons, which do not count as missing.
	Released bool           `json:"released"`
	Copies   []db.OwnedCopy `json:"copies"`
	// Wanted says the season is on the wishlist.
	Wanted bool `json:"wanted"`
}

// BuildShow returns a series with the discs holding each season and whether
// it is on the wishlist.
func BuildShow(ctx context.Context, store *db.Store, show *identify.Show, now time.Time) (Show, error) {
	copies, err := store.ShowCopies(ctx, show.ID)
	if err != nil {
		return Show{}, err
	}
	wanted, err := store.WantedSeasons(ctx, show.ID)
	if err != nil {
		return Show{}, err
	}
	return AssembleShow(show, copies, wanted, now), nil
}

// AssembleShow joins a series with the given discs (best format first) and
// wished-for seasons. wanted may be nil.
// Specials (season 0) are listed only when owned, and an owned season TMDB
// does not list is added.
func AssembleShow(show *identify.Show, copies []db.ShowCopy, wanted map[int]bool, now time.Time) Show {
	today := now.Format("2006-01-02")
	s := Show{TMDBID: show.ID, Name: show.Name, OriginalName: show.OriginalName, Year: show.Year,
		Status: show.Status, PosterPath: show.PosterPath,
		Seasons: []Season{}, Unsorted: []db.OwnedCopy{}}
	listed := map[int]bool{}
	for _, ts := range show.Seasons {
		season := Season{Number: ts.Number, Name: ts.Name, Year: ts.Year, EpisodeCount: ts.EpisodeCount,
			PosterPath: ts.PosterPath, Released: ts.AirDate != "" && ts.AirDate <= today, Copies: []db.OwnedCopy{},
			Wanted: wanted[ts.Number]}
		for _, c := range copies {
			if slices.Contains(c.Seasons, ts.Number) {
				season.Copies = append(season.Copies, c.OwnedCopy)
			}
		}
		if ts.Number == 0 && len(season.Copies) == 0 {
			continue
		}
		listed[ts.Number] = true
		s.Seasons = append(s.Seasons, season)
	}
	for _, c := range copies {
		if len(c.Seasons) == 0 {
			s.Unsorted = append(s.Unsorted, c.OwnedCopy)
			continue
		}
		for _, n := range c.Seasons {
			if listed[n] {
				continue
			}
			i := slices.IndexFunc(s.Seasons, func(x Season) bool { return x.Number == n })
			if i < 0 {
				s.Seasons = append(s.Seasons, Season{Number: n, Name: "Season " + strconv.Itoa(n), Released: true})
				i = len(s.Seasons) - 1
			}
			s.Seasons[i].Copies = append(s.Seasons[i].Copies, c.OwnedCopy)
		}
	}
	slices.SortStableFunc(s.Seasons, func(a, b Season) int { return a.Number - b.Number })
	return s
}
