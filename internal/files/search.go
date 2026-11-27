package files

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Limits for directory listing and text search.
const (
	DefaultListDepth   = 4
	MaxListDepth       = 16
	MaxListEntries     = 500
	DefaultGrepMatches = 100
	MaxGrepMatches     = 500
	maxGrepFileSize    = 2 << 20
	maxGrepLineRunes   = 240
)

// SkipDirs are noise directories skipped during directory listing and search
// (unless explicitly targeted as the root).
var SkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
}

// ListDir walks dir up to maxDepth levels and returns entries relative to dir,
// skipping noise directories and, unless includeHidden is set, dotfiles.
func ListDir(dir string, maxDepth int, includeHidden bool) (protocol.FileListResult, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return protocol.FileListResult{}, fmt.Errorf("%s: %w", dir, ErrNotFound)
		}
		return protocol.FileListResult{}, err
	}
	if !info.IsDir() {
		return protocol.FileListResult{}, fmt.Errorf("%s is not a directory", dir)
	}
	if maxDepth <= 0 {
		maxDepth = DefaultListDepth
	}
	maxDepth = min(maxDepth, MaxListDepth)

	// Walk the symlink-resolved root so a symlinked directory itself is entered.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		root = dir
	}

	res := protocol.FileListResult{Path: dir, Entries: []protocol.FileEntry{}}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		name := d.Name()
		if !includeHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		depth := strings.Count(relSlash, "/") + 1
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(res.Entries) >= MaxListEntries {
			res.Truncated = true
			return filepath.SkipAll
		}
		entry := protocol.FileEntry{Path: relSlash, Dir: d.IsDir()}
		if d.IsDir() {
			res.Entries = append(res.Entries, entry)
			if SkipDirs[name] || depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if fi, err := d.Info(); err == nil {
			entry.Size = fi.Size()
		}
		res.Entries = append(res.Entries, entry)
		return nil
	})
	return res, nil
}

// Grep searches UTF-8 text files under target (a file or directory) for
// p.Pattern, skipping noise directories, binary files and files over 2 MiB.
func Grep(target string, p protocol.FileGrepParams) (protocol.FileGrepResult, error) {
	if p.Pattern == "" {
		return protocol.FileGrepResult{}, errors.New("pattern is empty")
	}
	expr := p.Pattern
	if !p.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if p.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return protocol.FileGrepResult{}, fmt.Errorf("invalid pattern: %w", err)
	}
	if p.Glob != "" {
		if _, err := filepath.Match(p.Glob, "x"); err != nil {
			return protocol.FileGrepResult{}, fmt.Errorf("invalid glob %q: %w", p.Glob, err)
		}
	}
	limit := p.MaxMatches
	if limit <= 0 {
		limit = DefaultGrepMatches
	}
	limit = min(limit, MaxGrepMatches)

	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return protocol.FileGrepResult{}, fmt.Errorf("%s: %w", target, ErrNotFound)
		}
		return protocol.FileGrepResult{}, err
	}

	res := protocol.FileGrepResult{Path: target, Matches: []protocol.GrepMatch{}}
	grepOne := func(fullPath, displayPath string, size int64) error {
		if p.Glob != "" && !matchGlob(p.Glob, displayPath) {
			return nil
		}
		if size > maxGrepFileSize {
			res.Skipped = append(res.Skipped, displayPath+" (larger than 2 MB)")
			return nil
		}
		data, err := os.ReadFile(fullPath) //nolint:gosec // Callers validate target against policy.
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return nil
		}
		res.FilesSearched++
		matched := false
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64<<10), maxGrepFileSize)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if !re.MatchString(line) {
				continue
			}
			if len(res.Matches) >= limit {
				res.Truncated = true
				return filepath.SkipAll
			}
			if !matched {
				matched = true
				res.FilesMatched++
			}
			res.Matches = append(res.Matches, protocol.GrepMatch{
				Path: displayPath, Line: n, Text: clipRunes(strings.TrimRight(line, "\r"), maxGrepLineRunes),
			})
		}
		return nil
	}

	if !info.IsDir() {
		_ = grepOne(target, filepath.Base(target), info.Size())
		return res, nil
	}

	root, err := filepath.EvalSymlinks(target)
	if err != nil {
		root = target
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && SkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		return grepOne(path, filepath.ToSlash(rel), fi.Size())
	})
	return res, nil
}

func matchGlob(pattern, relSlash string) bool {
	if ok, _ := filepath.Match(pattern, filepath.Base(relSlash)); ok {
		return true
	}
	ok, _ := filepath.Match(pattern, relSlash)
	return ok
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
