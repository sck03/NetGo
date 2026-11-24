package linux

import "embed"

//go:embed *.service
var Units embed.FS
