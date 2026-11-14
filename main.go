package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/backup"
	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/export"
	"github.com/lepinkainen/moviecatalog/internal/identify"
	"github.com/lepinkainen/moviecatalog/internal/offers"
	"github.com/lepinkainen/moviecatalog/internal/server"
	"github.com/lepinkainen/moviecatalog/internal/webui"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultModel is used when OPENAI_MODEL is unset.
const defaultModel = "gpt-6.1-sol"

// modelPrices is LiteLLM's model price list, used for cost estimates. It is
// updated by hand from
// https://raw.githubusercontent.com/BerriAI/litellm/refs/heads/main/model_prices_and_context_window.json
//
//go:embed model_prices_and_context_window.json
var modelPrices []byte

// checkWatcher confirms at startup that the shop watcher answers. A watcher
// that is down is not fatal: the wishlist shows without offers until it is up.
func checkWatcher(ctx context.Context, log *slog.Logger, ping func(context.Context) error, base string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := ping(ctx); err != nil {
		log.Warn("shop watcher is not responding; the wishlist will show without offers until it is",
			"watcher_url", base, "error", err)
		return
	}
	log.Info("shop watcher is responding", "watcher_url", base, "duration", time.Since(start).Round(time.Millisecond))
}

const usage = `Usage:
  moviecatalog [serve]                 run the server (configured by environment variables)
  moviecatalog export [flags]          write the library as a read-only static site
  moviecatalog backup [flags]          zip the database and original photos

Run "moviecatalog export -h" or "moviecatalog backup -h" for the flags.
`

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = run(log)
	case "export":
		err = runExport(log, args)
	case "backup":
		err = runBackup(log, args)
	case "-h", "-help", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// runExport writes the library page with the identified collection into a
// directory, for uploading to a static web host.
func runExport(log *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), `Usage: moviecatalog export [flags]

Writes index.html, data.json and assets/ into the output directory, replacing
an earlier export there and leaving other files alone. Only identified discs
are included, with their titles, formats and series; barcodes, notes, photos
and the wishlist stay out. Set TMDB_API_KEY to include the series and season
lists.

Flags:
`)
		flags.PrintDefaults()
	}
	out := flags.String("output", "site", "directory to write the site into")
	title := flags.String("title", "Movie collection", "heading and page title")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	dataDir := envOr("MOVIECATALOG_DATA_DIR", "data")
	dbPath := filepath.Join(dataDir, "moviecatalog.db")
	// Opening a missing database would create an empty one and export nothing.
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no catalogue at %s (set MOVIECATALOG_DATA_DIR): %w", dbPath, err)
	}
	store, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := export.Options{Dir: *out, Title: *title, Now: time.Now()}
	if key := os.Getenv("TMDB_API_KEY"); key != "" {
		opts.TMDB = &identify.TMDB{APIKey: key, HTTP: &http.Client{Timeout: 30 * time.Second}}
	}
	sum, err := export.Run(ctx, store, webui.ExportFS(), opts, log)
	if err != nil {
		return err
	}
	log.Info("exported", "dir", *out, "discs", sum.Items, "titles", sum.Titles, "series", sum.Series, "shows", sum.Shows)
	return nil
}

// runBackup zips the database and original photos into a timestamped file,
// for running from a timer.
func runBackup(log *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), `Usage: moviecatalog backup [flags]

Writes a consistent copy of the database and the original photos from
MOVIECATALOG_DATA_DIR into <dir>/moviecatalog-YYYYMMDD-HHMMSS.zip. Thumbnails
are left out; the server regenerates them. To restore, unzip the file into an
empty data directory.

Flags:
`)
		flags.PrintDefaults()
	}
	dir := flags.String("dir", "backups", "directory to write the zip into")
	keep := flags.Int("keep", 0, "after a successful backup, delete all but the newest N backups in -dir (0 keeps all)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *keep < 0 {
		return fmt.Errorf("-keep must not be negative")
	}

	dataDir := envOr("MOVIECATALOG_DATA_DIR", "data")
	out := filepath.Join(*dir, backup.FileName(time.Now()))
	start := time.Now()
	sum, err := backup.Run(dataDir, out)
	if err != nil {
		return err
	}
	log.Info("backed up", "file", out, "images", sum.Images, "mb", sum.Bytes>>20,
		"duration", time.Since(start).Round(time.Millisecond))
	if *keep > 0 {
		deleted, err := backup.Prune(*dir, *keep)
		if err != nil {
			return fmt.Errorf("prune old backups: %w", err)
		}
		for _, name := range deleted {
			log.Info("deleted old backup", "file", filepath.Join(*dir, name))
		}
	}
	return nil
}

func run(log *slog.Logger) error {
	addr := envOr("MOVIECATALOG_ADDR", ":8080")
	dataDir := envOr("MOVIECATALOG_DATA_DIR", "data")

	if err := os.MkdirAll(filepath.Join(dataDir, "images"), 0o755); err != nil {
		return err
	}
	store, err := db.Open(filepath.Join(dataDir, "moviecatalog.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(store, dataDir, webui.FS(), log)

	if base := os.Getenv("WATCHER_URL"); base != "" {
		watcher, err := offers.NewWatcherAPI(base, &http.Client{Timeout: 10 * time.Second})
		if err != nil {
			return err
		}
		srv.SetOffers(watcher)
		log.Info("wishlist offers enabled", "watcher_url", base)
		go checkWatcher(ctx, log, watcher.Ping, base)
	}

	openAIKey, tmdbKey := os.Getenv("OPENAI_API_KEY"), os.Getenv("TMDB_API_KEY")
	var tmdb *identify.TMDB
	var series *identify.SeriesFinder
	if tmdbKey != "" {
		tmdb = &identify.TMDB{APIKey: tmdbKey, HTTP: &http.Client{Timeout: 30 * time.Second}}
		srv.SetTMDB(tmdb)
		series = identify.NewSeriesFinder(store, tmdb, log)
		srv.OnTitlesSaved(series.Notify)
		go series.Run(ctx)
	}
	if openAIKey != "" && tmdb != nil {
		llm := &identify.OpenAI{
			APIKey: openAIKey,
			Model:  envOr("OPENAI_MODEL", defaultModel),
			HTTP:   &http.Client{Timeout: 5 * time.Minute},
		}
		price, err := identify.LookupPrice(modelPrices, llm.Model)
		if err != nil {
			return err
		}
		llm.Price = price
		worker := identify.NewWorker(store, dataDir, llm, tmdb, log)
		worker.OnSaved = series.Notify
		srv.OnPending(worker.Notify)
		go worker.Run(ctx)
		if price != nil {
			log.Info("identification enabled", "model", llm.Model,
				"usd_per_1m_input", price.Input*1e6, "usd_per_1m_output", price.Output*1e6)
		} else {
			log.Warn("identification enabled, but the model is not in the price list; costs will not be estimated",
				"model", llm.Model)
		}
	} else {
		log.Warn("identification disabled: set OPENAI_API_KEY and TMDB_API_KEY to enable it; captures are still stored")
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr, "data_dir", dataDir)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
