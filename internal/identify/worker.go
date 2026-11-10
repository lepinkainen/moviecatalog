package identify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

const (
	// maxAttempts is how often a failing item is retried before it goes to review.
	maxAttempts = 3
	// retryDelay spaces out retries so an outage does not burn through the attempts.
	retryDelay = 30 * time.Second
	// maxCandidates caps the TMDB results shown to the LLM when it has to choose.
	maxCandidates = 8
)

// Worker identifies captured items one at a time in the background.
type Worker struct {
	store   *db.Store
	dataDir string
	llm     *OpenAI
	tmdb    *TMDB
	log     *slog.Logger
	wake    chan struct{}
}

// NewWorker creates a worker reading photos from dataDir.
func NewWorker(store *db.Store, dataDir string, llm *OpenAI, tmdb *TMDB, log *slog.Logger) *Worker {
	return &Worker{store: store, dataDir: dataDir, llm: llm, tmdb: tmdb, log: log, wake: make(chan struct{}, 1)}
}

// Notify tells the worker there may be new items to identify.
func (w *Worker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run processes captured items until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := w.ProcessNext(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			w.log.Error("identification failed", "error", err)
			sleep(ctx, retryDelay)
		case !processed:
			select {
			case <-ctx.Done():
			case <-w.wake:
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// ProcessNext identifies the oldest captured item. It reports whether there was one.
func (w *Worker) ProcessNext(ctx context.Context) (bool, error) {
	item, err := w.store.NextCaptured(ctx)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	start := time.Now()
	if err := w.process(ctx, item); err != nil {
		if ctx.Err() == nil {
			// A shutdown mid-request is not the item's fault.
			if recErr := w.store.RecordFailure(ctx, item.ID, err.Error(), maxAttempts); recErr != nil {
				w.log.Error("record failure", "item", item.ID, "error", recErr)
			}
		}
		return true, fmt.Errorf("item %d: %w", item.ID, err)
	}
	if done, err := w.store.GetItem(ctx, item.ID); err == nil {
		titles := make([]string, len(done.Titles))
		for i, t := range done.Titles {
			titles[i] = t.Title
		}
		w.log.Info("identified", "item", done.ID, "status", done.Status, "format", deref(done.Format),
			"titles", strings.Join(titles, " / "), "review_reason", deref(done.ReviewReason),
			"duration", time.Since(start).Round(time.Millisecond))
	}
	return true, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (w *Worker) process(ctx context.Context, item *db.Item) error {
	photos := map[string]string{}
	for _, img := range item.Images {
		photos[img.Kind] = filepath.Join(w.dataDir, img.Path)
	}
	front, err := loadImage(photos[db.KindFront])
	if err != nil {
		return fmt.Errorf("front photo: %w", err)
	}
	back, err := loadImage(photos[db.KindBack])
	if err != nil {
		return fmt.Errorf("back photo: %w", err)
	}

	// Step 1: barcode, deterministic and optional.
	barcode := DecodeBarcode(back)
	if barcode == "" {
		barcode = DecodeBarcode(front)
	}
	w.record(ctx, db.Run{ItemID: item.ID, Step: "barcode", Response: toJSON(object{"barcode": barcode})})

	// Step 2: the LLM reads the covers.
	frontJPEG, err := encodeForLLM(front)
	if err != nil {
		return fmt.Errorf("prepare front photo: %w", err)
	}
	backJPEG, err := encodeForLLM(back)
	if err != nil {
		return fmt.Errorf("prepare back photo: %w", err)
	}
	guess, raw, err := w.llm.Identify(ctx, frontJPEG, backJPEG)
	run := db.Run{ItemID: item.ID, Step: "llm", Model: w.llm.Model, Response: raw,
		Request: toJSON(object{"instructions": identifyInstructions, "images": []string{db.KindFront, db.KindBack}})}
	if err != nil {
		run.Error = err.Error()
		w.record(ctx, run)
		return err
	}
	w.record(ctx, run)

	// Step 3: match every title against TMDB.
	ident := db.Identification{Format: guess.Format, Barcode: barcode, Confidence: guess.Confidence}
	var notes []string
	if guess.Confidence == "low" {
		ident.ReviewReasons = append(ident.ReviewReasons, "low confidence")
	}
	if len(guess.Titles) == 0 {
		ident.ReviewReasons = append(ident.ReviewReasons, "no title recognised")
	}
	for _, gt := range guess.Titles {
		title := db.TitleInput{MediaType: gt.MediaType, Title: gt.Title, OriginalTitle: gt.OriginalTitle, Year: gt.Year}
		match, err := w.match(ctx, item.ID, gt)
		if err != nil {
			return err
		}
		if match != nil {
			title = db.TitleInput{TMDBID: &match.ID, MediaType: match.MediaType, Title: match.Title,
				OriginalTitle: match.OriginalTitle, Year: match.Year, PosterPath: match.PosterPath}
		} else {
			ident.ReviewReasons = append(ident.ReviewReasons, fmt.Sprintf("no TMDB match for %q", gt.Title))
		}
		ident.Titles = append(ident.Titles, title)
		if gt.Seasons != nil && *gt.Seasons != "" {
			if len(guess.Titles) > 1 {
				notes = append(notes, title.Title+": "+*gt.Seasons)
			} else {
				notes = append(notes, *gt.Seasons)
			}
		}
	}
	if guess.Notes != "" {
		notes = append(notes, guess.Notes)
	}
	ident.Notes = strings.Join(notes, "; ")

	return w.store.SaveIdentification(ctx, item.ID, ident)
}

// match finds the TMDB entry for a title read off the case, or nil when there is none.
// A single exact title match is accepted directly; anything less clear goes to the LLM.
func (w *Worker) match(ctx context.Context, itemID int64, gt GuessTitle) (*Candidate, error) {
	type search struct {
		query string
		year  *int
	}
	var searches []search
	add := func(query string, year *int) {
		if query != "" {
			searches = append(searches, search{query, year})
		}
	}
	// A series' year on a season box is unreliable, so series are searched by name only.
	if gt.MediaType == db.MediaTV {
		add(gt.Title, nil)
		if normalize(gt.OriginalTitle) != normalize(gt.Title) {
			add(gt.OriginalTitle, nil)
		}
	} else {
		if gt.Year != nil {
			add(gt.Title, gt.Year)
			if normalize(gt.OriginalTitle) != normalize(gt.Title) {
				add(gt.OriginalTitle, gt.Year)
			}
		}
		add(gt.Title, nil)
	}

	var candidates []Candidate
	seen := map[int64]bool{}
	var chosen *Candidate
	method := "none"
	for _, s := range searches {
		found, err := w.tmdb.Search(ctx, gt.MediaType, s.query, s.year)
		if err != nil {
			w.record(ctx, db.Run{ItemID: itemID, Step: "tmdb", Request: toJSON(gt), Error: err.Error()})
			return nil, err
		}
		for _, c := range found {
			if !seen[c.ID] {
				seen[c.ID] = true
				candidates = append(candidates, c)
			}
		}
		if exact := exactMatches(gt, candidates); len(exact) == 1 {
			chosen, method = &exact[0], "exact"
			break
		}
		if len(candidates) > 0 && s.year != nil {
			// Year-restricted results exist but are ambiguous: let the LLM choose among those.
			break
		}
	}

	var pickRaw string
	if chosen == nil && len(candidates) > 0 {
		if len(candidates) > maxCandidates {
			candidates = candidates[:maxCandidates]
		}
		id, raw, err := w.llm.Pick(ctx, gt, candidates)
		pickRaw = raw
		if err != nil {
			w.record(ctx, db.Run{ItemID: itemID, Step: "tmdb", Model: w.llm.Model, Request: toJSON(gt),
				Response: toJSON(object{"candidates": candidates, "pick": raw}), Error: err.Error()})
			return nil, err
		}
		method = "llm: none fits"
		if id != nil {
			for i := range candidates {
				if candidates[i].ID == *id {
					chosen, method = &candidates[i], "llm"
				}
			}
		}
	}

	run := db.Run{ItemID: itemID, Step: "tmdb", Request: toJSON(gt),
		Response: toJSON(object{"candidates": candidates, "method": method, "chosen": chosen, "pick": pickRaw})}
	if pickRaw != "" {
		run.Model = w.llm.Model
	}
	w.record(ctx, run)
	return chosen, nil
}

// exactMatches returns the candidates whose title equals the guess and, for
// movies, whose year is within one of it (release years differ by country).
func exactMatches(gt GuessTitle, candidates []Candidate) []Candidate {
	want := map[string]bool{normalize(gt.Title): true, normalize(gt.OriginalTitle): true}
	delete(want, "")
	var out []Candidate
	for _, c := range candidates {
		if !want[normalize(c.Title)] && !want[normalize(c.OriginalTitle)] {
			continue
		}
		if gt.MediaType != db.MediaTV {
			if gt.Year == nil || c.Year == nil || *c.Year < *gt.Year-1 || *c.Year > *gt.Year+1 {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// normalize reduces a title to lower-case letters and digits for comparison.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// record stores a run; the audit trail must never fail the identification itself.
func (w *Worker) record(ctx context.Context, run db.Run) {
	if err := w.store.AddRun(ctx, run); err != nil {
		w.log.Warn("record run", "item", run.ItemID, "step", run.Step, "error", err)
	}
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%q", err.Error())
	}
	return string(b)
}
