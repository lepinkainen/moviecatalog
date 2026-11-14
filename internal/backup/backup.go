// Package backup writes the catalogue's database and original photos into one
// zip file. Restoring is unzipping it into an empty data directory.
package backup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/lepinkainen/moviecatalog/internal/db"
)

// DBName and ImagesDir are the database file and photo directory inside both
// the data directory and the archive.
const (
	DBName    = "moviecatalog.db"
	ImagesDir = "images"
)

// Summary describes a finished backup.
type Summary struct {
	Images int   // photo files
	Bytes  int64 // size of the zip file
}

// Run writes dataDir's database and images/ into a new zip file at out, which
// must not exist. Thumbnails are left out, since the server regenerates them.
// The zip only appears at out once it is complete.
//
// The database is deflated. Photos are stored as they are: JPEGs do not
// compress further, and deflating gigabytes of them only costs time.
func Run(dataDir, out string) (Summary, error) {
	if _, err := os.Stat(out); err == nil {
		return Summary{}, fmt.Errorf("%s already exists", out)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Summary{}, err
	}
	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Summary{}, err
	}

	// VACUUM INTO needs a file name that does not exist yet.
	work, err := os.MkdirTemp(dir, ".backup-*")
	if err != nil {
		return Summary{}, err
	}
	defer os.RemoveAll(work)
	snap := filepath.Join(work, DBName)
	if err := db.Snapshot(filepath.Join(dataDir, DBName), snap); err != nil {
		return Summary{}, err
	}

	tmp := filepath.Join(work, "backup.zip")
	f, err := os.Create(tmp)
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	if err := addFile(zw, snap, DBName, zip.Deflate); err != nil {
		return Summary{}, err
	}

	var sum Summary
	root := filepath.Join(dataDir, ImagesDir)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		sum.Images++
		return addFile(zw, path, filepath.ToSlash(rel), zip.Store)
	})
	// A catalogue with no captures yet has no images directory.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Summary{}, fmt.Errorf("add images: %w", err)
	}

	if err := zw.Close(); err != nil {
		return Summary{}, err
	}
	if err := f.Sync(); err != nil {
		return Summary{}, err
	}
	if sum.Bytes, err = f.Seek(0, io.SeekEnd); err != nil {
		return Summary{}, err
	}
	if err := f.Close(); err != nil {
		return Summary{}, err
	}
	if err := os.Rename(tmp, out); err != nil {
		return Summary{}, err
	}
	return sum, nil
}

func addFile(zw *zip.Writer, path, name string, method uint16) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name, hdr.Method = name, method
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		return fmt.Errorf("add %s: %w", name, err)
	}
	return nil
}

// FileName is the name Run's archives get in a backup directory. It sorts by
// time, and Prune only touches names of this shape.
func FileName(t time.Time) string {
	return "moviecatalog-" + t.Format("20060102-150405") + ".zip"
}

var namePattern = regexp.MustCompile(`^moviecatalog-\d{8}-\d{6}\.zip$`)

// Prune deletes all but the newest keep backups in dir and returns the
// deleted names. Other files in dir are left alone.
func Prune(dir string, keep int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && namePattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return nil, nil
	}
	sort.Strings(names)
	old := names[:len(names)-keep]
	for _, name := range old {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return nil, err
		}
	}
	return old, nil
}
