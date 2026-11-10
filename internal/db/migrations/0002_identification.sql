-- Outcome of the identification pipeline.
ALTER TABLE items ADD COLUMN confidence TEXT;
ALTER TABLE items ADD COLUMN review_reason TEXT;
ALTER TABLE items ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;

-- Order of the titles within a box set, as listed by the identification.
ALTER TABLE item_titles ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
