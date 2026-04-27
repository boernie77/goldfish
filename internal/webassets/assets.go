package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var webEmbed embed.FS

// FS returns the web/ subdirectory as an fs.FS ready for http.FileServer.
func FS() fs.FS {
	sub, err := fs.Sub(webEmbed, "web")
	if err != nil {
		panic(err) // compile-time structure, can never fail at runtime
	}
	return sub
}
