package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

var (
	jpegBytes = encodeTestImage(func(w io.Writer, img image.Image) error { return jpeg.Encode(w, img, nil) })
	pngBytes  = encodeTestImage(png.Encode)
)

// encodeTestImage renders a 400x600 test photo in the given format.
func encodeTestImage(encode func(io.Writer, image.Image) error) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

var testUI = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>test ui</title>")},
	"assets/app.js": {Data: []byte("console.log('ui')")},
}

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	return newTestServerWithUI(t, testUI)
}

func newTestServerWithUI(t *testing.T, ui fs.FS) (*httptest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := db.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, dataDir, ui, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, dataDir
}

func postCapture(t *testing.T, url string, files map[string][]byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for field, data := range files {
		fw, err := mw.CreateFormFile(field, field+".bin")
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
	}
	mw.Close()
	resp, err := http.Post(url+"/api/items", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return v
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestCaptureRoundTrip(t *testing.T) {
	ts, dataDir := newTestServer(t)

	resp := postCapture(t, ts.URL, map[string][]byte{"front": jpegBytes, "back": pngBytes})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	item := decode[db.Item](t, resp)
	if item.Status != db.StatusCaptured {
		t.Errorf("status = %q, want %q", item.Status, db.StatusCaptured)
	}
	if len(item.Images) != 2 || item.Images[0].Kind != db.KindFront || item.Images[1].Kind != db.KindBack {
		t.Fatalf("images = %+v, want front then back", item.Images)
	}
	if item.Images[1].ContentType != "image/png" {
		t.Errorf("back content type = %q, want image/png", item.Images[1].ContentType)
	}

	// The stored photo is served back byte for byte.
	imgResp := get(t, ts.URL+item.Images[0].URL)
	got, _ := io.ReadAll(imgResp.Body)
	if imgResp.StatusCode != http.StatusOK || !bytes.Equal(got, jpegBytes) {
		t.Errorf("front image: status %d, %d bytes; want 200 and the uploaded bytes", imgResp.StatusCode, len(got))
	}

	items := decode[[]db.Item](t, get(t, ts.URL+"/api/items"))
	if len(items) != 1 || items[0].ID != item.ID || len(items[0].Images) != 2 {
		t.Errorf("list = %+v, want the one created item with two images", items)
	}

	// Deleting removes the row and the files.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/items/1", nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", delResp.StatusCode)
	}
	if resp := get(t, ts.URL+"/api/items/1"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "images", "1")); !os.IsNotExist(err) {
		t.Errorf("image directory still exists after delete (err = %v)", err)
	}
}

func TestCaptureRejectsBadUploads(t *testing.T) {
	ts, dataDir := newTestServer(t)

	cases := []struct {
		name  string
		files map[string][]byte
		want  int
	}{
		{"missing back", map[string][]byte{"front": jpegBytes}, http.StatusBadRequest},
		{"not an image", map[string][]byte{"front": jpegBytes, "back": []byte("hello")}, http.StatusUnsupportedMediaType},
		{"truncated image", map[string][]byte{"front": jpegBytes[:len(jpegBytes)/2], "back": pngBytes},
			http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if resp := postCapture(t, ts.URL, tc.files); resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}

	// Rejected captures leave nothing behind.
	items := decode[[]db.Item](t, get(t, ts.URL+"/api/items"))
	if len(items) != 0 {
		t.Errorf("list = %+v, want empty", items)
	}
	entries, _ := os.ReadDir(filepath.Join(dataDir, "images"))
	if len(entries) != 0 {
		t.Errorf("images directory has %d leftover entries", len(entries))
	}
}

func TestFrontend(t *testing.T) {
	ts, _ := newTestServer(t)

	resp := get(t, ts.URL+"/")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "test ui") {
		t.Errorf("GET / = %d %q, want the index page", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", cc)
	}
	if cc := get(t, ts.URL+"/assets/app.js").Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", cc)
	}

	// Unknown API paths are JSON 404s, not frontend files.
	resp = get(t, ts.URL+"/api/nope")
	if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("GET /api/nope = %d %s, want JSON 404", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestFrontendNotBuilt(t *testing.T) {
	// A dist directory holding only the placeholder, as in a fresh checkout.
	ts, _ := newTestServerWithUI(t, fstest.MapFS{".gitkeep": {}})

	if resp := get(t, ts.URL+"/"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET / = %d, want 503", resp.StatusCode)
	}
	if resp := get(t, ts.URL+"/api/health"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/health = %d, want 200", resp.StatusCode)
	}
}

func TestThumbnails(t *testing.T) {
	ts, dataDir := newTestServer(t)
	item := decode[db.Item](t, postCapture(t, ts.URL, map[string][]byte{"front": jpegBytes, "back": pngBytes}))

	// Both source formats come back as a JPEG scaled to the requested width.
	for _, img := range item.Images {
		resp := get(t, ts.URL+img.URL+"?w=160")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s thumbnail status = %d", img.Kind, resp.StatusCode)
		}
		thumb, format, err := image.Decode(resp.Body)
		if err != nil {
			t.Fatalf("%s thumbnail: %v", img.Kind, err)
		}
		if b := thumb.Bounds(); format != "jpeg" || b.Dx() != 160 || b.Dy() != 240 {
			t.Errorf("%s thumbnail = %s %dx%d, want jpeg 160x240", img.Kind, format, b.Dx(), b.Dy())
		}
	}
	// A photo narrower than the requested width is not enlarged.
	big, _, err := image.Decode(get(t, ts.URL+item.Images[0].URL+"?w=1200").Body)
	if err != nil || big.Bounds().Dx() != 400 {
		t.Errorf("w=1200 of a 400px photo: width %v, err %v; want 400", big, err)
	}
	if resp := get(t, ts.URL+item.Images[0].URL+"?w=999"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unsupported width status = %d, want 400", resp.StatusCode)
	}

	// Deleting the item removes its cached thumbnails too.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/items/1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if left, _ := os.ReadDir(filepath.Join(dataDir, "thumbs")); len(left) != 0 {
		t.Errorf("%d thumbnails left after delete", len(left))
	}
}

func TestRunsEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)
	postCapture(t, ts.URL, map[string][]byte{"front": jpegBytes, "back": pngBytes})

	resp := get(t, ts.URL+"/api/items/1/runs")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("runs of a fresh item = %d %q, want 200 []", resp.StatusCode, body)
	}
}

func patchItem(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestManualCorrection(t *testing.T) {
	ts, _ := newTestServer(t)
	postCapture(t, ts.URL, map[string][]byte{"front": jpegBytes, "back": pngBytes})
	itemURL := ts.URL + "/api/items/1"

	// A box set: one TMDB title and one typed by hand.
	resp := patchItem(t, itemURL, `{"format":"Blu-ray","notes":"box set","titles":[
		{"tmdb_id":603,"media_type":"movie","title":"The Matrix","original_title":"The Matrix","year":1999,"poster_path":"/m.jpg"},
		{"tmdb_id":null,"media_type":"movie","title":"Home Video","original_title":null,"year":null,"poster_path":null}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	item := decode[db.Item](t, resp)
	if item.Status != db.StatusIdentified || *item.Format != "Blu-ray" || item.Notes != "box set" {
		t.Errorf("item = %q %q %q; want identified Blu-ray \"box set\"", item.Status, *item.Format, item.Notes)
	}
	if len(item.Titles) != 2 || item.Titles[0].Title != "The Matrix" || *item.Titles[0].TMDBID != 603 ||
		item.Titles[1].Title != "Home Video" || item.Titles[1].TMDBID != nil {
		t.Errorf("titles = %+v", item.Titles)
	}

	// Fields that are left out stay as they are.
	item = decode[db.Item](t, patchItem(t, itemURL, `{"format":"UHD"}`))
	if *item.Format != "UHD" || item.Notes != "box set" || len(item.Titles) != 2 {
		t.Errorf("after format-only patch: %q %q %d titles; want UHD, notes and titles kept", *item.Format, item.Notes, len(item.Titles))
	}

	// A series with its seasons, sorted and without repeats; replacing the titles replaces them.
	item = decode[db.Item](t, patchItem(t, itemURL, `{"titles":[
		{"tmdb_id":1432,"media_type":"tv","title":"Veronica Mars","seasons":[3,1,3]}]}`))
	if len(item.Titles) != 1 || fmt.Sprint(item.Titles[0].Seasons) != "[1 3]" {
		t.Errorf("series titles = %+v, want seasons [1 3]", item.Titles)
	}
	item = decode[db.Item](t, patchItem(t, itemURL, `{"titles":[
		{"tmdb_id":1432,"media_type":"tv","title":"Veronica Mars","seasons":[2]}]}`))
	if fmt.Sprint(item.Titles[0].Seasons) != "[2]" {
		t.Errorf("after replacing: seasons = %v, want [2]", item.Titles[0].Seasons)
	}

	for name, body := range map[string]string{
		"movie seasons":  `{"titles":[{"tmdb_id":603,"media_type":"movie","title":"The Matrix","seasons":[1]}]}`,
		"bad season":     `{"titles":[{"tmdb_id":1432,"media_type":"tv","title":"Veronica Mars","seasons":[1999]}]}`,
		"unknown format": `{"format":"VHS"}`,
		"empty title":    `{"titles":[{"tmdb_id":null,"media_type":"movie","title":" "}]}`,
		"bad media type": `{"titles":[{"tmdb_id":1,"media_type":"book","title":"X"}]}`,
		"unknown field":  `{"status":"captured"}`,
	} {
		if resp := patchItem(t, itemURL, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
	if resp := patchItem(t, ts.URL+"/api/items/99", `{}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("patch of a missing item = %d, want 404", resp.StatusCode)
	}
}

func TestTMDBSearchWithoutKey(t *testing.T) {
	ts, _ := newTestServer(t)
	if resp := get(t, ts.URL+"/api/tmdb/search?q=alien"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when no TMDB key is configured", resp.StatusCode)
	}
}
