package backup

import (
	"archive/zip"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunArchivesDatabaseAndImages(t *testing.T) {
	data := t.TempDir()
	// Keep the store open, so the latest write is still only in the WAL.
	store, err := db.Open(filepath.Join(data, DBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateItem(t.Context(), time.Now(), func(int64) ([]db.NewImage, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(data, "images", "1", "front.jpg"), "front")
	writeFile(t, filepath.Join(data, "images", "1", "back.jpg"), "back")
	writeFile(t, filepath.Join(data, "thumbs", "1-400.jpg"), "thumb")

	out := filepath.Join(t.TempDir(), "sub", "backup.zip")
	sum, err := Run(data, out)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Images != 2 {
		t.Errorf("Images = %d, want 2", sum.Images)
	}
	if info, err := os.Stat(out); err != nil || info.Size() != sum.Bytes {
		t.Errorf("zip size: stat %v, %v; summary %d", info, err, sum.Bytes)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		files[f.Name] = f
	}
	sort.Strings(names)
	want := []string{"images/1/back.jpg", "images/1/front.jpg", "moviecatalog.db"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
	if m := files["moviecatalog.db"].Method; m != zip.Deflate {
		t.Errorf("database method = %d, want deflate", m)
	}
	if m := files["images/1/front.jpg"].Method; m != zip.Store {
		t.Errorf("image method = %d, want store", m)
	}
	if got := readEntry(t, files["images/1/front.jpg"]); got != "front" {
		t.Errorf("front.jpg = %q", got)
	}

	// The archived database is a complete catalogue on its own.
	restored := filepath.Join(t.TempDir(), DBName)
	writeFile(t, restored, readEntry(t, files["moviecatalog.db"]))
	conn, err := sql.Open("sqlite", "file:"+restored)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow("SELECT COUNT(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("restored items = %d, want 1", n)
	}

	if _, err := Run(data, out); err == nil {
		t.Error("second Run to the same file succeeded, want error")
	}
	left, _ := os.ReadDir(filepath.Dir(out))
	if len(left) != 1 {
		t.Errorf("output directory has %d entries, want only the zip", len(left))
	}
}

func TestRunWithoutDatabase(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backup.zip")
	if _, err := Run(t.TempDir(), out); err == nil {
		t.Fatal("Run without a database succeeded")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a failed Run left a zip behind")
	}
}

func readEntry(t *testing.T, f *zip.File) string {
	t.Helper()
	r, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 15, 3, 0, 0, 0, time.Local)
	for i := range 4 {
		writeFile(t, filepath.Join(dir, FileName(base.AddDate(0, 0, i))), "zip")
	}
	writeFile(t, filepath.Join(dir, "notes.zip"), "mine")

	deleted, err := Prune(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || deleted[0] != FileName(base) || deleted[1] != FileName(base.AddDate(0, 0, 1)) {
		t.Errorf("deleted = %v, want the two oldest", deleted)
	}
	left, _ := os.ReadDir(dir)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	want := []string{FileName(base.AddDate(0, 0, 2)), FileName(base.AddDate(0, 0, 3)), "notes.zip"}
	sort.Strings(names)
	sort.Strings(want)
	if len(names) != 3 || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Errorf("left = %v, want %v", names, want)
	}
}
