//go:build embed

package main

import (
	"io/fs"
	"log"

	webdist "github.com/zzemy/VibeBridge/web"
)

func embeddedWebFS() fs.FS {
	staticFS, err := fs.Sub(webdist.FS, "dist")
	if err != nil {
		log.Fatalf("load embedded web assets: %v", err)
	}
	return staticFS
}
