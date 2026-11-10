# Movie Catalog — Phase 1 Spec (draft 4)

A personal catalogue of ~500 physical discs (DVD, Blu-ray, UHD). Phase 1 is about
getting the collection digitised with as little effort per disc as possible.
A real UI comes later.

The question the catalogue answers: **do I own this movie, and in what quality?**

## Goals

- Capture every disc in one or two sittings: target **≤ 15 seconds of handling per case**.
- End up with a record per item: which movie(s) it contains and in which format.
- Keep the raw captures (photos, barcode) forever, so identification can be re-run
  later with better models or data sources.
- A barebones list to check what has been captured and identified.

## Non-goals (phase 1)

- Statistics, and browsing beyond the library view below.
- Edition tracking: steelbooks, cuts, regions, audio/subtitle languages.
- Barcode lookup services. The barcode is stored, not looked up.
- Loans, wishlist, watch history, ratings.
- Multi-user, auth beyond "it's on my home network".
- Ripping or reading the discs themselves.

## Decisions so far

| Area | Decision |
|---|---|
| Backend | Go, single binary |
| Frontend | Svelte + Vite + TypeScript. Vite dev server with live reload during development; built assets embedded in the Go binary (`embed.FS`) for production |
| Storage | SQLite file + images in a plain directory |
| Identification | OpenAI API (vision + structured output); accuracy over cost and privacy during ingestion |
| Detail level | Movie + format only |
| Box sets | One item linked to every movie it contains |
| TV series | One title per series; seasons go in the item's free-text notes |
| Duplicates | Never interrupt capture; flagged in the review queue |
| Barcode | Decoded deterministically from the photos and stored when readable; no lookup, no AI reading |

## Core idea: capture now, identify later

Capture and identification are separate steps. Scanning is a fast, dumb,
phone-in-hand loop that only stores evidence. Identification runs in the
background on the server and can be retried, re-run and corrected without
touching the shelf again.

```
phone: photo front + photo back ──► server stores images, status = captured
                                         │
                      background worker  ▼
        decode barcode from photos ─► stored on the item if readable
                                         ▼
                    LLM vision (both photos) ─► structured guess
                                         ▼
                      TMDB match ─► canonical title, year, poster, ids
                                         ▼
              status = identified | needs_review (low confidence / no match)
```

## Capture flow (mobile web)

1. Open the capture page on the phone.
2. Take front photo, take back photo. The item is saved and the page resets for the next case.
3. A counter and the last few thumbnails show progress; the last item can be undone.

Notes:

- Use `<input type="file" accept="image/*" capture="environment">` rather than a
  live `getUserMedia` stream: full-resolution native camera, and no HTTPS
  requirement on the LAN.
- No waiting for identification in the loop. Results appear in the viewer as they finish.
- No per-item input at capture time. Box sets are detected by the LLM from the covers.

## Identification pipeline

1. **Barcode**: try to decode EAN/UPC from the back photo, then the front, in Go
   (pure-Go decoder, no cgo). Stored on the item if it decodes; left empty if
   not. Never blocks or fails the pipeline. It serves as a duplicate-detection
   key and as a hook for any future lookup source.
2. **LLM vision**: both photos → JSON:
   - format: DVD / Blu-ray / UHD (a combo pack reports the best one)
   - a list of movies or series, each with title, original title and year
   - for series: which season(s) the item holds, as text for the notes field
   - confidence, and a short free-text reason when unsure

   The response is constrained to a JSON schema (structured output), so the
   worker never parses free text.
3. **TMDB match**: per movie, search by title + year and store the TMDB id,
   canonical titles and poster. The LLM picks between candidates when the search
   is ambiguous.
4. Anything low-confidence or unmatched lands in a **review queue**, as does any
   item whose barcode or (title, format) pair matches an existing item.

The model is a config value (`OPENAI_MODEL`), defaulting to `gpt-6.1-sol`. On the
first six test items it gave the same answers as the flagship `gpt-6-astra` at
about a fifth of the price: roughly 1.4 cents per item, about $7 for 500 items. Every step's raw input and output is
stored so the pipeline is re-runnable per item.

Each LLM call's token counts are stored with its run. The estimated cost is stored too,
at the list price in force at the time. Prices come from LiteLLM's
`model_prices_and_context_window.json`, which is embedded in the binary and updated by hand. A
model missing from it is still used, but its cost is not estimated.

## Data model (sketch)

- **item** — one physical thing on the shelf: id, captured_at, status, format,
  barcode, free-text notes.
- **image** — item id, kind (front / back), file path.
- **title** — a movie or series: TMDB id, title, original title, year, poster.
- **item_title** — many-to-many; a box set is one item with several titles,
  and the same movie on DVD and Blu-ray is two items with one title.
- **identification_run** — item id, step, raw request/response, model, timestamp.

"Best quality owned" for a title is derived: the highest format among its items.

## Architecture

- **Go server**: JSON API under `/api`, image upload and serving, background
  identification worker (in-process, driven by item status in SQLite — no queue
  infrastructure), embedded frontend.
- **Frontend**: small SPA with three areas: capture and viewer (phone first), and library (desktop first). In development its
  dev server proxies `/api` to the Go process and is exposed on the LAN so the
  phone gets live reloads too.
- **Config**: API keys (LLM, TMDB) and data directory via environment variables.

## Viewer (barebones)

- A list of items: front thumbnail, title(s), year, format, status.
- Item detail: both photos, identified fields, raw LLM output.
- Review queue: fix the title by hand (TMDB search box), change format, re-run
  identification, delete. Possible duplicates are shown side by side with
  "keep both" (a genuine second copy) or "delete this one" (scanned twice).

## Library (desktop)

A desktop view for two jobs: browsing the collection, and checking "do I own
this?" while looking at a disc sale in another tab.

- One poster card per movie or series (TMDB poster), however many discs hold it,
  with the best format owned as a badge. Titles from items still in review are
  shown and marked; items still waiting are not shown, only counted.
- Search ignores case, accents and punctuation, and skips the words shop listings
  add ("Blu-ray", "4K", "Steelbook"…), so a pasted listing title finds the movie.
  A year in the search matches the release year ±1. No match says plainly "Not in
  your collection".
- `#/library?q=…` opens with a search, so it can be set up as a browser keyword search.
- Filters: best format, movies or series. Sort: title (ignoring a leading
  "The"/"A"/"An"), year, recently added.
- Selecting a card lists the discs that hold it, each linking to the item detail.

## Build order

1. Go server skeleton, SQLite schema, image upload and serving.
2. Capture page — usable on the phone from here on, so scanning can start
   before identification exists.
3. Identification worker: barcode decode → LLM → TMDB.
4. Viewer list and item detail.
5. Review queue.

## Open questions

None at the moment.
