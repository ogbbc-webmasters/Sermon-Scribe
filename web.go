// Package sermonscribe embeds the compiled frontend. The go:embed directive
// is package-relative, so it lives here at the repo root next to web/.
package sermonscribe

import (
	"embed"
	"io/fs"
)

//go:embed web
var webFS embed.FS

// WebFS returns the embedded frontend rooted at the web/ directory.
func WebFS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
