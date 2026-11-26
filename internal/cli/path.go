package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jtsang4/lanesh/internal/daemon"
)

type pathExposeKind int

const (
	pathNoop pathExposeKind = iota
	pathSkipped
	pathLinked
	pathProfileAdded
	pathProfileExists
	pathWindowsAdded
	pathWindowsExists
	pathManualHint
)

type pathExposeResult struct {
	Kind      pathExposeKind
	LinkPath  string
	Target    string
	Profile   string
	ReloadCmd string
	Shadowed  string
	BinDir    string
}

// exposeCLI ensures the managed binary at p.Binary() is reachable on PATH and
// reports any link, profile update, or follow-up action to w.
func exposeCLI(w io.Writer, p daemon.Paths) {
	res := exposeManagedBinary(p)
	writeExposeResult(w, res)
}

func writeExposeResult(w io.Writer, res pathExposeResult) {
	switch res.Kind {
	case pathNoop, pathSkipped:
		return
	case pathLinked:
		fmt.Fprintf(w, "Linked %s -> %s.\n", res.LinkPath, res.Target)
		if res.Shadowed != "" {
			fmt.Fprintf(w, "Note: %s appears earlier in PATH and may shadow %s.\n", res.Shadowed, res.LinkPath)
		}
	case pathProfileAdded:
		fmt.Fprintf(w, "Added %s to %s.\n", res.BinDir, res.Profile)
		fmt.Fprintf(w, "Open a new terminal or run `%s` to use `lanesh`.\n", res.ReloadCmd)
	case pathProfileExists:
		fmt.Fprintf(w, "Open a new terminal or run `%s` to use `lanesh`.\n", res.ReloadCmd)
	case pathWindowsAdded:
		fmt.Fprintf(w, "Added %s to your user PATH (open a new terminal to use `lanesh`).\n", res.BinDir)
	case pathWindowsExists:
		fmt.Fprintln(w, "Open a new terminal to use `lanesh`.")
	case pathManualHint:
		if runtime.GOOS == "windows" {
			fmt.Fprintf(w, "Add %s to your PATH to use `lanesh`.\n", res.BinDir)
		} else {
			fmt.Fprintf(w, "Add %s to your PATH:  export PATH=\"%s:$PATH\"\n", res.BinDir, res.BinDir)
		}
	}
}

func isDefaultHome(p daemon.Paths, userHome string) bool {
	if userHome == "" {
		return false
	}
	def := filepath.Clean(filepath.Join(userHome, ".lanesh"))
	cur := filepath.Clean(p.Home)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(cur, def)
	}
	return cur == def
}

func modifyPathDisabled(val string) bool {
	v := strings.TrimSpace(strings.ToLower(val))
	return v != "" && v != "0" && v != "false"
}

func pathContainsDir(pathEnv, dir string) bool {
	want := filepath.Clean(dir)
	wantReal, _ := filepath.EvalSymlinks(want)
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry == "" {
			continue
		}
		clean := filepath.Clean(entry)
		if samePathString(clean, want) {
			return true
		}
		if wantReal != "" {
			if realEntry, err := filepath.EvalSymlinks(clean); err == nil && samePathString(realEntry, wantReal) {
				return true
			}
		}
	}
	return false
}

func samePathString(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func isSameExecutable(a, b string) bool {
	sa, err1 := os.Stat(a) //nolint:gosec // Comparing local PATH executable against managed binary.
	sb, err2 := os.Stat(b) //nolint:gosec // Managed binary path under LANESH_HOME.
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func findLaneshInPath(pathEnv string) string {
	names := []string{"lanesh"}
	if runtime.GOOS == "windows" {
		names = []string{"lanesh.exe", "lanesh.cmd", "lanesh.bat"}
	}
	return findInPath(pathEnv, names)
}

func findInPath(pathEnv string, names []string) string {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || isEphemeralLauncherPath(dir+string(filepath.Separator)) {
			continue
		}
		for _, name := range names {
			cand := filepath.Join(dir, name)
			info, err := os.Stat(cand) //nolint:gosec // Standard PATH lookup for lanesh executable.
			if err != nil || info.IsDir() {
				continue
			}
			if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
				continue
			}
			return cand
		}
	}
	return ""
}

// isPersistentNPMLauncher reports whether found is a globally installed npm
// launcher for lanesh (as opposed to an ephemeral npx/dlx/bunx shim).
func isPersistentNPMLauncher(found string) bool {
	realPath, err := filepath.EvalSymlinks(found)
	if err != nil {
		realPath = found
	}
	if isEphemeralLauncherPath(found) || isEphemeralLauncherPath(realPath) {
		return false
	}
	if strings.HasSuffix(strings.ToLower(filepath.ToSlash(realPath)), "/lanesh/bin/lanesh.js") {
		return true
	}
	// Package managers like pnpm or npm on Windows generate small wrapper shims
	// that invoke lanesh/bin/lanesh.js.
	info, err := os.Stat(found) //nolint:gosec // Path was discovered via PATH lookup.
	if err != nil || info.Size() > 4096 {
		return false
	}
	raw, err := os.ReadFile(found) //nolint:gosec // Path was discovered via PATH lookup.
	if err != nil {
		return false
	}
	s := strings.ToLower(filepath.ToSlash(string(raw)))
	return strings.Contains(s, "lanesh/bin/lanesh.js")
}

func isEphemeralLauncherPath(p string) bool {
	s := strings.ToLower(filepath.ToSlash(p))
	for _, marker := range []string{"/node_modules/.bin/", "/_npx/", "/pnpm-dlx/", "/dlx-", "/bunx-", "/npm-cache/"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// addWindowsPathEntry prepends binDir to a semicolon-separated Windows PATH
// string if it is not already present (case-insensitive, ignoring trailing
// backslashes).
func addWindowsPathEntry(userPath, binDir string) (updated string, added bool) {
	want := strings.TrimRight(strings.TrimSpace(binDir), `\/`)
	for _, entry := range strings.Split(userPath, ";") {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(entry), `\/`), want) {
			return userPath, false
		}
	}
	trimmed := strings.Trim(userPath, ";")
	if trimmed == "" {
		return binDir, true
	}
	return binDir + ";" + trimmed, true
}
