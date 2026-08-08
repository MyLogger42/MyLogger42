// Package mylogger42 exposes the embedded SPA assets to the binary.
//
// We separate this into its own package at the module root so the embed
// directive sees web/dist relative to the module directory regardless of
// where main lives in the tree.
package mylogger42

import (
	"embed"
	"errors"
	"io/fs"
)

// webDist holds the compiled React+Vite output. The directive uses
// `all:` to include files prefixed with `_` or `.` (e.g. .vite cache
// markers). `make clean` leaves a web/dist/.gitkeep behind so the
// directive still resolves and the project compiles before the
// frontend is built.
//
//go:embed all:web/dist
var webDist embed.FS

// StaticAssets returns an fs.FS rooted at web/dist, or an error if the
// SPA hasn't been built. Callers should treat a non-nil error as
// "frontend not bundled" and continue serving only the API.
func StaticAssets() (fs.FS, error) {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		return nil, err
	}
	// Probe for index.html so we surface a helpful error early when the
	// embed succeeded but the directory is empty (e.g. first build).
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, errors.New("web/dist/index.html not found — run `make web` first")
	}
	return sub, nil
}
