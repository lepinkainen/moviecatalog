-- One physical thing on the shelf.
CREATE TABLE items (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    captured_at TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'captured',
    format      TEXT,
    barcode     TEXT,
    notes       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX items_status ON items (status);
CREATE INDEX items_barcode ON items (barcode);

CREATE TABLE images (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id      INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    path         TEXT NOT NULL,
    content_type TEXT NOT NULL,
    UNIQUE (item_id, kind)
);

-- A movie or series, shared between the items that contain it.
CREATE TABLE titles (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    tmdb_id        INTEGER,
    media_type     TEXT NOT NULL DEFAULT 'movie',
    title          TEXT NOT NULL,
    original_title TEXT,
    year           INTEGER,
    poster_path    TEXT,
    UNIQUE (media_type, tmdb_id)
);

CREATE TABLE item_titles (
    item_id  INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    title_id INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, title_id)
);

CREATE INDEX item_titles_title ON item_titles (title_id);

-- Raw input and output of every identification step, so runs can be audited and repeated.
CREATE TABLE identification_runs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id    INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    step       TEXT NOT NULL,
    model      TEXT,
    request    TEXT,
    response   TEXT,
    error      TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX identification_runs_item ON identification_runs (item_id);
