package skills

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Limits for grep.
const (
	DefaultGrepMatches = 100
	MaxGrepMatches     = 500
	maxGrepFileSize    = 2 << 20
	maxGrepLineRunes   = 240
)

// Grep searches the text files of a skill (or one file or directory in it)
// for a literal string or, with Regex, an RE2 regular expression.
func Grep(idx protocol.SkillIndex, p protocol.SkillGrepParams) (protocol.SkillGrepResult, error) {
	s, ok := Find(idx, p.Skill)
	if !ok {
		return protocol.SkillGrepResult{}, fmt.Errorf("skill %q: %w", p.Skill, ErrNotFound)
	}
	if p.Pattern == "" {
		return protocol.SkillGrepResult{}, fmt.Errorf("pattern is empty")
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
		return protocol.SkillGrepResult{}, fmt.Errorf("invalid pattern: %w", err)
	}
	limit := p.MaxMatches
	if limit <= 0 {
		limit = DefaultGrepMatches
	}
	limit = min(limit, MaxGrepMatches)

	root, err := filepath.EvalSymlinks(s.Path)
	if err != nil {
		return protocol.SkillGrepResult{}, fmt.Errorf("%s: %w", s.Path, ErrNotFound)
	}
	start := root
	if rel := filepath.Clean(filepath.FromSlash(strings.TrimSuffix(p.Path, "/"))); p.Path != "" && rel != "." {
		if !filepath.IsLocal(rel) {
			return protocol.SkillGrepResult{}, ErrOutside
		}
		if start, err = within(s.Path, rel); err != nil {
			return protocol.SkillGrepResult{}, err
		}
	}

	res := protocol.SkillGrepResult{Skill: s.Name, Matches: []protocol.GrepMatch{}}
	_ = filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != start && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > maxGrepFileSize {
			res.Skipped = append(res.Skipped, rel+" (larger than 2 MB)")
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // Inside the skill directory.
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return nil // binary
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
			res.Matches = append(res.Matches, protocol.GrepMatch{Path: rel, Line: n, Text: clip(strings.TrimRight(line, "\r"), maxGrepLineRunes)})
		}
		return nil
	})
	return res, nil
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
