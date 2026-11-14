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
	"github.com/lepinkainen/moviecatalog/internal/photo"
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
	// OnSaved, when set, is called after an identification has been saved.
	OnSaved func()
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
	err = w.process(ctx, item)
	if errors.Is(err, db.ErrStale) {
		// Corrected by hand, re-queued or deleted meanwhile: that change wins.
		w.log.Info("identification discarded, item changed meanwhile", "item", item.ID)
		return true, nil
	}
	if err != nil {
		// A shutdown mid-request or an outage of an outside service is not the item's fault.
		if ctx.Err() == nil && !isTransient(err) {
			attempts := maxAttempts
			if isBadCapture(err) {
				attempts = 1
			}
			recErr := w.store.RecordFailure(ctx, item.ID, item.Revision, err.Error(), attempts)
			if recErr != nil && !errors.Is(recErr, db.ErrStale) {
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
		attrs := []any{"item", done.ID, "status", done.Status, "format", deref(done.Format),
			"titles", strings.Join(titles, " / "), "review_reason", deref(done.ReviewReason),
			"duration", time.Since(start).Round(time.Millisecond)}
		if w.llm.Price != nil {
			// Every run of the item, so retries and earlier attempts are included.
			if cost, err := w.store.RunsCost(ctx, done.ID); err == nil {
				attrs = append(attrs, "cost_usd", fmt.Sprintf("%.4f", cost))
			}
		}
		w.log.Info("identified", attrs...)
	}
	return true, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// checkpoint is what the photo-reading steps found, kept so that a retry
// after a later failure does not pay for the vision call again.
type checkpoint struct {
	Barcode string `json:"barcode"`
	Guess   Guess  `json:"guess"`
}

func (w *Worker) process(ctx context.Context, item *db.Item) error {
	cp, err := w.readPhotos(ctx, item)
	if err != nil {
		return err
	}
	guess, barcode := &cp.Guess, cp.Barcode

	// Step 3: match every title against TMDB.
	ident := db.Identification{Format: guess.Format, Barcode: barcode, Confidence: guess.Confidence}
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
		if gt.MediaType == db.MediaTV {
			title.Seasons = gt.Seasons
			if gt.CompleteSeries {
				if match == nil {
					ident.ReviewReasons = append(ident.ReviewReasons, fmt.Sprintf("%q is a complete series: tick its seasons", gt.Title))
				} else {
					aired, err := w.airedSeasons(ctx, item, match.ID)
					if err != nil {
						return err
					}
					title.Seasons = append(title.Seasons, aired...)
				}
			}
			title.Seasons = db.NormalizeSeasons(title.Seasons)
		}
		ident.Titles = append(ident.Titles, title)
	}
	ident.Notes = guess.Notes

	if err := w.store.SaveIdentification(ctx, item.ID, item.Revision, ident); err != nil {
		return err
	}
	if w.OnSaved != nil {
		w.OnSaved()
	}
	return nil
}

// readPhotos runs the barcode and LLM steps, or returns their stored result
// from an earlier attempt.
func (w *Worker) readPhotos(ctx context.Context, item *db.Item) (*checkpoint, error) {
	stored, err := w.store.Checkpoint(ctx, item.ID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, db.ErrStale
	}
	if err != nil {
		return nil, err
	}
	if stored != "" {
		var cp checkpoint
		if err := json.Unmarshal([]byte(stored), &cp); err == nil {
			return &cp, nil
		}
		w.log.Warn("ignoring unreadable checkpoint", "item", item.ID)
	}

	photos := map[string]string{}
	for _, img := range item.Images {
		photos[img.Kind] = filepath.Join(w.dataDir, img.Path)
	}
	front, err := photo.Load(photos[db.KindFront])
	if err != nil {
		return nil, &badCaptureError{fmt.Errorf("front photo: %w", err)}
	}
	back, err := photo.Load(photos[db.KindBack])
	if err != nil {
		return nil, &badCaptureError{fmt.Errorf("back photo: %w", err)}
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
		return nil, &badCaptureError{fmt.Errorf("prepare front photo: %w", err)}
	}
	backJPEG, err := encodeForLLM(back)
	if err != nil {
		return nil, &badCaptureError{fmt.Errorf("prepare back photo: %w", err)}
	}
	guess, raw, err := w.llm.Identify(ctx, frontJPEG, backJPEG)
	run := db.Run{ItemID: item.ID, Step: "llm", Model: w.llm.Model, Response: raw,
		Request: toJSON(object{"instructions": identifyInstructions, "images": []string{db.KindFront, db.KindBack}})}
	w.addUsage(&run, raw)
	if err != nil {
		run.Error = err.Error()
		w.record(ctx, run)
		return nil, err
	}
	w.record(ctx, run)

	cp := &checkpoint{Barcode: barcode, Guess: *guess}
	if err := w.store.SaveCheckpoint(ctx, item.ID, item.Revision, toJSON(cp)); err != nil {
		return nil, err
	}
	return cp, nil
}

// airedSeasons returns the seasons of a TMDB series that had aired by the
// day the item was captured: what a complete-series box holds. Specials do
// not count.
func (w *Worker) airedSeasons(ctx context.Context, item *db.Item, tmdbID int64) ([]int, error) {
	show, err := w.tmdb.Show(ctx, tmdbID)
	run := db.Run{ItemID: item.ID, Step: "tmdb", Request: toJSON(object{"tv": tmdbID})}
	if err != nil {
		run.Error = err.Error()
		w.record(ctx, run)
		return nil, err
	}
	captured := item.CapturedAt.Local().Format("2006-01-02")
	aired := []int{}
	for _, s := range show.Seasons {
		if s.Number > 0 && s.AirDate != "" && s.AirDate <= captured {
			aired = append(aired, s.Number)
		}
	}
	run.Response = toJSON(object{"complete_series_seasons": aired})
	w.record(ctx, run)
	return aired, nil
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
			run := db.Run{ItemID: itemID, Step: "tmdb", Model: w.llm.Model, Request: toJSON(gt),
				Response: toJSON(object{"candidates": candidates, "pick": raw}), Error: err.Error()}
			w.addUsage(&run, raw)
			w.record(ctx, run)
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
		w.addUsage(&run, pickRaw)
	}
	w.record(ctx, run)
	return chosen, nil
}

// addUsage copies the token usage of a raw OpenAI response onto run and,
// when the model's price is known, the estimated cost.
func (w *Worker) addUsage(run *db.Run, raw string) {
	u := parseUsage(raw)
	if u == nil {
		return
	}
	run.InputTokens, run.OutputTokens = &u.InputTokens, &u.OutputTokens
	if w.llm.Price != nil {
		cost := w.llm.Price.Cost(*u)
		run.CostUSD = &cost
	}
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
