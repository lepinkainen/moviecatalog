-- A wish can be for one season of a series (a season box) instead of the
-- whole title. season is NULL for a movie or a whole series. One wish per
-- title and season: the old UNIQUE (title_id) becomes an index that treats
-- NULL as a value, which needs a table rebuild in SQLite.
CREATE TABLE wants_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id        INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    season          INTEGER,
    formats         TEXT NOT NULL,
    max_price_cents INTEGER,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    collected_on    TEXT
);
INSERT INTO wants_new (id, title_id, formats, max_price_cents, notes, created_at, collected_on)
    SELECT id, title_id, formats, max_price_cents, notes, created_at, collected_on FROM wants;
DROP TABLE wants;
ALTER TABLE wants_new RENAME TO wants;
CREATE UNIQUE INDEX wants_title_season ON wants (title_id, IFNULL(season, -1));
