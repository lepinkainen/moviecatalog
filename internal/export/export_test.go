package export

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/identify"
)

var testAssets = fstest.MapFS{
	"export.html":         {Data: []byte("<!doctype html><title>page</title>")},
	"assets/export-1.js":  {Data: []byte("js")},
	"assets/export-1.css": {Data: []byte("css")},
	".gitkeep":            {},
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeTMDB map[int64]*identify.Collection

func (f fakeTMDB) Collection(_ context.Context, id int64) (*identify.Collection, error) {
	if c, ok := f[id]; ok {
		return c, nil
	}
	return nil, identify.ErrNotFound
}

func openStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func newItem(t *testing.T, store *db.Store) *db.Item {
	t.Helper()
	item, err := store.CreateItem(context.Background(), time.Now(), func(int64) ([]db.NewImage, error) {
		return []db.NewImage{{Kind: db.KindFront, Path: "images/front.jpg", ContentType: "image/jpeg"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func ptr[T any](v T) *T { return &v }

// seed stores Raiders on UHD (identified by the worker, with a barcode and
// notes) and on DVD (reviewed by hand), Temple of Doom still in review, one
// disc still waiting, and a hand-typed title whose name tries to break out of
// the page.
func seed(t *testing.T, store *db.Store) {
	t.Helper()
	ctx := context.Background()
	raiders := db.TitleInput{TMDBID: ptr(int64(85)), MediaType: db.MediaMovie, Title: "Raiders of the Lost Ark",
		Year: ptr(1981), PosterPath: ptr("/r.jpg")}

	uhd := newItem(t, store)
	if err := store.SaveIdentification(ctx, uhd.ID, uhd.Revision, db.Identification{
		Format: "UHD", Barcode: "5051892000001", Confidence: "high", Notes: "secret shelf note",
		Titles: []db.TitleInput{raiders},
	}); err != nil {
		t.Fatal(err)
	}

	dvd := newItem(t, store)
	if err := store.ApplyReview(ctx, dvd.ID, db.ItemEdit{Format: ptr("DVD"), Notes: ptr("another note"),
		Titles: &[]db.TitleInput{raiders}}); err != nil {
		t.Fatal(err)
	}

	review := newItem(t, store)
	if err := store.SaveIdentification(ctx, review.ID, review.Revision, db.Identification{
		Format: "Blu-ray", Confidence: "low", ReviewReasons: []string{"low confidence"},
		Titles: []db.TitleInput{{TMDBID: ptr(int64(87)), MediaType: db.MediaMovie, Title: "Indiana Jones and the Temple of Doom"}},
	}); err != nil {
		t.Fatal(err)
	}

	newItem(t, store) // waiting for identification

	odd := newItem(t, store)
	if err := store.ApplyReview(ctx, odd.ID, db.ItemEdit{Format: ptr("Blu-ray"),
		Titles: &[]db.TitleInput{{MediaType: db.MediaMovie, Title: "</script><script>alert(1)</script>"}}}); err != nil {
		t.Fatal(err)
	}

	// The series finder has filed every Raiders title under Indiana Jones.
	all, err := store.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range all {
		for _, ti := range it.Titles {
			if ti.TMDBID != nil && *ti.TMDBID == 85 {
				if err := store.SaveSeries(ctx, ti.ID, &db.Series{ID: 84, Name: "Indiana Jones"}, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

var indy = fakeTMDB{84: {ID: 84, Name: "Indiana Jones Collection", Parts: []identify.CollectionPart{
	{ID: 87, Title: "Indiana Jones and the Temple of Doom", ReleaseDate: "1984-05-23", Year: ptr(1984)},
	{ID: 85, Title: "Raiders of the Lost Ark", ReleaseDate: "1981-06-12", Year: ptr(1981)},
}}}

func readData(t *testing.T, dir string) (Data, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, dataFile))
	if err != nil {
		t.Fatal(err)
	}
	var data Data
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	return data, string(raw)
}

func TestRun(t *testing.T) {
	store := openStore(t)
	seed(t, store)
	dir := filepath.Join(t.TempDir(), "site")
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	sum, err := Run(context.Background(), store, testAssets,
		Options{Dir: dir, Title: "My discs", TMDB: indy, Now: now}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if sum != (Summary{Items: 3, Titles: 2, Series: 1}) {
		t.Errorf("summary = %+v", sum)
	}

	for _, name := range []string{"index.html", "data.json", "assets/export-1.js", "assets/export-1.css"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"export.html", ".gitkeep"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s should not be written", name)
		}
	}

	data, raw := readData(t, dir)
	if data.Title != "My discs" || data.GeneratedAt != "2026-11-12T12:00:00Z" {
		t.Errorf("title, generated_at = %q, %q", data.Title, data.GeneratedAt)
	}
	// Only identified discs: both Raiders and the hand-typed one.
	if len(data.Items) != 3 {
		t.Fatalf("items = %+v, want 3", data.Items)
	}
	for _, it := range data.Items {
		if it.Status != db.StatusIdentified {
			t.Errorf("item %d status = %q", it.ID, it.Status)
		}
		if len(it.CapturedAt) != len("2006-01-02") {
			t.Errorf("captured_at = %q, want a date only", it.CapturedAt)
		}
	}
	for _, leak := range []string{"5051892000001", "secret shelf note", "another note", "images/",
		"barcode", "notes", "confidence", "review_reason", "\"images\"", "\"wanted\":true"} {
		if strings.Contains(raw, leak) {
			t.Errorf("data.json contains %q:\n%s", leak, raw)
		}
	}
	// Go's encoder escapes <, > and &, so nothing in the data reads as markup.
	if strings.Contains(raw, "<") {
		t.Errorf("data.json has an unescaped <:\n%s", raw)
	}

	s, ok := data.Series[84]
	if !ok {
		t.Fatalf("series = %+v, want 84", data.Series)
	}
	if s.Name != "Indiana Jones" || len(s.Parts) != 2 || s.Parts[0].TMDBID != 85 {
		t.Fatalf("series 84 = %+v", s)
	}
	if len(s.Parts[0].Copies) != 2 || s.Parts[0].TitleID == nil {
		t.Errorf("Raiders = %+v, want two copies and a title", s.Parts[0])
	}
	// Temple of Doom is in review, so the site says it is not owned.
	if temple := s.Parts[1]; len(temple.Copies) != 0 || temple.TitleID != nil {
		t.Errorf("Temple of Doom = %+v, want no copies", temple)
	}
}

func TestRunWithoutTMDB(t *testing.T) {
	store := openStore(t)
	seed(t, store)
	dir := t.TempDir()

	sum, err := Run(context.Background(), store, testAssets, Options{Dir: dir, Now: time.Now()}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Series != 0 {
		t.Errorf("series = %d, want 0", sum.Series)
	}
	data, _ := readData(t, dir)
	// Series names still come along for grouping and search.
	named := false
	for _, it := range data.Items {
		for _, ti := range it.Titles {
			named = named || (ti.Series != nil && ti.Series.Name == "Indiana Jones")
		}
	}
	if !named {
		t.Errorf("no title carries the series name: %+v", data.Items)
	}
}

func TestRunReplacesEarlierExport(t *testing.T) {
	store := openStore(t)
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html")
	write("data.json")
	write("assets/export-old.js")
	write("CNAME")
	write("robots.txt")

	if _, err := Run(context.Background(), store, testAssets, Options{Dir: dir, Now: time.Now()}, quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "assets/export-old.js")); err == nil {
		t.Error("old asset was kept")
	}
	for _, name := range []string{"CNAME", "robots.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

func TestRunRefusesForeignSite(t *testing.T) {
	store := openStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("someone else's"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), store, testAssets, Options{Dir: dir, Now: time.Now()}, quiet)
	if err == nil {
		t.Fatal("export overwrote a page it did not write")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(got) != "someone else's" {
		t.Errorf("index.html = %q", got)
	}
}

func TestRunNeedsBuiltPage(t *testing.T) {
	store := openStore(t)
	_, err := Run(context.Background(), store, fstest.MapFS{".gitkeep": {}}, Options{Dir: t.TempDir(), Now: time.Now()}, quiet)
	if err == nil || !strings.Contains(err.Error(), "not built") {
		t.Errorf("err = %v, want a not-built error", err)
	}
}
