package identify

import (
	"context"
	"testing"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

func (fx *fixture) wish(t *testing.T, tmdbID int64, title string, at time.Time) {
	t.Helper()
	in := db.TitleInput{TMDBID: &tmdbID, MediaType: db.MediaMovie, Title: title}
	if _, err := fx.store.CreateWant(context.Background(), in, db.WantEdit{Formats: db.WantFormats}, at); err != nil {
		t.Fatal(err)
	}
}

// wishes maps each wished-for title to the day it was collected, "" when still wanted.
func (fx *fixture) wishes(t *testing.T) map[string]string {
	t.Helper()
	wants, err := fx.store.ListWants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range wants {
		got[w.Title.Title] = deref(w.CollectedOn)
	}
	return got
}

func today() string { return time.Now().Format(time.DateOnly) }

func TestIdentifiedDiscCollectsWish(t *testing.T) {
	fx := newFixture(t)
	fx.wish(t, 16320, "Serenity", time.Now().Add(-time.Hour))
	fx.wish(t, 1, "Something Else", time.Now().Add(-time.Hour))
	fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Serenity", 2005)
	fx.fakes.guess.Format = "DVD" // any format fulfils the wish
	fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	got := fx.wishes(t)
	if len(got) != 2 || got["Serenity"] != today() || got["Something Else"] != "" {
		t.Errorf("wishes = %v, want Serenity collected today, Something Else still wanted", got)
	}
}

func TestDiscInReviewKeepsWish(t *testing.T) {
	fx := newFixture(t)
	fx.wish(t, 16320, "Serenity", time.Now().Add(-time.Hour))
	id := fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Serenity", 2005)
	fx.fakes.guess.Confidence = "low"
	fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	if got := fx.wishes(t); got["Serenity"] != "" {
		t.Fatalf("wishes = %v, want Serenity still wanted while the disc is in review", got)
	}
	// Approving the disc collects it.
	if err := fx.store.ApplyReview(context.Background(), id, db.ItemEdit{}); err != nil {
		t.Fatal(err)
	}
	if got := fx.wishes(t); got["Serenity"] != today() {
		t.Errorf("wishes = %v, want Serenity collected after approval", got)
	}
}

func TestLaterWishSurvivesOldDisc(t *testing.T) {
	fx := newFixture(t)
	fx.wish(t, 16320, "Serenity", time.Now().Add(-time.Hour))
	id := fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Serenity", 2005)
	fx.fakes.guess.Format = "DVD"
	fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}
	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}

	// Wishing again for the collected title (an upgrade to the DVD) makes it wanted again.
	fx.wish(t, 16320, "Serenity", time.Now().Add(time.Hour))
	if got := fx.wishes(t); got["Serenity"] != "" {
		t.Fatalf("wishes = %v, want the upgrade wish open", got)
	}
	notes := "edited"
	if err := fx.store.ApplyReview(context.Background(), id, db.ItemEdit{Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.ResetForIdentification(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	if got := fx.wishes(t); got["Serenity"] != "" {
		t.Errorf("wishes = %v, want the upgrade wish kept after editing and re-identifying the DVD", got)
	}
	in := db.TitleInput{TMDBID: new(int64), MediaType: db.MediaMovie, Title: "Serenity"}
	*in.TMDBID = 16320
	if _, err := fx.store.CreateWant(context.Background(), in, db.WantEdit{Formats: db.WantFormats}, time.Now()); err != db.ErrExists {
		t.Errorf("wishing for an open wish again: err = %v, want ErrExists", err)
	}
}
