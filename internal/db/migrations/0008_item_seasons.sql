-- The seasons of a TV series that one item holds. No rows means the item
-- does not say. Removed with the item's link to the title.
CREATE TABLE item_title_seasons (
    item_id  INTEGER NOT NULL,
    title_id INTEGER NOT NULL,
    season   INTEGER NOT NULL,
    PRIMARY KEY (item_id, title_id, season),
    FOREIGN KEY (item_id, title_id) REFERENCES item_titles (item_id, title_id) ON DELETE CASCADE
);
