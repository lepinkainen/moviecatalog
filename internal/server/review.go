package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/identify"
)

// SetTMDB enables the catalogue search used when fixing a title by hand.
func (s *Server) SetTMDB(tmdb *identify.TMDB) {
	s.tmdb = tmdb
}

type titleEdit struct {
	TMDBID        *int64  `json:"tmdb_id"`
	MediaType     string  `json:"media_type"`
	Title         string  `json:"title"`
	OriginalTitle *string `json:"original_title"`
	Year          *int    `json:"year"`
	PosterPath    *string `json:"poster_path"`
}

type itemEdit struct {
	Format *string      `json:"format"`
	Notes  *string      `json:"notes"`
	Titles *[]titleEdit `json:"titles"`
}

// handleUpdateItem applies a manual correction; an empty body approves the item as is.
func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var body itemEdit
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	edit := db.ItemEdit{Format: body.Format, Notes: body.Notes}
	if body.Format != nil && !slices.Contains(db.Formats, *body.Format) {
		s.writeError(w, http.StatusBadRequest, "format must be one of "+strings.Join(db.Formats, ", "))
		return
	}
	if body.Titles != nil {
		titles := make([]db.TitleInput, 0, len(*body.Titles))
		for _, t := range *body.Titles {
			t.Title = strings.TrimSpace(t.Title)
			if t.Title == "" {
				s.writeError(w, http.StatusBadRequest, "every title needs a name")
				return
			}
			if t.MediaType != db.MediaMovie && t.MediaType != db.MediaTV {
				s.writeError(w, http.StatusBadRequest, "media_type must be movie or tv")
				return
			}
			in := db.TitleInput{TMDBID: t.TMDBID, MediaType: t.MediaType, Title: t.Title, Year: t.Year, PosterPath: t.PosterPath}
			if t.OriginalTitle != nil {
				in.OriginalTitle = *t.OriginalTitle
			}
			titles = append(titles, in)
		}
		edit.Titles = &titles
	}

	err := s.store.ApplyReview(r.Context(), id, edit)
	if errors.Is(err, db.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		s.log.Error("update item", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not update item")
		return
	}
	s.handleGetItem(w, r)
}

type tmdbResult struct {
	TMDBID        int64   `json:"tmdb_id"`
	MediaType     string  `json:"media_type"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          *int    `json:"year"`
	PosterPath    *string `json:"poster_path"`
	Overview      string  `json:"overview"`
}

// handleTMDBSearch looks a title up in TMDB: ?q=<title>&type=movie|tv.
func (s *Server) handleTMDBSearch(w http.ResponseWriter, r *http.Request) {
	if s.tmdb == nil {
		s.writeError(w, http.StatusServiceUnavailable, "TMDB search is not configured (TMDB_API_KEY is not set)")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	mediaType := r.URL.Query().Get("type")
	if mediaType == "" {
		mediaType = db.MediaMovie
	}
	if query == "" || (mediaType != db.MediaMovie && mediaType != db.MediaTV) {
		s.writeError(w, http.StatusBadRequest, "expected q=<title> and type=movie or tv")
		return
	}
	found, err := s.tmdb.Search(r.Context(), mediaType, query, nil)
	if err != nil {
		s.log.Error("tmdb search", "query", query, "error", err)
		s.writeError(w, http.StatusBadGateway, "TMDB search failed")
		return
	}
	results := make([]tmdbResult, 0, len(found))
	for _, c := range found {
		results = append(results, tmdbResult{TMDBID: c.ID, MediaType: c.MediaType, Title: c.Title,
			OriginalTitle: c.OriginalTitle, Year: c.Year, PosterPath: c.PosterPath, Overview: c.Overview})
	}
	s.writeJSON(w, http.StatusOK, results)
}
