package templates

import "embed"

// Files are build inputs embedded in the Go maintenance command.
//
//go:embed local/*.tmpl auxiliary/*.tmpl
var Files embed.FS
