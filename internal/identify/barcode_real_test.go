package identify

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealPhotos prints what DecodeBarcode reads from the photos in
// BARCODE_PHOTOS (a glob). It is a manual aid, skipped by default.
func TestRealPhotos(t *testing.T) {
	pattern := os.Getenv("BARCODE_PHOTOS")
	if pattern == "" {
		t.Skip("BARCODE_PHOTOS not set")
	}
	paths, _ := filepath.Glob(pattern)
	for _, path := range paths {
		img, err := loadImage(path)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		code := DecodeBarcode(img)
		t.Logf("%s  %-14s %s", path[len(path)-12:], code, time.Since(start).Round(time.Millisecond))
	}
}
