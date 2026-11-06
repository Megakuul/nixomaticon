package web

import "embed"

//go:embed all:dist
var Asset embed.FS
