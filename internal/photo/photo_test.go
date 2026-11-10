package photo

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// exifSegment is a JPEG APP1 segment holding only an orientation tag.
func exifSegment(orientation byte) []byte {
	tiff := []byte{
		'M', 'M', 0, 42, 0, 0, 0, 8, // big-endian TIFF header, first directory at offset 8
		0, 1, // one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, orientation, 0, 0, // orientation, SHORT, count 1
		0, 0, 0, 0, // no further directories
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	size := len(payload) + 2
	return append([]byte{0xFF, 0xE1, byte(size >> 8), byte(size)}, payload...)
}

// writePhoto saves a 60x40 JPEG whose top-left quarter is white on black,
// tagged with the given EXIF orientation (0 for no EXIF at all).
func writePhoto(t *testing.T, orientation byte) string {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 60, 40))
	for y := range 20 {
		for x := range 30 {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	if orientation != 0 {
		data = append(append(append([]byte{}, data[:2]...), exifSegment(orientation)...), data[2:]...)
	}
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesOrientation(t *testing.T) {
	// Where the white quarter ends up once the photo is upright.
	cases := []struct {
		orientation byte
		w, h        int
		corner      string
	}{
		{0, 60, 40, "top-left"},
		{1, 60, 40, "top-left"},
		{2, 60, 40, "top-right"},
		{3, 60, 40, "bottom-right"},
		{4, 60, 40, "bottom-left"},
		{5, 40, 60, "top-left"},
		{6, 40, 60, "top-right"},
		{7, 40, 60, "bottom-right"},
		{8, 40, 60, "bottom-left"},
	}
	for _, tc := range cases {
		img, err := Load(writePhoto(t, tc.orientation))
		if err != nil {
			t.Fatalf("orientation %d: %v", tc.orientation, err)
		}
		b := img.Bounds()
		if b.Dx() != tc.w || b.Dy() != tc.h {
			t.Errorf("orientation %d: size %dx%d, want %dx%d", tc.orientation, b.Dx(), b.Dy(), tc.w, tc.h)
			continue
		}
		// Sample the middle of each quarter and find the white one.
		got := ""
		for name, p := range map[string]image.Point{
			"top-left": {b.Dx() / 4, b.Dy() / 4}, "top-right": {3 * b.Dx() / 4, b.Dy() / 4},
			"bottom-left": {b.Dx() / 4, 3 * b.Dy() / 4}, "bottom-right": {3 * b.Dx() / 4, 3 * b.Dy() / 4},
		} {
			if r, _, _, _ := img.At(b.Min.X+p.X, b.Min.Y+p.Y).RGBA(); r > 0x8000 {
				got += name
			}
		}
		if got != tc.corner {
			t.Errorf("orientation %d: white quarter at %q, want %q", tc.orientation, got, tc.corner)
		}
	}
}

func TestFitWidth(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 400, 600))
	if b := FitWidth(img, 160).Bounds(); b.Dx() != 160 || b.Dy() != 240 {
		t.Errorf("scaled to %dx%d, want 160x240", b.Dx(), b.Dy())
	}
	if FitWidth(img, 1200) != image.Image(img) {
		t.Error("an image narrower than the limit should be returned unchanged")
	}
}
