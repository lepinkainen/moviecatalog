// Package server exposes the catalogue over HTTP.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

// maxUploadBytes caps one capture request (front + back photo).
const maxUploadBytes = 64 << 20

// imageExts maps the accepted upload content types to file extensions.
var imageExts = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// Server holds the HTTP handlers and their dependencies.
type Server struct {
	store   *db.Store
	dataDir string
	ui      fs.FS
	log     *slog.Logger
}

// New creates a server storing photos under dataDir/images and serving the
// built frontend from ui. ui may be nil or empty when the frontend has not
// been built.
func New(store *db.Store, dataDir string, ui fs.FS, log *slog.Logger) *Server {
	return &Server{store: store, dataDir: dataDir, ui: ui, log: log}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/items", s.handleCreateItem)
	mux.HandleFunc("GET /api/items", s.handleListItems)
	mux.HandleFunc("GET /api/items/{id}", s.handleGetItem)
	mux.HandleFunc("DELETE /api/items/{id}", s.handleDeleteItem)
	mux.HandleFunc("GET /api/images/{id}", s.handleImage)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", s.uiHandler())
	return s.logRequests(mux)
}

// uiHandler serves the embedded frontend, or an explanation when it has not been built.
func (s *Server) uiHandler() http.Handler {
	if s.ui != nil {
		if _, err := fs.Stat(s.ui, "index.html"); err == nil {
			files := http.FileServerFS(s.ui)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/assets/") {
					// Vite puts a content hash in every asset file name.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
			})
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "The frontend is not built into this binary.\n"+
			"Run `task build`, or use the Vite dev server with `task web-dev`.",
			http.StatusServiceUnavailable)
	})
}

// uploadError is a problem with the request itself, reported to the client.
type uploadError struct {
	status int
	msg    string
}

func (e *uploadError) Error() string { return e.msg }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCreateItem stores one capture: a multipart form with "front" and "back" photos.
func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
			return
		}
		s.writeError(w, http.StatusBadRequest, "expected a multipart form with front and back photos")
		return
	}
	defer r.MultipartForm.RemoveAll()

	var itemDir string
	item, err := s.store.CreateItem(r.Context(), time.Now(), func(itemID int64) ([]db.NewImage, error) {
		relDir := filepath.Join("images", strconv.FormatInt(itemID, 10))
		itemDir = filepath.Join(s.dataDir, relDir)
		if err := os.MkdirAll(itemDir, 0o755); err != nil {
			return nil, fmt.Errorf("create image directory: %w", err)
		}
		var images []db.NewImage
		for _, kind := range []string{db.KindFront, db.KindBack} {
			img, err := saveUpload(r, kind, itemDir, relDir)
			if err != nil {
				return nil, err
			}
			images = append(images, img)
		}
		return images, nil
	})
	if err != nil {
		if itemDir != "" {
			os.RemoveAll(itemDir)
		}
		var ue *uploadError
		if errors.As(err, &ue) {
			s.writeError(w, ue.status, ue.msg)
			return
		}
		s.log.Error("create item", "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not save item")
		return
	}
	s.writeJSON(w, http.StatusCreated, item)
}

// saveUpload writes the photo in the given form field to dir as <kind>.<ext>.
func saveUpload(r *http.Request, kind, dir, relDir string) (db.NewImage, error) {
	f, _, err := r.FormFile(kind)
	if err != nil {
		return db.NewImage{}, &uploadError{http.StatusBadRequest, "missing " + kind + " photo"}
	}
	defer f.Close()

	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return db.NewImage{}, fmt.Errorf("read %s photo: %w", kind, err)
	}
	contentType := http.DetectContentType(head[:n])
	ext, ok := imageExts[contentType]
	if !ok {
		return db.NewImage{}, &uploadError{http.StatusUnsupportedMediaType,
			kind + " photo must be JPEG, PNG or WebP (got " + contentType + ")"}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return db.NewImage{}, fmt.Errorf("read %s photo: %w", kind, err)
	}

	name := kind + ext
	out, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return db.NewImage{}, fmt.Errorf("save %s photo: %w", kind, err)
	}
	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		return db.NewImage{}, fmt.Errorf("save %s photo: %w", kind, err)
	}
	if err := out.Close(); err != nil {
		return db.NewImage{}, fmt.Errorf("save %s photo: %w", kind, err)
	}
	return db.NewImage{Kind: kind, Path: filepath.Join(relDir, name), ContentType: contentType}, nil
}

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListItems(r.Context())
	if err != nil {
		s.log.Error("list items", "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not list items")
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	item, err := s.store.GetItem(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		s.log.Error("get item", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not load item")
		return
	}
	s.writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteItem(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		s.log.Error("delete item", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not delete item")
		return
	}
	dir := filepath.Join(s.dataDir, "images", strconv.FormatInt(id, 10))
	if err := os.RemoveAll(dir); err != nil {
		s.log.Warn("remove item images", "id", id, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	img, err := s.store.GetImage(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "image not found")
		return
	}
	if err != nil {
		s.log.Error("get image", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "could not load image")
		return
	}
	// Image ids are never reused and photos are never rewritten.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", img.ContentType)
	http.ServeFile(w, r, filepath.Join(s.dataDir, img.Path))
}

func (s *Server) pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Warn("write response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start).Round(time.Millisecond))
	})
}
