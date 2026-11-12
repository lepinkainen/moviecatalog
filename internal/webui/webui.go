// Package webui embeds the built frontend. The dist and export directories are
// produced by the Vite build in web/; until that has run they hold only a
// placeholder.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

//go:embed all:export
var export embed.FS

// FS returns the built frontend files, rooted at the site root.
func FS() fs.FS {
	return sub(dist, "dist")
}

// ExportFS returns the built read-only library page for `moviecatalog export`:
// export.html and its assets.
func ExportFS() fs.FS {
	return sub(export, "export")
}

func sub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err) // the directory is embedded at compile time, so this cannot fail
	}
	return s
}
