// Package web embeds the compiled frontend.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html styles.css elm.js
var webFS embed.FS

// WebFS returns the embedded frontend.
func WebFS() fs.FS {
	return webFS
}
