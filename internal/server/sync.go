package server

import (
	"net/http"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

// wantedMovie is a wish as the shop watcher needs it, to search the shops for it.
type wantedMovie struct {
	TMDBID        int64    `json:"tmdb_id"`
	Title         string   `json:"title"`
	OriginalTitle *string  `json:"original_title"`
	Year          *int     `json:"year"`
	Formats       []string `json:"formats"`
	MaxPriceCents *int64   `json:"max_price_cents"`
}

type syncResponse struct {
	Owned  []db.OwnedMovie `json:"owned"`
	Wanted []wantedMovie   `json:"wanted"`
}

// handleSync tells the shop watcher what I own and what I want, so it can hide
// owned movies while browsing and search the shops for wished-for ones.
// Movies only: the watcher does not match series.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	owned, err := s.store.ListOwnedMovies(r.Context())
	if err != nil {
		s.log.Error("list owned movies", "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not list owned movies")
		return
	}
	wants, err := s.store.ListWants(r.Context())
	if err != nil {
		s.log.Error("list wants", "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not list the wishlist")
		return
	}
	resp := syncResponse{Owned: owned, Wanted: []wantedMovie{}}
	for _, want := range wants {
		if want.Title.MediaType != db.MediaMovie {
			continue
		}
		resp.Wanted = append(resp.Wanted, wantedMovie{
			TMDBID: *want.Title.TMDBID, Title: want.Title.Title, OriginalTitle: want.Title.OriginalTitle,
			Year: want.Title.Year, Formats: want.Formats, MaxPriceCents: want.MaxPriceCents,
		})
	}
	s.writeJSON(w, http.StatusOK, resp)
}
