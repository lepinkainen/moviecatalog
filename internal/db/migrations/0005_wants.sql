-- The wishlist: one entry per wanted title. A want always has a TMDB match,
-- because offers from the shop watcher are keyed by TMDB id.
CREATE TABLE wants (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id        INTEGER NOT NULL UNIQUE REFERENCES titles (id) ON DELETE CASCADE,
    -- Accepted formats, comma-separated ("Blu-ray,UHD").
    formats         TEXT NOT NULL,
    -- The most I'd pay, in euro cents; NULL means no cap.
    max_price_cents INTEGER,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);
