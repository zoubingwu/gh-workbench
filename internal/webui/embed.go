package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

func FS() (fs.FS, error) {
	return fs.Sub(embedded, "dist")
}
