package files

import (
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
// whether it exists, whether it is in a Git working tree (and on which
// branch), and which agent instruction files it has. It reads files only and
// never runs commands.
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
	if !info.Dir {
		return info
	}
	info.Git, info.Branch, info.Worktree = gitState(resolved)
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
// dir and its parents, and reads its HEAD.
func gitState(dir string) (git bool, branch string, worktree bool) {
	for d := dir; ; {
		dotGit := filepath.Join(d, ".git")
		if st, err := os.Lstat(dotGit); err == nil {
			gitDir := dotGit
			if !st.IsDir() {
				// A linked worktree or submodule: .git is a file naming the
				// real Git directory.
				data, err := os.ReadFile(dotGit) //nolint:gosec // A fixed name inside the inspected directory.
				if err != nil {
					return true, "", false
				}
				target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
				if !ok {
					return true, "", false
				}
				gitDir = strings.TrimSpace(target)
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(d, gitDir)
				}
				worktree = strings.Contains(filepath.ToSlash(gitDir), "/worktrees/")
			}
			return true, headBranch(gitDir), worktree
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false, "", false
		}
		d = parent
	}
}

func headBranch(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD")) //nolint:gosec // HEAD of a Git directory found above.
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		return ref
	}
	if len(head) >= 7 {
		return "detached at " + head[:7]
	}
	return ""
}
