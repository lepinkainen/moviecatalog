package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/offers"
)

// SetOffers enables shop offers on the wishlist.
func (s *Server) SetOffers(src offers.Source) {
	s.offers = src
}

// wantOffer is an offer as shown on a wish.
type wantOffer struct {
	offers.Offer
	// Deal means in stock and within the wish's price cap.
	Deal bool `json:"deal"`
}

type wantView struct {
	*db.Want
	// Offers is null when offers could not be looked up.
	Offers []wantOffer `json:"offers"`
	// SearchedAt is when the watcher last searched the shops for this movie.
	SearchedAt *time.Time `json:"searched_at"`
}

type offersStatus struct {
	Configured bool    `json:"configured"`
	Error      *string `json:"error"`
}

type wishlist struct {
	Wants  []wantView   `json:"wants"`
	Offers offersStatus `json:"offers"`
}

func (s *Server) handleListWants(w http.ResponseWriter, r *http.Request) {
	wants, err := s.store.ListWants(r.Context())
	if err != nil {
		s.log.Error("list wants", "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not list the wishlist")
		return
	}
	resp := wishlist{Wants: make([]wantView, 0, len(wants)), Offers: offersStatus{Configured: s.offers != nil}}

	var found map[int64]offers.Movie
	if s.offers != nil {
		var ids []int64
		for _, want := range wants {
			if want.Title.MediaType == db.MediaMovie && want.CollectedOn == nil {
				ids = append(ids, *want.Title.TMDBID)
			}
		}
		found, err = s.offers.Lookup(r.Context(), ids)
		if err != nil {
			// The wishlist is still useful without prices.
			s.log.Warn("look up offers", "error", err)
			found = nil
			msg := "could not read offers from the shop watcher"
			resp.Offers.Error = &msg
		}
	}
	for _, want := range wants {
		view := wantView{Want: want}
		if want.CollectedOn != nil {
			// Bought: the shops no longer matter.
			view.Offers = []wantOffer{}
		} else if found != nil {
			movie := found[*want.Title.TMDBID]
			view.Offers = matchingOffers(want, movie.Offers)
			view.SearchedAt = movie.SearchedAt
		}
		resp.Wants = append(resp.Wants, view)
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// matchingOffers keeps the offers in a format the wish accepts. A sold-out used
// listing is dropped: it was a single copy, so it won't come back.
func matchingOffers(want *db.Want, all []offers.Offer) []wantOffer {
	matching := []wantOffer{}
	if want.Title.MediaType != db.MediaMovie {
		return matching
	}
	for _, o := range all {
		if !slices.Contains(want.Formats, o.Format) {
			continue
		}
		if o.Condition == "used" && o.Availability == offers.OutOfStock {
			continue
		}
		deal := o.Availability == offers.InStock && want.MaxPriceCents != nil && o.PriceCents <= *want.MaxPriceCents
		matching = append(matching, wantOffer{Offer: o, Deal: deal})
	}
	return matching
}

type wantEditBody struct {
	Formats       []string `json:"formats"`
	MaxPriceCents *int64   `json:"max_price_cents"`
	Notes         string   `json:"notes"`
}

func (b wantEditBody) validate() (db.WantEdit, string) {
	if len(b.Formats) == 0 {
		return db.WantEdit{}, "a wish needs at least one format"
	}
	var formats []string
	// Stored in a fixed order, whatever order they were sent in.
	for _, f := range db.WantFormats {
		if slices.Contains(b.Formats, f) {
			formats = append(formats, f)
		}
	}
	if len(formats) != len(slices.Compact(slices.Sorted(slices.Values(b.Formats)))) {
		return db.WantEdit{}, "formats must be from " + strings.Join(db.WantFormats, ", ")
	}
	if b.MaxPriceCents != nil && *b.MaxPriceCents <= 0 {
		return db.WantEdit{}, "max_price_cents must be positive"
	}
	return db.WantEdit{Formats: formats, MaxPriceCents: b.MaxPriceCents, Notes: strings.TrimSpace(b.Notes)}, ""
}

type createWantBody struct {
	Title titleEdit `json:"title"`
	wantEditBody
}

func (s *Server) handleCreateWant(w http.ResponseWriter, r *http.Request) {
	// A clone, because decoding into the slice would overwrite db.WantFormats.
	body := createWantBody{wantEditBody: wantEditBody{Formats: slices.Clone(db.WantFormats)}}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	t := body.Title
	t.Title = strings.TrimSpace(t.Title)
	if t.TMDBID == nil || t.Title == "" {
		s.writeError(w, http.StatusBadRequest, "a wish needs a title with a tmdb_id")
		return
	}
	if t.MediaType != db.MediaMovie && t.MediaType != db.MediaTV {
		s.writeError(w, http.StatusBadRequest, "media_type must be movie or tv")
		return
	}
	edit, msg := body.validate()
	if msg != "" {
		s.writeError(w, http.StatusBadRequest, msg)
		return
	}
	in := db.TitleInput{TMDBID: t.TMDBID, MediaType: t.MediaType, Title: t.Title, Year: t.Year, PosterPath: t.PosterPath}
	if t.OriginalTitle != nil {
		in.OriginalTitle = *t.OriginalTitle
	}
	want, err := s.store.CreateWant(r.Context(), in, edit, time.Now())
	if errors.Is(err, db.ErrExists) {
		s.writeError(w, http.StatusConflict, "already on the wishlist")
		return
	}
	if err != nil {
		s.log.Error("create want", "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not add the wish")
		return
	}
	s.titlesSaved()
	s.writeJSON(w, http.StatusCreated, want)
}

// handleUpdateWant replaces a wish's formats, price cap and notes.
func (s *Server) handleUpdateWant(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var body wantEditBody
	if !s.decodeJSON(w, r, &body) {
		return
	}
	edit, msg := body.validate()
	if msg != "" {
		s.writeError(w, http.StatusBadRequest, msg)
		return
	}
	err := s.store.UpdateWant(r.Context(), id, edit)
	if errors.Is(err, db.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "wish not found")
		return
	}
	if err != nil {
		s.log.Error("update want", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not update the wish")
		return
	}
	want, err := s.store.GetWant(r.Context(), id)
	if err != nil {
		s.log.Error("get want", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not load the wish")
		return
	}
	s.writeJSON(w, http.StatusOK, want)
}

func (s *Server) handleDeleteWant(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteWant(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "wish not found")
		return
	}
	if err != nil {
		s.log.Error("delete want", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not delete the wish")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSON reads a small JSON body into v, rejecting unknown fields.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}
