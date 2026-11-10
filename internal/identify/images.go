// Package identify turns the photos of a captured item into a catalogue entry:
// barcode decoding, LLM identification and TMDB matching.
package identify

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// llmMaxSide is the longest image side sent to the LLM. Cover text stays
// legible at this size and the upload is a fraction of the camera original.
const llmMaxSide = 2048

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}

// encodeForLLM returns img as a JPEG no larger than llmMaxSide on its longest side.
func encodeForLLM(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if long := max(w, h); long > llmMaxSide {
		w, h = w*llmMaxSide/long, h*llmMaxSide/long
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// toGray converts img to grayscale, scaled down to at most maxWidth wide.
func toGray(img image.Image, maxWidth int) *image.Gray {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxWidth {
		w, h = maxWidth, h*maxWidth/w
	}
	dst := image.NewGray(image.Rect(0, 0, w, h))
	if w == b.Dx() {
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	} else {
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	}
	return dst
}

// rotate90 returns src rotated a quarter turn clockwise.
func rotate90(src *image.Gray) *image.Gray {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewGray(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w]
		for x, v := range row {
			dst.Pix[x*dst.Stride+(h-1-y)] = v
		}
	}
	return dst
}
