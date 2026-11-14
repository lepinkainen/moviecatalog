package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
)

// Snapshot writes a consistent copy of the database at src into dst, a file
// that must not exist yet. The copy is a single self-contained file: changes
// still in the write-ahead log are folded in. The source is opened read-only
// and is not migrated.
func Snapshot(src, dst string) error {
	// Opening a missing file would create an empty database.
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	conn, err := sql.Open("sqlite", "file:"+src+"?"+url.Values{"mode": {"ro"}}.Encode())
	if err != nil {
		return fmt.Errorf("snapshot: open %s: %w", src, err)
	}
	defer conn.Close()
	if _, err := conn.Exec("VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("snapshot %s: %w", src, err)
	}
	return nil
}
