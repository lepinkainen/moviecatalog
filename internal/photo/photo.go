// Package photo loads captured photos the way a person sees them.
//
// Phone cameras store the sensor's pixels as they are and record how the
// phone was held in an EXIF orientation tag. Go's decoders ignore that tag,
// so Load applies it; everything downstream can then assume upright pixels.
package photo

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Load decodes the image at path and applies its EXIF orientation.
func Load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	orientation := readOrientation(bufio.NewReader(f))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return orient(img, orientation), nil
}

// FitWidth returns img scaled down to at most width pixels wide; smaller images are returned as is.
func FitWidth(img image.Image, width int) image.Image {
	b := img.Bounds()
	if b.Dx() <= width {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, max(1, b.Dy()*width/b.Dx())))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// readOrientation returns the EXIF orientation (1-8) of a JPEG, or 1 when
// the data is not a JPEG or carries no orientation.
func readOrientation(r io.Reader) int {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil || soi != [2]byte{0xFF, 0xD8} {
		return 1
	}
	for {
		var head [4]byte
		if _, err := io.ReadFull(r, head[:]); err != nil || head[0] != 0xFF {
			return 1
		}
		marker, size := head[1], int(binary.BigEndian.Uint16(head[2:]))-2
		if marker == 0xDA || size < 0 { // start of scan: no metadata beyond this point
			return 1
		}
		segment := make([]byte, size)
		if _, err := io.ReadFull(r, segment); err != nil {
			return 1
		}
		if marker == 0xE1 && len(segment) > 6 && string(segment[:6]) == "Exif\x00\x00" {
			return tiffOrientation(segment[6:])
		}
	}
}

// tiffOrientation finds the orientation tag in the first directory of a TIFF block.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	offset := int(order.Uint32(tiff[4:8]))
	if offset < 0 || offset+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[offset:]))
	for i := range count {
		entry := offset + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:]) == 0x0112 {
			if v := int(order.Uint16(tiff[entry+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient returns img turned upright according to an EXIF orientation value.
func orient(img image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)

	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch orientation {
			case 2: // mirrored
				sx, sy = w-1-x, y
			case 3: // upside down
				sx, sy = w-1-x, h-1-y
			case 4: // mirrored and upside down
				sx, sy = x, h-1-y
			case 5: // mirrored, then a quarter turn
				sx, sy = y, x
			case 6: // needs a quarter turn clockwise
				sx, sy = y, h-1-x
			case 7: // mirrored, then a quarter turn the other way
				sx, sy = w-1-y, h-1-x
			case 8: // needs a quarter turn anticlockwise
				sx, sy = w-1-y, x
			}
			s, d := src.PixOffset(sx, sy), dst.PixOffset(x, y)
			copy(dst.Pix[d:d+4], src.Pix[s:s+4])
		}
	}
	return dst
}
