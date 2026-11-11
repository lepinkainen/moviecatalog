package main

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
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
func checkWatcher(ctx context.Context, log *slog.Logger, ping func(context.Context) error, key, where string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := ping(ctx); err != nil {
		log.Warn("shop watcher is not responding; the wishlist will show without offers until it is",
			key, where, "error", err)
		return
	}
	log.Info("shop watcher is responding", key, where, "duration", time.Since(start).Round(time.Millisecond))
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
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

	// The watcher's HTTP API is the real connection; its database file is a
	// development fallback.
	if base := os.Getenv("WATCHER_URL"); base != "" {
		watcher, err := offers.NewWatcherAPI(base, &http.Client{Timeout: 10 * time.Second})
		if err != nil {
			return err
		}
		srv.SetOffers(watcher)
		log.Info("wishlist offers enabled", "watcher_url", base)
		go checkWatcher(ctx, log, watcher.Ping, "watcher_url", base)
	} else if path := os.Getenv("WATCHER_DB"); path != "" {
		watcher, err := offers.OpenWatcherDB(path)
		if err != nil {
			return err
		}
		defer watcher.Close()
		srv.SetOffers(watcher)
		log.Info("wishlist offers enabled", "watcher_db", path)
		go checkWatcher(ctx, log, watcher.Ping, "watcher_db", path)
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
