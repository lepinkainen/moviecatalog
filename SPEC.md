# Movie Catalog — Spec (draft 5)

A personal catalogue of ~500 physical discs (DVD, Blu-ray, UHD). Phase 1 is about
getting the collection digitised with as little effort per disc as possible.
A real UI comes later.

The question the catalogue answers: **do I own this movie, and in what quality?**
Phase 2 adds a second one: **what do I want, and is it on sale anywhere?** (see
[Wishlist and offers](#wishlist-and-offers-phase-2)).

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
- Loans, watch history, ratings.
- Multi-user, auth beyond "it's on my home network" (later: "it's on my tailnet").
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
| TV series | One title per series; each disc stores the season numbers it holds (a complete series is expanded from TMDB) |
| Duplicates | Never interrupt capture; flagged in the review queue. Boxes of different seasons of one series are not duplicates |
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
   - for series: the season numbers the item holds, or that it is the complete series
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
- **want** — a wishlist entry (phase 2): title id, accepted formats, optional
  price cap, notes, added_at. See below.

"Best quality owned" for a title is derived: the highest format among its items.

## Architecture

- **Go server**: JSON API under `/api`, image upload and serving, background
  identification worker (in-process, driven by item status in SQLite — no queue
  infrastructure), embedded frontend.
- **Frontend**: small SPA: capture and viewer (phone first), library and wishlist (desktop first). In development its
  dev server proxies `/api` to the Go process and is exposed on the LAN so the
  phone gets live reloads too.
- **Config**: API keys (LLM, TMDB), data directory and the watcher connection via environment variables.

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
- **Series.** A movie in a TMDB collection belongs to that series ("Indiana Jones
  Collection" → "Indiana Jones"). The title sort files a series under its name in
  release order, so *Raiders of the Lost Ark* sits with the other Indiana Jones
  movies; a card shows the series name when the title doesn't say it; search
  matches the series name. The library panel and the item detail list the whole
  series ("4 of 5 owned"), with announced movies marked as upcoming rather than
  missing. A movie that isn't owned shows "on wishlist", or a small + that adds
  it to the wishlist with the default formats and no price cap.
  - The collection is looked up in the background (`GET /movie/{id}`) for every
    matched movie, however it was saved, including titles saved before series
    existed. TMDB's collections are curated by its users, so coverage is uneven,
    and a movie is in at most one. TV series have none.
  - Not yet: a series set by hand for what TMDB doesn't group (the MCU, "Studio
    Ghibli"), overriding the collection.
- **Seasons.** A TV series is one title, and each disc stores the season numbers
  it holds. Identification reads them off the case. A box sold as the complete
  series holds every season TMDB says had aired by the day it was captured. Without
  a TMDB match it goes to review. In the review form a series has one checkbox per
  TMDB season, or a text field ("1-3, 5") for a title typed by hand or when TMDB
  is unavailable. A card shows the seasons owned ("Seasons 1–3"). The library
  panel and the item detail list the series' seasons from TMDB (`GET /tv/{id}`)
  with the discs holding each ("3 of 4 owned"). Announced seasons count as
  upcoming, and specials (season 0) are listed only when owned. Discs that don't
  say are counted under the list. Two boxes of one series in the same format are
  duplicates only when their seasons overlap or either doesn't say. A missing
  aired season shows "on wishlist", or a + that wishes for that season.

### Static export

`moviecatalog export --output=site/` writes the library as a read-only static
site, to upload to a public web host that serves only files. The page is the
same Library (search, series matching, filters, sort, the title panel and its
series list) without links into the catalogue or a wishlist.

- Public, so its data is an allowlist: identified discs only, with their titles,
  formats, series and capture date (no time). Barcodes, notes, photos, items in
  review and the wishlist stay out.
- Posters are loaded from TMDB's image CDN; the footer carries TMDB's attribution.
- Series lists are a snapshot from TMDB at export time.
- Re-running replaces the previous export and leaves other files in the
  directory (a `CNAME`, `robots.txt`) alone.

## Wishlist and offers (phase 2)

The catalogue will run on a VPS behind Tailscale, reachable from anywhere. It
is paired with **shelfwatch**, which crawls disc shops (Levykauppa Äx, CDON)
and matches their listings to TMDB. The catalogue owns everything about *me*:
what I own and what I want. The watcher is a crawler, a database of offers, and
a browsing UI for deals outside the wishlist. It keeps no state about me apart
from "not interested" ignores, and pulls what I own and want from here.

The two jobs:

1. **Browsing for deals I didn't plan on**, on the watcher's main page: the
   category crawl, with movies I own hidden automatically.
2. **Seeing when wished-for movies are on sale**, on the wishlist page here:
   the watcher searches each shop for every wish on each refresh, so they are
   found even when they never appear in the crawled categories.

### Ownership of data

| Data | Lives in |
|---|---|
| Owned discs, formats | catalogue |
| Wishlist, accepted formats, price caps | catalogue |
| Store products, prices, price history, stock | watcher |
| Listing → TMDB matching and its manual queue | watcher |
| "Not interested" ignores while browsing | watcher |

The shared key is the **TMDB movie id**. The watcher only matches movies, so
offers are only looked up for wishes whose title is a movie. A series, or one
season of it, can still be wished for, as a note to self. Formats translate between the systems as
`bd` = Blu-ray and `4k` = UHD. The watcher does not track DVDs, so a wish
accepts Blu-ray and/or UHD only.

The watcher's own watch preference is replaced by wishes here. Its browsing
price limits (Blu-ray ≤ €10, UHD ≤ €20) stay, for job 1; a wish's own price cap
applies to job 2.

**Owned movies while browsing** (the watcher applies this rule to the owned list):

| I own it on | Offers on the watcher's main page |
|---|---|
| Nothing | Shown |
| DVD only | Shown, marked "Own: DVD" (an upgrade candidate) |
| Blu-ray or UHD | Hidden |
| Anything, and it's on the wishlist | Shown, marked "On wishlist" |

Upgrading from Blu-ray to UHD is rare and deliberate, so it goes through the
wishlist, not browsing.

### Want

- A want points at a title (the same `titles` row an owned item would use),
  so it always has a TMDB id. One want per title, or for a series also one per
  season (a season box). A season wish is collected only by a disc holding
  that season; a whole-series wish by any disc of the series.
- **Formats**: Blu-ray, UHD, or both (the default). An upgrade is just a want
  for a better format than the one owned, for example UHD for a movie I own on DVD.
- **Price cap** (optional, euros): the most I'd pay. Without one, any price is shown
  but nothing is called a deal.
- **Notes**: free text.
- **Collected on**: the day a captured disc fulfilled the wish (see below).
- Wishes are added from a TMDB search on the wishlist page, or with the + on a
  missing movie in a series list (library panel, item detail). When the title is
  already owned, the wishlist page says in which format(s), so it's clear whether
  the wish is an upgrade or a mistake.
- **Buying the disc fulfils the wish.** When a captured disc is identified as a
  wished-for title, the wish is marked collected on the day the disc was
  captured, whatever its format: I know what I'm buying, so there is no prompt.
  Nothing is deleted. A collected wish is kept as a record in a "Collected"
  group at the end of the wishlist, and no longer counts as wanted: no offers,
  not sent to the watcher, not "on wishlist" in series lists.
  - A disc that goes to review leaves the wish open until it is approved, since
    the match may be wrong.
  - Only wishes made before the disc was captured are collected, so an upgrade
    wished for later (UHD of a DVD I own) survives edits to, or
    re-identification of, the old disc.
  - Wishing again for a collected title reopens it as a new wish.

### Offers

An offer is a store product matched to the wish's TMDB id, in an accepted
format, that the watcher saw recently (within 48 hours, its freshness rule) and
that is still active. Each one carries the store, listing title, format, condition (new / used),
price, regular price when discounted, availability (in stock, supplier order,
preorder, sold out, unknown) and a link to the listing.

- An offer is a **deal** when it is in stock and at or under the wish's price cap.
- A used offer that is sold out is hidden: a used listing is a single copy, so
  its price belongs to a product that no longer exists.
- The catalogue never writes to the watcher.

### Wishlist page (`#/wishlist`, desktop first, usable on the phone)

- A search box to add a wish (TMDB search, like the review panel).
- One row per wish: poster, title and year, formats owned (if any), accepted
  formats, price cap, notes, and its offers sorted by price, deals highlighted.
- Wishes with a deal come first, then wishes with any offer, then the rest; within
  each group, newest first.
- Formats, price cap and notes are edited in place; a wish can be removed.
- When the watcher can't be reached, the wishlist still works and says that
  offers are unavailable.

### Connecting to the watcher

**Watcher → catalogue.** At the start of each refresh the watcher pulls
`GET /api/sync`, read-only, which returns what I own and what I want (movies only):

```json
{
  "owned":  [{"tmdb_id": 9874, "formats": ["Blu-ray"]}],
  "wanted": [{"tmdb_id": 9874, "title": "Cobra", "original_title": "Cobra",
              "year": 1986, "formats": ["UHD"], "max_price_cents": 2000}]
}
```

- `owned` lists the formats of all discs holding the movie, from identified
  items and items in review (their format is usually right). Items still waiting
  for identification are left out.
- The watcher keeps the last response, so it is never blocked by the catalogue being down.
- The watcher's "Add to wishlist" action on an offer calls `POST /api/wants`.

**Catalogue → watcher.** A read-only endpoint on the watcher, configured here
as `WATCHER_URL` (for example `http://shelfwatch:8080`):

```
GET /api/v2/offers?tmdb_id=9874&tmdb_id=1551
```

```json
{
  "offers": {
    "9874": {
      "searched_at": "2026-11-11T06:00:00Z",
      "items": [
        {"id": 412, "store_key": "ax", "store": "Levykauppa Äx",
         "title": "Cobra (4K Ultra HD)", "format": "4k", "condition": "new",
         "price_cents": 2299, "regular_price_cents": 3599, "currency": "EUR",
         "availability": "in_stock", "url": "https://www.levykauppax.fi/…",
         "image_url": null, "last_seen_at": "2026-11-11T11:46:17Z"}
      ]
    },
    "1551": {"searched_at": null, "items": []}
  }
}
```

- Every requested id is a key. `items` holds active, single-movie `bd`/`4k`
  listings within the watcher's freshness limit, every availability, cheapest
  first (then by id). Timestamps are ISO 8601 UTC.
- `availability` is one of `in_stock`, `supplier_order` (orderable, ships in
  days or weeks), `preorder` (not released yet), `out_of_stock` (a sold-out Äx
  edition, or a CDON listing with zero stock) and `unknown`. Only `in_stock`
  can be a deal. The others are shown dimmed, labelled. A value the catalogue
  doesn't know is shown as is.
- `searched_at` is when the watcher last searched the shops for the movie, `null`
  if it has not yet. It is what tells "nothing found" apart from "not searched
  yet" (for example a wish added since the last refresh). Each offer's own age is
  `last_seen_at`; scheduling searches is the watcher's business and is not exposed.
- `id` is the watcher's store product id; the wishlist links to its
  `/products/<id>` price history page.
- Errors are a non-2xx status. The wishlist then shows without prices.

Without `WATCHER_URL`, the wishlist works without offers.

On the wishlist, a wish with no offers says either "Not searched for yet" or
"No shop has it … (searched 3 h ago)", and wishes with offers show when the shops
were last searched.

### Not yet

- Notifications (push, email) when a deal appears.
- Price history charts. The watcher has the data, and its product page can be linked.
- Gaps for studios ("22 of 33 Pixar features"). Series already show what is
  missing and can be wished for from there; an overview of every incomplete
  series is still to do.
- Capturing a wish with the phone camera, through the identification pipeline.
- Showing "on the wishlist" on library cards (series lists already show it).

## Build order

1. Go server skeleton, SQLite schema, image upload and serving.
2. Capture page — usable on the phone from here on, so scanning can start
   before identification exists.
3. Identification worker: barcode decode → LLM → TMDB.
4. Viewer list and item detail.
5. Review queue.
6. Wishlist: wants table and API, wishlist page.
7. Offers from the watcher's HTTP API.
8. `GET /api/sync` for the watcher; then, in the watcher, targeted searches and
   owned-movie filtering (see its `docs/09_multi_store_plan.md`, phase 2).

## Open questions

None at the moment.
