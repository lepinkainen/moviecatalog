# moviecatalog

A personal catalogue of physical discs (DVD, Blu-ray, UHD). Photograph each
case's front and back with a phone. A background worker then reads the barcode,
identifies the release with an OpenAI vision model and matches it against TMDB.
Anything uncertain goes to a review queue.

It also keeps a wishlist with shop offers from
[cdon-watcher](https://github.com/lepinkainen/cdon-watcher), and can export the
library as a read-only static site.

It is a single Go binary with an embedded Svelte frontend, storing data in
SQLite plus a directory of photos. See [SPEC.md](SPEC.md) for the full spec and
the decisions behind it.

## Requirements

- Go 1.27+, Node.js (for the frontend build)
- [Task](https://taskfile.dev) and [mise](https://mise.jdx.dev) (for `air` during development)

## Usage

```sh
task build            # frontend, lint, tests, then build/moviecatalog
./build/moviecatalog  # serve on :8080
```

Open `http://<host>:8080` on a phone to capture discs. There is no
authentication, so run it only on a trusted network.

| Variable | Purpose |
|---|---|
| `OPENAI_API_KEY`, `TMDB_API_KEY` | Identification runs only when both are set; captures are stored either way |
| `OPENAI_MODEL` | Vision model (has a built-in default) |
| `MOVIECATALOG_ADDR` | Listen address, default `:8080` |
| `MOVIECATALOG_DATA_DIR` | Database and photos, default `data` |
| `WATCHER_URL` | cdon-watcher base URL, for wishlist offers (optional) |

### Static export

```sh
task export -- --output=site/ --title="My discs"
```

This writes identified discs only, with no barcodes, notes, photos or wishlist.

## Development

```sh
task dev       # Go server with rebuild on change (air)
task web-dev   # Vite dev server on the LAN, proxies /api to :8080
task test
task lint
```

The `*-op` task variants read the API keys from 1Password through a local,
gitignored `op.env` file.
