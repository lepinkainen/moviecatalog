// Package export writes the library as a read-only static site: the built
// library page, its assets and a data.json holding the collection, ready to be
// uploaded to any static web host.
//
// The site is public, so the data is an allowlist: titles, formats and series.
// Barcodes, notes, photos, the wishlist and items still in review stay out.
package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/identify"
	"github.com/lepinkainen/moviecatalog/internal/series"
)

// pageFile is the library page in the built assets; it is written as index.html.
const pageFile = "export.html"

// The files and directories an export writes. Anything else in the output
// directory (a CNAME, robots.txt, .git) is left alone.
const (
	indexFile = "index.html"
	dataFile  = "data.json"
	assetsDir = "assets"
)

// Collections looks up TMDB collections; *identify.TMDB satisfies it.
type Collections interface {
	Collection(ctx context.Context, id int64) (*identify.Collection, error)
}

// Options configures an export.
type Options struct {
	// Dir is the output directory, created if missing.
	Dir string
	// Title is the page's heading.
	Title string
	// TMDB fetches the series lists. Without it the series panels are left
	// out; series names still group and search.
	TMDB Collections
	Now  time.Time
}

// Data is data.json, read by the export page.
type Data struct {
	Title       string `json:"title"`
	GeneratedAt string `json:"generated_at"`
	Items       []Item `json:"items"`
	// Series maps a TMDB collection id to its movies.
	Series map[int64]series.Series `json:"series"`
}

// Item is one disc, shaped like the API's item so the library page can read it.
type Item struct {
	ID int64 `json:"id"`
	// CapturedAt is the capture date only, for "recently added".
	CapturedAt string  `json:"captured_at"`
	Status     string  `json:"status"`
	Format     *string `json:"format"`
	Titles     []Title `json:"titles"`
}

// Title is a movie or series on a disc.
type Title struct {
	ID            int64        `json:"id"`
	TMDBID        *int64       `json:"tmdb_id"`
	MediaType     string       `json:"media_type"`
	Title         string       `json:"title"`
	OriginalTitle *string      `json:"original_title"`
	Year          *int         `json:"year"`
	PosterPath    *string      `json:"poster_path"`
	Series        *TitleSeries `json:"series"`
}

// TitleSeries names the series a movie belongs to.
type TitleSeries struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Summary says what an export wrote.
type Summary struct {
	Items  int
	Titles int
	Series int
}

// Run writes the site into opts.Dir. assets holds the built export page
// (webui.ExportFS).
func Run(ctx context.Context, store *db.Store, assets fs.FS, opts Options, log *slog.Logger) (Summary, error) {
	if _, err := fs.Stat(assets, pageFile); err != nil {
		return Summary{}, errors.New("the export page is not built into this binary; run `task build` (or `task web-build` before `go build`)")
	}
	data, err := collect(ctx, store, opts, log)
	if err != nil {
		return Summary{}, err
	}
	if err := prepare(opts.Dir); err != nil {
		return Summary{}, err
	}
	if err := copyAssets(assets, opts.Dir); err != nil {
		return Summary{}, err
	}
	body, err := json.Marshal(data)
	if err != nil {
		return Summary{}, err
	}
	if err := os.WriteFile(filepath.Join(opts.Dir, dataFile), body, 0o644); err != nil {
		return Summary{}, err
	}

	sum := Summary{Items: len(data.Items), Series: len(data.Series)}
	titles := map[int64]bool{}
	for _, it := range data.Items {
		for _, t := range it.Titles {
			titles[t.ID] = true
		}
	}
	sum.Titles = len(titles)
	return sum, nil
}

func collect(ctx context.Context, store *db.Store, opts Options, log *slog.Logger) (*Data, error) {
	all, err := store.ListItems(ctx)
	if err != nil {
		return nil, err
	}
	data := &Data{Title: opts.Title, GeneratedAt: opts.Now.UTC().Format(time.RFC3339),
		Items: []Item{}, Series: map[int64]series.Series{}}
	var seriesIDs []int64
	for _, it := range all {
		// Items in review may be wrong guesses, and waiting ones have no titles yet.
		if it.Status != db.StatusIdentified || len(it.Titles) == 0 {
			continue
		}
		item := Item{ID: it.ID, CapturedAt: it.CapturedAt.Local().Format("2006-01-02"), Status: it.Status,
			Format: it.Format, Titles: make([]Title, 0, len(it.Titles))}
		for _, t := range it.Titles {
			title := Title{ID: t.ID, TMDBID: t.TMDBID, MediaType: t.MediaType, Title: t.Title,
				OriginalTitle: t.OriginalTitle, Year: t.Year, PosterPath: t.PosterPath}
			if t.Series != nil {
				title.Series = &TitleSeries{ID: t.Series.ID, Name: t.Series.Name}
				if !slices.Contains(seriesIDs, t.Series.ID) {
					seriesIDs = append(seriesIDs, t.Series.ID)
				}
			}
			item.Titles = append(item.Titles, title)
		}
		data.Items = append(data.Items, item)
	}

	if opts.TMDB == nil {
		if len(seriesIDs) > 0 {
			log.Warn("TMDB_API_KEY is not set; the series lists are left out of the export", "series", len(seriesIDs))
		}
		return data, nil
	}
	slices.Sort(seriesIDs)
	for _, id := range seriesIDs {
		c, err := opts.TMDB.Collection(ctx, id)
		if errors.Is(err, identify.ErrNotFound) {
			log.Warn("series not found on TMDB; left out", "id", id)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("series %d: %w", id, err)
		}
		copies, err := store.OwnedCopies(ctx, series.TMDBIDs(c))
		if err != nil {
			return nil, err
		}
		// Only discs the site shows, so every owned movie links to a card.
		copies = slices.DeleteFunc(copies, func(c db.OwnedCopy) bool { return c.Status != db.StatusIdentified })
		data.Series[id] = series.Assemble(c, copies, nil, opts.Now)
	}
	return data, nil
}

// prepare creates dir, or clears what an earlier export wrote into it. It
// refuses a directory holding a page or assets that an export did not write,
// so a mistyped path does not overwrite another site.
func prepare(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if !exists(dataFile) && (exists(indexFile) || exists(assetsDir)) {
		return fmt.Errorf("%s already holds an %s or %s/ that is not from an export; choose an empty directory", dir, indexFile, assetsDir)
	}
	for _, name := range []string{indexFile, dataFile, assetsDir} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyAssets(assets fs.FS, dir string) error {
	return fs.WalkDir(assets, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Base(name) == ".gitkeep" {
			return nil
		}
		out := name
		if name == pageFile {
			out = indexFile
		}
		body, err := fs.ReadFile(assets, name)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(out))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
}
