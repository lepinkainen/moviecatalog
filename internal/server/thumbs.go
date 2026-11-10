package server

import (
	"fmt"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"

	"github.com/lepinkainen/moviecatalog/internal/db"
	"github.com/lepinkainen/moviecatalog/internal/photo"
)

// thumbWidths are the downscaled sizes the frontend may ask for with ?w=.
var thumbWidths = map[string]int{"160": 160, "480": 480, "1200": 1200}

// thumbSlots limits concurrent resizing: a decoded camera photo is tens of megabytes.
var thumbSlots = make(chan struct{}, 2)

func (s *Server) thumbPath(imageID int64, width int) string {
	return filepath.Join(s.dataDir, "thumbs", fmt.Sprintf("%d-%d.jpg", imageID, width))
}

// serveThumb serves img scaled down to width, creating and caching the file on first use.
func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request, img *db.Image, width int) {
	path := s.thumbPath(img.ID, width)
	if _, err := os.Stat(path); err != nil {
		thumbSlots <- struct{}{}
		// Another request may have made it while this one waited.
		if _, statErr := os.Stat(path); statErr != nil {
			err = makeThumb(filepath.Join(s.dataDir, img.Path), path, width)
		} else {
			err = nil
		}
		<-thumbSlots
		if err != nil {
			s.log.Error("make thumbnail", "image", img.ID, "error", err)
			s.writeError(w, http.StatusInternalServerError, "could not resize image")
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, path)
}

func makeThumb(srcPath, dstPath string, width int) error {
	src, err := photo.Load(srcPath)
	if err != nil {
		return err
	}
	src = photo.FitWidth(src, width)

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	// Write to a temporary name so a half-written file is never served.
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), "tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := jpeg.Encode(tmp, src, &jpeg.Options{Quality: 82}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dstPath)
}

// removeThumbs deletes the cached thumbnails of the given images.
func (s *Server) removeThumbs(images []db.Image) {
	for _, img := range images {
		for _, width := range thumbWidths {
			os.Remove(s.thumbPath(img.ID, width))
		}
	}
}
