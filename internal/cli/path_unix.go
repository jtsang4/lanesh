//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jtsang4/lanesh/internal/daemon"
)

type unixPathEnv struct {
	home          string
	pathEnv       string
	shell         string
	zdotdir       string
	xdgConfigHome string
	goos          string
	noModify      bool
	candidates    []string
}

func exposeManagedBinary(p daemon.Paths) pathExposeResult {
	home, _ := os.UserHomeDir()
	return exposeUnix(p, unixPathEnv{
		home:          home,
		pathEnv:       os.Getenv("PATH"),
		shell:         os.Getenv("SHELL"),
		zdotdir:       os.Getenv("ZDOTDIR"),
		xdgConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		goos:          runtime.GOOS,
		noModify:      modifyPathDisabled(os.Getenv("LANESH_NO_MODIFY_PATH")),
	})
}

func exposeUnix(p daemon.Paths, env unixPathEnv) pathExposeResult {
	if env.noModify || !isDefaultHome(p, env.home) {
		return pathExposeResult{Kind: pathSkipped, BinDir: p.BinDir()}
	}
	if pathContainsDir(env.pathEnv, p.BinDir()) {
		return pathExposeResult{Kind: pathNoop}
	}
	if found := findInPath(env.pathEnv, []string{"lanesh"}); found != "" {
		if isSameExecutable(found, p.Binary()) || isPersistentNPMLauncher(found) {
			return pathExposeResult{Kind: pathNoop}
		}
	}

	candidates := env.candidates
	if candidates == nil {
		candidates = []string{
			filepath.Join(env.home, ".local", "bin"),
			filepath.Join(env.home, "bin"),
			"/usr/local/bin",
		}
	}

	for _, dir := range candidates {
		if dir == "" || !pathContainsDir(env.pathEnv, dir) || isHomebrewBinDir(dir) {
			continue
		}
		if linkPath, ok := trySymlinkInDir(dir, p.Binary(), env.home); ok {
			shadowed := ""
			if found := findInPath(env.pathEnv, []string{"lanesh"}); found != "" && !isSameExecutable(found, p.Binary()) {
				shadowed = found
			}
			return pathExposeResult{
				Kind:     pathLinked,
				LinkPath: linkPath,
				Target:   p.Binary(),
				Shadowed: shadowed,
				BinDir:   p.BinDir(),
			}
		}
	}

	profile, line, reloadCmd := shellProfileSpec(env)
	dispProfile := tildePath(profile, env.home)
	dispBin := tildePath(p.BinDir(), env.home)

	if raw, err := os.ReadFile(profile); err == nil { //nolint:gosec // User's own shell profile.
		if profileContainsBinDir(string(raw), p.BinDir()) {
			return pathExposeResult{
				Kind:      pathProfileExists,
				Profile:   dispProfile,
				ReloadCmd: reloadCmd,
				BinDir:    dispBin,
			}
		}
	}
	if err := appendToProfile(profile, line); err != nil {
		return pathExposeResult{Kind: pathManualHint, BinDir: dispBin}
	}
	return pathExposeResult{
		Kind:      pathProfileAdded,
		Profile:   dispProfile,
		ReloadCmd: reloadCmd,
		BinDir:    dispBin,
	}
}

func isHomebrewBinDir(dir string) bool {
	clean := filepath.Clean(dir)
	if clean == "/opt/homebrew/bin" {
		return true
	}
	if _, err := os.Lstat(filepath.Join(clean, "brew")); err == nil {
		return true
	}
	if clean == "/usr/local/bin" {
		if fi, err := os.Stat("/usr/local/Homebrew"); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

func trySymlinkInDir(dir, binary, userHome string) (string, bool) {
	cleanDir := filepath.Clean(dir)
	if userHome != "" && strings.HasPrefix(cleanDir, filepath.Clean(userHome)+string(filepath.Separator)) {
		if err := os.MkdirAll(cleanDir, 0o755); err != nil { //nolint:gosec // User bin directory.
			return "", false
		}
	} else {
		fi, err := os.Stat(cleanDir)
		if err != nil || !fi.IsDir() {
			return "", false
		}
	}

	linkPath := filepath.Join(cleanDir, "lanesh")
	if fi, err := os.Lstat(linkPath); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return "", false
		}
		dest, rerr := os.Readlink(linkPath)
		if rerr != nil || !isLaneshSymlinkTarget(dest, binary) {
			return "", false
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false
	}

	tmp := filepath.Join(cleanDir, fmt.Sprintf(".lanesh.tmp.%d", os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink(binary, tmp); err != nil {
		return "", false
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		_ = os.Remove(tmp)
		return "", false
	}
	return linkPath, true
}

func isLaneshSymlinkTarget(dest, binary string) bool {
	if filepath.Clean(dest) == filepath.Clean(binary) {
		return true
	}
	return strings.HasSuffix(filepath.ToSlash(filepath.Clean(dest)), "/.lanesh/bin/lanesh")
}

func shellQuotePath(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

func shellProfileSpec(env unixPathEnv) (profile, line, reloadCmd string) {
	shellName := filepath.Base(env.shell)
	switch shellName {
	case "zsh":
		zdir := env.zdotdir
		if zdir == "" {
			zdir = env.home
		}
		profile = filepath.Join(zdir, ".zshrc")
		line = `export PATH="$HOME/.lanesh/bin:$PATH"`
		reloadCmd = "source " + shellQuotePath(tildePath(profile, env.home))
	case "bash":
		if env.goos == "darwin" {
			profile = filepath.Join(env.home, ".bash_profile")
		} else {
			profile = filepath.Join(env.home, ".bashrc")
		}
		line = `export PATH="$HOME/.lanesh/bin:$PATH"`
		reloadCmd = "source " + shellQuotePath(tildePath(profile, env.home))
	case "fish":
		xdg := env.xdgConfigHome
		if xdg == "" {
			xdg = filepath.Join(env.home, ".config")
		}
		profile = filepath.Join(xdg, "fish", "config.fish")
		line = `set -gx PATH "$HOME/.lanesh/bin" $PATH`
		reloadCmd = "source " + shellQuotePath(tildePath(profile, env.home))
	default:
		profile = filepath.Join(env.home, ".profile")
		line = `export PATH="$HOME/.lanesh/bin:$PATH"`
		reloadCmd = ". " + shellQuotePath(tildePath(profile, env.home))
	}
	return profile, line, reloadCmd
}

func profileContainsBinDir(content, binDir string) bool {
	slashBin := filepath.ToSlash(filepath.Clean(binDir))
	for _, rawLine := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, ".lanesh/bin") || strings.Contains(filepath.ToSlash(trimmed), slashBin) {
			return true
		}
	}
	return false
}

func appendToProfile(profile, line string) error {
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil { //nolint:gosec // Shell config directory.
		return err
	}
	existing, err := os.ReadFile(profile) //nolint:gosec // User's own shell profile.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(profile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // Shell profile file.
	if err != nil {
		return err
	}
	var b strings.Builder
	if len(existing) > 0 {
		if existing[len(existing)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString("# Added by Lanesh\n")
	b.WriteString(line)
	b.WriteByte('\n')
	_, werr := f.WriteString(b.String())
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func tildePath(p, home string) string {
	if home == "" {
		return p
	}
	cleanHome := filepath.Clean(home)
	cleanP := filepath.Clean(p)
	if cleanP == cleanHome {
		return "~"
	}
	prefix := cleanHome + string(filepath.Separator)
	if strings.HasPrefix(cleanP, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(cleanP, prefix)
	}
	return p
}
