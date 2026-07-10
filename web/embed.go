//go:build embed

package web

import "embed"

// FS contains the Vite production build when compiled with -tags embed.
//
//go:embed dist
var FS embed.FS
