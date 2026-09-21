package web

import "embed"

// Files contains the compiled manager assets. The frontend build writes to dist.
//
//go:embed dist
var Files embed.FS
