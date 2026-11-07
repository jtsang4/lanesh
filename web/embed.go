// Package web embeds the compiled management UI.
//
// The SvelteKit app builds into build/app. The build directory itself is kept
// in git (via .gitkeep) so the Go program compiles even when the UI has not
// been built; in that case Assets reports ok=false.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var build embed.FS

// Assets returns the compiled UI file system and whether a build is present.
func Assets() (assets fs.FS, ok bool) {
	sub, err := fs.Sub(build, "build/app")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
