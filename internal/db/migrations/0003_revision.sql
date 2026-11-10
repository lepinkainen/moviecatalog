-- Bumped by every manual change, so identification results computed from an
-- older state of the item can be recognised and discarded.
ALTER TABLE items ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;

-- Result of the expensive photo-reading steps of an unfinished identification,
-- so a retry after a later step fails does not repeat them.
ALTER TABLE items ADD COLUMN checkpoint TEXT;
