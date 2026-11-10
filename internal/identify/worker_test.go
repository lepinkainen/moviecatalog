package identify

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

// fakes stands in for OpenAI and TMDB.
type fakes struct {
	t *testing.T
	// guess is the identification answer; guessStatus overrides the HTTP status when set.
	guess       Guess
	guessStatus int
	// pick is the tmdb_id the LLM chooses when asked; picks counts the questions.
	pick  *int64
	picks int
	// results maps "<media type>|<query>|<year or empty>" to TMDB results.
	results map[string][]map[string]any
	// identifies counts the identification requests; onIdentify runs during each one.
	identifies int
	onIdentify func()
	// tmdbStatus makes TMDB searches fail with this HTTP status when set.
	tmdbStatus int
}

func (f *fakes) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /responses", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text struct {
				Format struct {
					Name string `json:"name"`
				} `json:"format"`
			} `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Errorf("bad OpenAI request: %v", err)
		}
		var answer any
		switch req.Text.Format.Name {
		case "identification":
			f.identifies++
			if f.onIdentify != nil {
				f.onIdentify()
			}
			if f.guessStatus != 0 {
				http.Error(w, `{"error":"boom"}`, f.guessStatus)
				return
			}
			answer = f.guess
		case "pick":
			f.picks++
			answer = map[string]any{"tmdb_id": f.pick, "reason": "test"}
		default:
			f.t.Errorf("unexpected schema %q", req.Text.Format.Name)
		}
		text, _ := json.Marshal(answer)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"output": []any{
				map[string]any{"type": "reasoning"},
				map[string]any{"type": "message", "content": []any{
					map[string]any{"type": "output_text", "text": string(text)},
				}},
			},
			"usage": map[string]any{
				"input_tokens": 1000, "output_tokens": 200,
				"input_tokens_details": map[string]any{"cached_tokens": 400},
			},
		})
	})
	mux.HandleFunc("GET /search/{kind}", func(w http.ResponseWriter, r *http.Request) {
		if f.tmdbStatus != 0 {
			http.Error(w, `{"status_message":"boom"}`, f.tmdbStatus)
			return
		}
		q := r.URL.Query()
		year := q.Get("year") + q.Get("first_air_date_year")
		key := r.PathValue("kind") + "|" + q.Get("query") + "|" + year
		json.NewEncoder(w).Encode(map[string]any{"results": f.results[key]})
	})
	return mux
}

type fixture struct {
	store  *db.Store
	worker *Worker
	fakes  *fakes
	dir    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := &fakes{t: t, results: map[string][]map[string]any{}}
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	llm := &OpenAI{APIKey: "k", Model: "test-model", BaseURL: ts.URL, HTTP: ts.Client()}
	tmdb := &TMDB{APIKey: "k", BaseURL: ts.URL, HTTP: ts.Client()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &fixture{store: store, worker: NewWorker(store, dir, llm, tmdb, log), fakes: f, dir: dir}
}

func writeJPEG(t *testing.T, path string, img image.Image) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := jpeg.Encode(out, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
}

// capture stores an item whose back photo is the given image (blank when nil).
func (fx *fixture) capture(t *testing.T, back image.Image) int64 {
	t.Helper()
	blank := image.NewGray(image.Rect(0, 0, 64, 64))
	if back == nil {
		back = blank
	}
	item, err := fx.store.CreateItem(context.Background(), time.Now(), func(id int64) ([]db.NewImage, error) {
		rel := filepath.Join("images", strconv.FormatInt(id, 10))
		if err := os.MkdirAll(filepath.Join(fx.dir, rel), 0o755); err != nil {
			return nil, err
		}
		writeJPEG(t, filepath.Join(fx.dir, rel, "front.jpg"), blank)
		writeJPEG(t, filepath.Join(fx.dir, rel, "back.jpg"), back)
		return []db.NewImage{
			{Kind: db.KindFront, Path: filepath.Join(rel, "front.jpg"), ContentType: "image/jpeg"},
			{Kind: db.KindBack, Path: filepath.Join(rel, "back.jpg"), ContentType: "image/jpeg"},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return item.ID
}

func (fx *fixture) process(t *testing.T) error {
	t.Helper()
	processed, err := fx.worker.ProcessNext(context.Background())
	if !processed {
		t.Fatal("no item was processed")
	}
	return err
}

func (fx *fixture) item(t *testing.T, id int64) *db.Item {
	t.Helper()
	item, err := fx.store.GetItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func intp(v int) *int { return &v }

func movieGuess(title string, year int) Guess {
	return Guess{Format: "Blu-ray", Confidence: "high",
		Titles: []GuessTitle{{MediaType: "movie", Title: title, OriginalTitle: title, Year: intp(year)}}}
}

func result(id int, title, date string) map[string]any {
	return map[string]any{"id": id, "title": title, "original_title": title, "release_date": date,
		"name": title, "original_name": title, "first_air_date": date, "poster_path": "/p" + strconv.Itoa(id) + ".jpg"}
}

// barcodeImage renders an EAN-13 barcode with a white margin, optionally sideways.
func barcodeImage(t *testing.T, digits string, sideways bool) image.Image {
	t.Helper()
	matrix, err := oned.NewEAN13Writer().Encode(digits, gozxing.BarcodeFormat_EAN_13, 400, 120, nil)
	if err != nil {
		t.Fatal(err)
	}
	const margin = 60
	img := image.NewGray(image.Rect(0, 0, matrix.GetWidth()+2*margin, matrix.GetHeight()+2*margin))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y := 0; y < matrix.GetHeight(); y++ {
		for x := 0; x < matrix.GetWidth(); x++ {
			if matrix.Get(x, y) {
				img.SetGray(x+margin, y+margin, color.Gray{})
			}
		}
	}
	if sideways {
		return rotate90(img)
	}
	return img
}

func TestDecodeBarcode(t *testing.T) {
	const digits = "7332504002574"
	if got := DecodeBarcode(barcodeImage(t, digits, false)); got != digits {
		t.Errorf("upright barcode = %q, want %q", got, digits)
	}
	if got := DecodeBarcode(barcodeImage(t, digits, true)); got != digits {
		t.Errorf("sideways barcode = %q, want %q", got, digits)
	}
	if got := DecodeBarcode(image.NewGray(image.Rect(0, 0, 200, 200))); got != "" {
		t.Errorf("blank image = %q, want no barcode", got)
	}
}

func TestIdentifyExactMatch(t *testing.T) {
	fx := newFixture(t)
	const digits = "7332504002574"
	id := fx.capture(t, barcodeImage(t, digits, true))
	fx.fakes.guess = movieGuess("Serenity", 2005)
	fx.fakes.results["movie|Serenity|2005"] = []map[string]any{
		result(16320, "Serenity", "2005-09-25"), result(99, "Serenity Now", "2005-01-01"),
	}

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	item := fx.item(t, id)
	if item.Status != db.StatusIdentified || deref(item.Format) != "Blu-ray" || deref(item.Barcode) != digits {
		t.Errorf("item = status %q format %q barcode %q; want identified Blu-ray %s",
			item.Status, deref(item.Format), deref(item.Barcode), digits)
	}
	if len(item.Titles) != 1 || item.Titles[0].TMDBID == nil || *item.Titles[0].TMDBID != 16320 {
		t.Fatalf("titles = %+v, want TMDB 16320", item.Titles)
	}
	if got := deref(item.Titles[0].PosterPath); got != "/p16320.jpg" {
		t.Errorf("poster = %q", got)
	}
	if fx.fakes.picks != 0 {
		t.Errorf("LLM was asked to pick %d times for an unambiguous match", fx.fakes.picks)
	}
}

func TestIdentifyAmbiguousMatchAsksLLM(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Hamlet", 1990)
	// Two films with the same title in adjacent years: the title alone cannot decide.
	fx.fakes.results["movie|Hamlet|1990"] = []map[string]any{
		result(1, "Hamlet", "1990-12-19"), result(2, "Hamlet", "1991-02-01"),
	}
	pick := int64(2)
	fx.fakes.pick = &pick

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	item := fx.item(t, id)
	if fx.fakes.picks != 1 || len(item.Titles) != 1 || *item.Titles[0].TMDBID != 2 {
		t.Errorf("picks = %d, titles = %+v; want one pick choosing TMDB 2", fx.fakes.picks, item.Titles)
	}
	if item.Status != db.StatusIdentified {
		t.Errorf("status = %q, want identified", item.Status)
	}
}

func TestIdentifyRecordsTokenUsageAndCost(t *testing.T) {
	fx := newFixture(t)
	fx.worker.llm.Price = &Price{Input: 2e-6, CachedInput: 0.5e-6, Output: 10e-6}
	id := fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Hamlet", 1990)
	fx.fakes.results["movie|Hamlet|1990"] = []map[string]any{
		result(1, "Hamlet", "1990-12-19"), result(2, "Hamlet", "1991-02-01"),
	}
	pick := int64(2)
	fx.fakes.pick = &pick

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	runs, err := fx.store.ListRuns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// 600 uncached × $2 + 400 cached × $0.5 + 200 out × $10, per million tokens.
	const perCall = 0.0034
	priced := 0
	for _, r := range runs {
		switch r.Step {
		case "barcode":
			if r.InputTokens != nil || r.CostUSD != nil {
				t.Errorf("barcode run has usage: %+v", r)
			}
		case "llm", "tmdb":
			if r.InputTokens == nil || *r.InputTokens != 1000 || *r.OutputTokens != 200 {
				t.Errorf("%s run tokens = %v/%v, want 1000/200", r.Step, r.InputTokens, r.OutputTokens)
			}
			if r.CostUSD == nil || math.Abs(*r.CostUSD-perCall) > 1e-9 {
				t.Errorf("%s run cost = %v, want %v", r.Step, r.CostUSD, perCall)
			}
			priced++
		}
	}
	if priced != 2 {
		t.Errorf("priced runs = %d, want the vision call and the pick", priced)
	}
	total, err := fx.store.RunsCost(context.Background(), id)
	if err != nil || math.Abs(total-2*perCall) > 1e-9 {
		t.Errorf("RunsCost = %v, %v; want %v", total, err, 2*perCall)
	}
}

func TestIdentifyWithoutPriceRecordsTokensOnly(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Heat", 1995)
	fx.fakes.results["movie|Heat|1995"] = []map[string]any{result(949, "Heat", "1995-12-15")}

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	runs, err := fx.store.ListRuns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if r.Step == "llm" && (r.InputTokens == nil || r.CostUSD != nil) {
			t.Errorf("llm run tokens = %v, cost = %v; want tokens and no cost", r.InputTokens, r.CostUSD)
		}
	}
}

func TestIdentifySeriesKeepsSeasonsInNotes(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	seasons := "Season 2"
	fx.fakes.guess = Guess{Format: "DVD", Confidence: "high", Titles: []GuessTitle{
		{MediaType: "tv", Title: "Firefly", OriginalTitle: "Firefly", Year: intp(2003), Seasons: &seasons}}}
	// Series are searched without a year: the year on a season box is not the first-air year.
	fx.fakes.results["tv|Firefly|"] = []map[string]any{result(1437, "Firefly", "2002-09-20")}

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	item := fx.item(t, id)
	if item.Status != db.StatusIdentified || item.Notes != "Season 2" {
		t.Errorf("status %q notes %q; want identified with notes \"Season 2\"", item.Status, item.Notes)
	}
	if len(item.Titles) != 1 || item.Titles[0].MediaType != "tv" || *item.Titles[0].Year != 2002 {
		t.Errorf("titles = %+v, want the series with first-air year 2002", item.Titles)
	}
}

func TestIdentifyNeedsReview(t *testing.T) {
	t.Run("no TMDB match keeps the guess", func(t *testing.T) {
		fx := newFixture(t)
		id := fx.capture(t, nil)
		fx.fakes.guess = movieGuess("Obscure Film", 1971)

		if err := fx.process(t); err != nil {
			t.Fatal(err)
		}
		item := fx.item(t, id)
		if item.Status != db.StatusNeedsReview || !strings.Contains(deref(item.ReviewReason), "no TMDB match") {
			t.Errorf("status %q reason %q; want needs_review for a missing TMDB match", item.Status, deref(item.ReviewReason))
		}
		if len(item.Titles) != 1 || item.Titles[0].Title != "Obscure Film" || item.Titles[0].TMDBID != nil {
			t.Errorf("titles = %+v, want the unmatched guess", item.Titles)
		}
	})

	t.Run("low confidence", func(t *testing.T) {
		fx := newFixture(t)
		id := fx.capture(t, nil)
		fx.fakes.guess = movieGuess("Serenity", 2005)
		fx.fakes.guess.Confidence = "low"
		fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}

		if err := fx.process(t); err != nil {
			t.Fatal(err)
		}
		if item := fx.item(t, id); item.Status != db.StatusNeedsReview || deref(item.ReviewReason) != "low confidence" {
			t.Errorf("status %q reason %q; want needs_review, low confidence", item.Status, deref(item.ReviewReason))
		}
	})

	t.Run("same titles and format as another item", func(t *testing.T) {
		fx := newFixture(t)
		first, second, dvd := fx.capture(t, nil), fx.capture(t, nil), fx.capture(t, nil)
		fx.fakes.guess = movieGuess("Serenity", 2005)
		fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}
		for range 2 {
			if err := fx.process(t); err != nil {
				t.Fatal(err)
			}
		}
		fx.fakes.guess.Format = "DVD"
		if err := fx.process(t); err != nil {
			t.Fatal(err)
		}

		if item := fx.item(t, first); item.Status != db.StatusIdentified {
			t.Errorf("first copy status = %q, want identified", item.Status)
		}
		item := fx.item(t, second)
		want := "same titles and format as item #" + strconv.FormatInt(first, 10)
		if item.Status != db.StatusNeedsReview || deref(item.ReviewReason) != want {
			t.Errorf("second copy: status %q reason %q; want needs_review, %q", item.Status, deref(item.ReviewReason), want)
		}
		// "Keep both": approving the second copy clears the flag.
		if err := fx.store.ApplyReview(context.Background(), second, db.ItemEdit{}); err != nil {
			t.Fatal(err)
		}
		if item := fx.item(t, second); item.Status != db.StatusIdentified || item.ReviewReason != nil || len(item.Titles) != 1 {
			t.Errorf("after approval: status %q reason %q titles %d; want identified with its title kept",
				item.Status, deref(item.ReviewReason), len(item.Titles))
		}
		// The same movie in another format is not a duplicate.
		if item := fx.item(t, dvd); item.Status != db.StatusIdentified {
			t.Errorf("DVD copy status = %q (%s), want identified", item.Status, deref(item.ReviewReason))
		}
	})
}

func TestIdentifyFailureRetriesThenGoesToReview(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	fx.fakes.guessStatus = http.StatusBadRequest

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := fx.process(t); err == nil {
			t.Fatalf("attempt %d: expected an error", attempt)
		}
		item := fx.item(t, id)
		wantStatus := db.StatusCaptured
		if attempt == maxAttempts {
			wantStatus = db.StatusNeedsReview
		}
		if item.Status != wantStatus {
			t.Fatalf("after attempt %d: status = %q, want %q", attempt, item.Status, wantStatus)
		}
	}
	if reason := deref(fx.item(t, id).ReviewReason); !strings.HasPrefix(reason, "identification failed") {
		t.Errorf("review reason = %q", reason)
	}

	// Re-queueing gives it a fresh set of attempts.
	fx.fakes.guessStatus = 0
	fx.fakes.guess = movieGuess("Serenity", 2005)
	fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}
	if err := fx.store.ResetForIdentification(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	if item := fx.item(t, id); item.Status != db.StatusIdentified || item.ReviewReason != nil {
		t.Errorf("after retry: status %q reason %q; want identified", item.Status, deref(item.ReviewReason))
	}
}

func TestIdentifyOutageDoesNotUseUpAttempts(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	fx.fakes.guessStatus = http.StatusServiceUnavailable

	for range maxAttempts + 1 {
		if err := fx.process(t); err == nil {
			t.Fatal("expected an error")
		}
	}
	if item := fx.item(t, id); item.Status != db.StatusCaptured {
		t.Errorf("status = %q (%s), want still captured after an outage", item.Status, deref(item.ReviewReason))
	}
}

func TestIdentifyRetryReusesPhotoReading(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Serenity", 2005)
	fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}
	fx.fakes.tmdbStatus = http.StatusBadGateway

	if err := fx.process(t); err == nil {
		t.Fatal("expected the TMDB failure")
	}
	fx.fakes.tmdbStatus = 0
	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	if fx.fakes.identifies != 1 {
		t.Errorf("covers were read %d times, want once", fx.fakes.identifies)
	}
	if item := fx.item(t, id); item.Status != db.StatusIdentified {
		t.Errorf("status = %q, want identified", item.Status)
	}

	// Re-queueing by hand reads the covers afresh.
	if err := fx.store.ResetForIdentification(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	if fx.fakes.identifies != 2 {
		t.Errorf("covers were read %d times after re-queueing, want twice", fx.fakes.identifies)
	}
}

func TestIdentifyDoesNotOverwriteManualCorrection(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	fx.fakes.guess = movieGuess("Serenity", 2005)
	fx.fakes.results["movie|Serenity|2005"] = []map[string]any{result(16320, "Serenity", "2005-09-25")}
	format, notes := "DVD", "corrected by hand"
	titles := []db.TitleInput{{MediaType: db.MediaMovie, Title: "Firefly"}}
	// The correction is saved while the worker waits for the LLM.
	fx.fakes.onIdentify = func() {
		edit := db.ItemEdit{Format: &format, Notes: &notes, Titles: &titles}
		if err := fx.store.ApplyReview(context.Background(), id, edit); err != nil {
			t.Error(err)
		}
	}

	if err := fx.process(t); err != nil {
		t.Fatal(err)
	}
	item := fx.item(t, id)
	if item.Status != db.StatusIdentified || deref(item.Format) != "DVD" || item.Notes != notes ||
		len(item.Titles) != 1 || item.Titles[0].Title != "Firefly" {
		t.Errorf("item = status %q format %q notes %q titles %+v; want the manual correction kept",
			item.Status, deref(item.Format), item.Notes, item.Titles)
	}
}

func TestIdentifyUnreadablePhotoGoesStraightToReview(t *testing.T) {
	fx := newFixture(t)
	id := fx.capture(t, nil)
	item := fx.item(t, id)
	if err := os.WriteFile(filepath.Join(fx.dir, item.Images[1].Path), []byte("\xff\xd8broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fx.process(t); err == nil {
		t.Fatal("expected an error")
	}
	if item := fx.item(t, id); item.Status != db.StatusNeedsReview {
		t.Errorf("status = %q, want needs_review after one attempt", item.Status)
	}
	if fx.fakes.identifies != 0 {
		t.Errorf("LLM was asked about an unreadable photo")
	}
}
