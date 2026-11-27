package files

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// MaxInspectPaths bounds one path.inspect request.
const MaxInspectPaths = 50

// agentFiles are the instruction files agents read from a project directory,
// relative to it, in the order they are reported.
var agentFiles = []string{"AGENTS.md", "CLAUDE.md", filepath.Join(".claude", "CLAUDE.md")}

// Inspect reports facts about a directory that help an agent work in it:
// whether it exists, its canonical path when symbolic links are involved,
// whether it is in a Git working tree (branch, short commit, worktree), and
// which agent instruction files it has. It reads files only and never runs
// commands.
func Inspect(path, home string) protocol.PathInfo {
	info := protocol.PathInfo{Path: path}
	resolved, err := Expand(path, home)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Resolved = resolved
	st, err := os.Stat(resolved)
	if err != nil {
		return info
	}
	info.Exists, info.Dir = true, st.IsDir()
	if realPath, err := filepath.EvalSymlinks(resolved); err == nil && realPath != resolved {
		info.RealPath = realPath
	}
	if !info.Dir {
		return info
	}
	info.Git, info.Branch, info.Commit, info.Worktree = gitState(resolved)
	info.AgentFiles = findAgentFiles(resolved)
	return info
}

// findAgentFiles lists agent instruction files in dir. A file that is the
// same as one listed before it (typically CLAUDE.md as a link to AGENTS.md)
// is reported as "CLAUDE.md -> AGENTS.md", so that it is not read twice.
func findAgentFiles(dir string) []string {
	var out []string
	seen := map[string]string{} // real path -> name
	for _, name := range agentFiles {
		p := filepath.Join(dir, name)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		label := filepath.ToSlash(name)
		target, err := filepath.EvalSymlinks(p)
		if err == nil {
			if first, ok := seen[target]; ok {
				label += " -> " + first
			} else {
				seen[target] = label
			}
		}
		out = append(out, label)
	}
	return out
}

// gitState finds the Git working tree containing dir by looking for .git in
// dir and its parents, and reads its HEAD branch and short commit hash.
func gitState(dir string) (git bool, branch, commit string, worktree bool) {
	for d := dir; ; {
		dotGit := filepath.Join(d, ".git")
		if st, err := os.Stat(dotGit); err == nil {
			gitDir := dotGit
			commonDir := dotGit
			if !st.IsDir() {
				// A linked worktree or submodule: .git is a file naming the
				// real Git directory.
				data, err := os.ReadFile(dotGit) //nolint:gosec // A fixed name inside the inspected directory.
				if err != nil {
					return true, "", "", false
				}
				target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
				if !ok {
					return true, "", "", false
				}
				gitDir = strings.TrimSpace(target)
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(d, gitDir)
				}
				commonDir = gitDir
				// A linked worktree's Git directory names the main one in
				// commondir; a submodule's does not.
				if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil { //nolint:gosec // Follows .git as Git does; only checks existence, under the file access policy.
					worktree = true
					cd := strings.TrimSpace(string(raw))
					if cd != "" {
						if !filepath.IsAbs(cd) {
							cd = filepath.Join(gitDir, cd)
						}
						commonDir = filepath.Clean(cd)
					}
				}
			}
			branch, commit = headState(gitDir, commonDir)
			return true, branch, commit, worktree
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false, "", "", false
		}
		d = parent
	}
}

func headState(gitDir, commonDir string) (branch, commit string) {
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD")) //nolint:gosec // HEAD of a Git directory found above.
	if err != nil {
		return "", ""
	}
	head := strings.TrimSpace(string(data))
	if refName, ok := strings.CutPrefix(head, "ref: "); ok {
		refName = strings.TrimSpace(refName)
		if ref, ok := strings.CutPrefix(refName, "refs/heads/"); ok && ref != ".invalid" {
			branch = ref
		}
		if refName != "" && !strings.HasSuffix(refName, "/.invalid") {
			commit = resolveRefCommit(gitDir, commonDir, refName)
		}
		return branch, commit
	}
	if short := shortCommit(head); short != "" {
		return "detached at " + short, short
	}
	return "", ""
}

func resolveRefCommit(gitDir, commonDir, refName string) string {
	// Refuse any ref path that escapes the git directory.
	rel := filepath.Clean(filepath.FromSlash(refName))
	if !filepath.IsLocal(rel) {
		return ""
	}
	dirs := []string{gitDir}
	if commonDir != "" && commonDir != gitDir {
		dirs = append(dirs, commonDir)
	}
	for _, base := range dirs {
		if data, err := os.ReadFile(filepath.Join(base, rel)); err == nil { //nolint:gosec // Local ref under gitDir/commonDir validated with filepath.IsLocal.
			if short := shortCommit(strings.TrimSpace(string(data))); short != "" {
				return short
			}
		}
	}
	// Fall back to packed-refs in commonDir.
	packedDir := commonDir
	if packedDir == "" {
		packedDir = gitDir
	}
	f, err := os.Open(filepath.Join(packedDir, "packed-refs")) //nolint:gosec // Fixed filename inside the Git directory.
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		hash, name, ok := strings.Cut(line, " ")
		if ok && name == refName {
			return shortCommit(hash)
		}
	}
	return ""
}

func shortCommit(s string) string {
	if len(s) < 7 {
		return ""
	}
	for i := range len(s) {
		c := s[i]
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return ""
		}
	}
	return strings.ToLower(s[:7])
}
