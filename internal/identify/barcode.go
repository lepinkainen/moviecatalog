package identify

import (
	"image"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"
)

// barcodeWidths are the image widths tried in turn. Full resolution resolves
// small barcodes; the smaller sizes rescue ones that are noisy or blurred.
var barcodeWidths = []int{1 << 30, 2000, 1200}

// DecodeBarcode looks for an EAN-13 or UPC-A barcode in img, printed either
// way up or sideways. It returns the digits (UPC-A as EAN-13 with a leading
// zero), or "" when none can be read.
func DecodeBarcode(img image.Image) string {
	for _, width := range barcodeWidths {
		gray := toGray(img, width)
		if code := decodeEAN(gray); code != "" {
			return code
		}
		if code := decodeEAN(rotate90(gray)); code != "" {
			return code
		}
	}
	return ""
}

// minBarcodeRows is how many scan lines must agree on a code. A printed
// barcode is tall enough to be crossed by many lines, while cover art or
// text that happens to pass the check digit almost never repeats.
const minBarcodeRows = 3

type rowDecoder interface {
	DecodeRow(rowNumber int, row *gozxing.BitArray, hints map[gozxing.DecodeHintType]interface{}) (*gozxing.Result, error)
}

// decodeEAN scans horizontal lines across img and returns the code that the
// most lines agree on, or "" when no code reaches minBarcodeRows.
func decodeEAN(img *image.Gray) string {
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return ""
	}
	reader, ok := oned.NewEAN13Reader().(rowDecoder)
	if !ok {
		return ""
	}
	height := bmp.GetHeight()
	step := max(1, height/500)
	votes := map[string]int{}
	for y := 0; y < height; y += step {
		row, err := bmp.GetBlackRow(y, nil)
		if err != nil {
			continue
		}
		// Try the line left to right, then right to left for an upside-down barcode.
		for range 2 {
			if result, err := reader.DecodeRow(y, row, nil); err == nil {
				votes[result.GetText()]++
				break
			}
			row.Reverse()
		}
	}
	best, bestVotes := "", 0
	for code, n := range votes {
		if n > bestVotes || (n == bestVotes && code < best) {
			best, bestVotes = code, n
		}
	}
	if bestVotes < minBarcodeRows {
		return ""
	}
	return best
}
