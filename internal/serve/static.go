package serve

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var webFS embed.FS

// staticHandler serves the phone client. The files are compiled into the
// binary, so `wombat serve` needs no Node runtime or build step.
func staticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// The embed path is fixed at build time; this cannot fail at runtime.
		panic(err)
	}
	return http.FileServerFS(sub)
}
