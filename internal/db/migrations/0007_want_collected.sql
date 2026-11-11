-- The day a wish was fulfilled by a captured disc (YYYY-MM-DD, local time).
-- A collected wish is kept as a record instead of being deleted; it no longer
-- counts as wanted.
ALTER TABLE wants ADD COLUMN collected_on TEXT;
