package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

var (
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("front-photo-data")...)
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), []byte("back-photo-data")...)
)

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := db.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, dataDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
