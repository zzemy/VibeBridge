//go:build !windows

package workspace

import "path/filepath"

func canonicalizePath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
