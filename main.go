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

	openAIKey, tmdbKey := os.Getenv("OPENAI_API_KEY"), os.Getenv("TMDB_API_KEY")
	var tmdb *identify.TMDB
	if tmdbKey != "" {
		tmdb = &identify.TMDB{APIKey: tmdbKey, HTTP: &http.Client{Timeout: 30 * time.Second}}
		srv.SetTMDB(tmdb)
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
