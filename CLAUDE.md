# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

A personal catalogue of physical discs (DVD/Blu-ray/UHD): a phone photographs each case's front and back, and a background worker identifies the release. `SPEC.md` has the product spec, goals, non-goals and the decisions behind them. Read it before changing behaviour.

## Commands

Tasks run through `task` (Taskfile.yml). Tools like `air` are pinned in `mise.toml` and called via `mise exec --`.

- `task build`: build the frontend, lint, test, then build `build/moviecatalog`
- `task test`: `go test ./...`
- `task lint`: `go vet ./...` plus `svelte-check` (`npm --prefix web run check`)
- `task dev`: Go server on :8080 with rebuild/restart on change (air). Changes to `.sql` also trigger a rebuild, because migrations are embedded.
- `task dev-op`: like `dev`, with API keys resolved from 1Password via `op.env`
- `task web-dev`: Vite dev server on the LAN that proxies `/api` to :8080, so the phone gets live reload
- Single test: `go test ./internal/identify -run TestIdentifyExactMatch`

Runtime config comes from environment variables: `OPENAI_API_KEY`, `TMDB_API_KEY`, `OPENAI_MODEL`, `MOVIECATALOG_ADDR` (default `:8080`), `MOVIECATALOG_DATA_DIR` (default `data`) `WATCHER_URL` (cdon-watcher's base URL, for wishlist offers) and `WATCHER_DB` (development fallback: cdon-watcher's SQLite file, opened read-only, e.g. `../cdon-watcher/data/catalog.db`; ignored when `WATCHER_URL` is set). Identification only runs when both API keys are set. Without them, captures are still stored. Without `TMDB_API_KEY`, the manual TMDB search and `GET /api/series/{id}` return 503, and series are not looked up. Without either watcher setting, the wishlist works without shop offers.

LLM cost estimates use `model_prices_and_context_window.json` (LiteLLM's price list), embedded by `main.go`. It is updated by hand. `TestDefaultModelIsPriced` fails if the default model drops out of it.

Tests never hit the live OpenAI or TMDB APIs. `internal/identify/worker_test.go` runs both clients against an `httptest` fake (`fakes`), and new pipeline tests should use the same fake.

## Architecture

The core rule is "capture now, identify later". Capture only stores evidence. Identification runs asynchronously, can be retried, and can be overridden by hand.

- **`main.go`** wires everything up: store → server → (optional) worker. The server calls `worker.Notify` through `OnPending` whenever items are queued.
- **`internal/db`**: SQLite (modernc, no cgo) and all queries.
  - Migrations are `migrations/NNNN_*.sql`, embedded and applied in name order. The count is tracked in `PRAGMA user_version`, so add new files and never edit applied ones.
  - Item `status` (`captured` → `identified` | `needs_review`) *is* the work queue. There is no separate queue infrastructure.
- **`internal/identify`**: the background `Worker` processes the oldest `captured` item through these steps:
  1. barcode decode (gozxing, never fails the run)
  2. OpenAI Responses API vision call with a strict JSON schema (`Guess`)
  3. TMDB search per title, with the LLM's `Pick` asked to choose only when the matches are ambiguous

  Every step's raw request and response is written to `identification_runs`, which feeds the item log in the UI.

  `SeriesFinder` (`identify/series.go`) is a second background loop that needs only `TMDB_API_KEY`. It fills `titles.collection_id`/`collection_name` from TMDB's `belongs_to_collection` for every matched movie with no `details_checked_at`, whichever path saved it. The worker (`OnSaved`) and the server (`OnTitlesSaved`) wake it. `GET /api/series/{id}` (`server/series.go`) returns a collection's movies in release order with the discs holding each and whether each is on the wishlist, caching TMDB's answer (not the ownership or wishes) in memory for a day.
- **Concurrency and retry rules** (spread across `db/identification.go`, `db/review.go` and `identify/worker.go`):
  - `items.revision` is bumped by manual edits (`ApplyReview`) and by re-queueing (`ResetForIdentification`). The worker's `SaveIdentification`, `SaveCheckpoint` and `RecordFailure` only apply if the revision and the `captured` status are unchanged since pickup. Otherwise they return `db.ErrStale`, and the worker discards its result.
  - `items.checkpoint` caches the barcode and vision result, so a retry after a TMDB failure doesn't repeat the paid vision call. Re-queueing clears it.
  - Errors are classified in `identify/errors.go`. *Transient* errors (network, 429, 5xx) don't count as attempts. A *bad capture* (undecodable photo) goes to review at once. Other errors go to review after `maxAttempts`.
  - Low confidence, a missing TMDB match, or a duplicate (same barcode, or the same matched titles in the same format) sends an item to review.
- **`internal/offers`**: shop offers for wished-for movies, from cdon-watcher (which crawls the shops; this repo owns ownership and the wishlist). `Source` is the interface: `Lookup` returns per movie its offers and `SearchedAt` (when the watcher last searched the shops; `TracksSearches` says whether that is known). At boot, `main.go` pings the configured source in the background (`Ping`: the offers endpoint with no ids, or a query on the watcher's file) and only logs the result; a watcher that is down is never fatal. `WatcherAPI` calls the watcher's `GET /api/v2/offers` (contract in `SPEC.md`, "Connecting to the watcher"); tests use an `httptest` fake. `WatcherDB` reads the watcher's SQLite file directly as a development fallback, so it depends on the watcher's schema (`store_products`, `stores`) and knows nothing about searches. Offers are keyed by TMDB movie id, and the watcher's `bd`/`4k` map to Blu-ray/UHD.
- **Wishlist** (`db/wants.go`, `server/wants.go`): a `wants` row points at a `titles` row (always with a TMDB id). `GET /api/wants` attaches offers in accepted formats and marks a `deal` (in stock and within the price cap). If the offer lookup fails, the list is still returned, with `offers: null` per wish and an error. `GET /api/sync` (`server/sync.go`) is the reverse direction: the watcher pulls owned movies and wishes from it at the start of each refresh, to hide owned movies while browsing and to search the shops for wishes. Its JSON shape is a contract with the watcher (`../cdon-watcher/docs/09_multi_store_plan.md`, phase 2).
- **`internal/photo`**: all image decoding goes through `photo.Load`, which applies the EXIF orientation (Go's decoders ignore it). Uploads are fully decoded once at capture to reject corrupt files, and the original bytes are kept.
- **`internal/server`**: `net/http` ServeMux JSON API under `/api`. It also serves photos (immutable cache headers, thumbnails generated on demand into the data directory) and the embedded SPA.
- **Frontend (`web/`)**: Svelte 5 (runes) + TypeScript + Vite.
  - The build writes to `internal/webui/dist`, which `internal/webui` embeds into the binary. The Go build serves an explanatory 503 page when it's missing.
  - Hash-routed SPA (`App.svelte`): Capture and Catalogue (phone first), item detail with `ReviewPanel`, `Library` (desktop first: a poster grid per title, built client-side from `GET /api/items`; the title sort files a series under its name) and `Wishlist` (`WantRow` per wish). `SeriesList` shows a series in both the item detail and the library panel, with a + to wish for a missing movie. All fetch calls and types live in `api.ts`.
  - Capture stays mounted while hidden, so unsaved photos survive navigation.
  - Capture uses `<input type="file" capture="environment">` rather than `getUserMedia`, so it works on the LAN without HTTPS.
