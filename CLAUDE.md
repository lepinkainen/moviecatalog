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
- `task export` / `task export-op`: write the read-only library site into `site/` (`moviecatalog export --output=… --title=…`; flags go after `--`)
- `task backup`: zip a `VACUUM INTO` snapshot of the database plus `images/` into `backups/` with the `sqlite3`/`zip` CLIs (for the laptop). `moviecatalog backup -dir=… -keep=N` (`internal/backup`) does the same without the CLIs, for a timer on a server.
- `task publish`: `export` with keys from `op.env`, then `rsync` of `site/` to `$PUBLISH_HOST` (also in `op.env`)
- Single test: `go test ./internal/identify -run TestIdentifyExactMatch`

Runtime config comes from environment variables: `OPENAI_API_KEY`, `TMDB_API_KEY`, `OPENAI_MODEL`, `MOVIECATALOG_ADDR` (default `:8080`), `MOVIECATALOG_DATA_DIR` (default `data`) and `WATCHER_URL` (shelfwatch's base URL, for wishlist offers). Identification only runs when both API keys are set. Without them, captures are still stored. Without `TMDB_API_KEY`, the manual TMDB search and `GET /api/series/{id}` return 503, and series are not looked up. Without `WATCHER_URL`, the wishlist works without shop offers.

LLM cost estimates use `model_prices_and_context_window.json` (LiteLLM's price list), embedded by `main.go`. It is updated by hand. `TestDefaultModelIsPriced` fails if the default model drops out of it.

Tests never hit the live OpenAI or TMDB APIs. `internal/identify/worker_test.go` runs both clients against an `httptest` fake (`fakes`), and new pipeline tests should use the same fake.

## Architecture

The core rule is "capture now, identify later". Capture only stores evidence. Identification runs asynchronously, can be retried, and can be overridden by hand.

- **`main.go`** has three subcommands: `serve` (the default), `export` and `backup`. Serving wires everything up: store → server → (optional) worker. The server calls `worker.Notify` through `OnPending` whenever items are queued.
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
  - Low confidence, a missing TMDB match, or a duplicate (same barcode, or the same matched titles in the same format) sends an item to review. For series, the same titles are not duplicates when both items give seasons and they don't overlap (`disjointSeasons`).
- **Seasons**: `item_title_seasons` holds the seasons of a series per item and title. No rows means the item doesn't say. Removing the `item_titles` link cascades to them, so `replaceTitles` rewrites them from `TitleInput.Seasons`. The LLM returns `seasons` (numbers) and `complete_series`. The worker expands a complete series to the seasons TMDB says had aired by the capture date (`airedSeasons`, `TMDB.Show`). Without a match, that sends the item to review. `PATCH /api/items/{id}` takes `seasons` per title, edited with TMDB checkboxes in `SeasonPicker.svelte`. `GET /api/shows/{tmdb_id}` (`server/series.go`) joins TMDB's `/tv/{id}` seasons with `db.ShowCopies` through `series.AssembleShow`, cached for a day like collections. `SeasonList.svelte` shows it.
- **`internal/offers`**: shop offers for wished-for movies, from shelfwatch (which crawls the shops; this repo owns ownership and the wishlist). `Source` is the interface: `Lookup` returns per movie its offers and `SearchedAt` (when the watcher last searched the shops, nil if not yet). At boot, `main.go` pings the watcher in the background (`Ping`: the offers endpoint with no ids) and only logs the result; a watcher that is down is never fatal. `WatcherAPI` calls the watcher's `GET /api/v2/offers` (contract in `SPEC.md`, "Connecting to the watcher"); tests use an `httptest` fake. Offers are keyed by TMDB movie id, and the watcher's `bd`/`4k` map to Blu-ray/UHD.
- **Wishlist** (`db/wants.go`, `server/wants.go`): a `wants` row points at a `titles` row (always with a TMDB id), and for a series optionally at one `season` (unique per title and season, `IFNULL(season, -1)`). A season wish is collected only by an item holding that season, and appears as `wanted` in `GET /api/shows/{id}`. An item that becomes `identified` (`SaveIdentification` without review, or `ApplyReview`) sets `collected_on` (the capture's local date) on the open wishes for its titles that were created before the item's `captured_at` (`collectWants`), in the same transaction. Wishes are never deleted automatically. A collected wish gets no offers, is left out of `/api/sync`'s `wanted` and the series `wanted` flag, and `CreateWant` reopens it. `GET /api/wants` attaches offers in accepted formats and marks a `deal` (in stock and within the price cap). If the offer lookup fails, the list is still returned, with `offers: null` per wish and an error. `GET /api/sync` (`server/sync.go`) is the reverse direction: the watcher pulls owned movies and wishes from it at the start of each refresh, to hide owned movies while browsing and to search the shops for wishes. Its JSON shape is a contract with the watcher (`../shelfwatch/docs/09_multi_store_plan.md`, phase 2).
- **Static export** (`internal/export`, `moviecatalog export`): writes the Library as a read-only site for a public static host: `index.html` and `assets/` from a second Vite build (`web/vite.export.config.ts`, `export.html` → `ExportApp.svelte`, embedded as `webui.ExportFS`), plus `data.json`. The data is an allowlist (`export.Item`/`export.Title`): identified items only, capture date without time, no barcodes, notes, photos or wishes. Series and season lists (`data.series`, `data.shows`) are fetched from TMDB at export time (skipped without `TMDB_API_KEY`), assembled by `internal/series` like `GET /api/series/{id}` and `/api/shows/{id}`, but counting only identified discs. Season numbers are exported, notes are not. The export replaces only `index.html`, `data.json` and `assets/`, and refuses a directory with a page it did not write. `Library` and `SeriesList` take `load`/`loadSeries` and `readonly` props for it.
- **`internal/photo`**: all image decoding goes through `photo.Load`, which applies the EXIF orientation (Go's decoders ignore it). Uploads are fully decoded once at capture to reject corrupt files, and the original bytes are kept.
- **`internal/server`**: `net/http` ServeMux JSON API under `/api`. It also serves photos (immutable cache headers, thumbnails generated on demand into the data directory) and the embedded SPA.
- **Frontend (`web/`)**: Svelte 5 (runes) + TypeScript + Vite.
  - The build writes to `internal/webui/dist`, which `internal/webui` embeds into the binary. The Go build serves an explanatory 503 page when it's missing.
  - Hash-routed SPA (`App.svelte`): Capture and Catalogue (phone first), item detail with `ReviewPanel`, `Library` (desktop first: a poster grid per title, built client-side from `GET /api/items`; the title sort files a series under its name) and `Wishlist` (`WantRow` per wish). `SeriesList` shows a series in both the item detail and the library panel, with a + to wish for a missing movie. All fetch calls and types live in `api.ts`.
  - Capture stays mounted while hidden, so unsaved photos survive navigation.
  - Capture uses `<input type="file" capture="environment">` rather than `getUserMedia`, so it works on the LAN without HTTPS.
