//go:build windows

// Package assets embeds the icons shared by the native application and tests.
package assets

import "embed"

//go:embed *.ico
var Icons embed.FS
