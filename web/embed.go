package web

import "embed"

// Assets contains the production frontend generated in build/.
//
//go:embed all:build
var Assets embed.FS
