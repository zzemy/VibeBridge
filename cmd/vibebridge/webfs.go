//go:build !embed

package main

import "io/fs"

func embeddedWebFS() fs.FS {
	return nil
}
