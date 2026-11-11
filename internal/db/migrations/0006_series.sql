-- The TMDB collection a movie belongs to ("Indiana Jones Collection"), used to
-- group a series in the library. Filled in the background after a title is
-- saved; details_checked_at says the lookup has been done, so a movie in no
-- collection is not looked up again.
ALTER TABLE titles ADD COLUMN collection_id INTEGER;
ALTER TABLE titles ADD COLUMN collection_name TEXT;
ALTER TABLE titles ADD COLUMN details_checked_at TEXT;

CREATE INDEX titles_collection ON titles (collection_id);
