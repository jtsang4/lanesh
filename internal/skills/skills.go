// Package skills discovers globally installed agent skills on a machine.
package skills

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v4"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// SkillFile is the file that marks a directory as a skill.
const SkillFile = "SKILL.md"

// Limits that keep scanning and reading cheap.
const (
	maxHashFiles    = 2000
	maxTreeEntries  = 500
	DefaultReadSize = 256 << 10
	MaxReadSize     = 1 << 20
)

// skipDirs are never descended into when hashing or listing a skill.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "__pycache__": true, ".venv": true}

// Source is a directory containing skills, in priority order.
type Source struct {
	Name string
	Dir  string
}

// DefaultSources returns the global skill directories under home, highest
// priority first.
func DefaultSources(home string) []Source {
	return []Source{
		{Name: "agents", Dir: filepath.Join(home, ".agents", "skills")},
		{Name: "claude", Dir: filepath.Join(home, ".claude", "skills")},
	}
}

// Scan builds the skill index from sources. Missing directories are ignored.
func Scan(sources []Source) protocol.SkillIndex {
	idx := protocol.SkillIndex{Skills: []protocol.Skill{}}
	byName := map[string]bool{}
	byReal := map[string]bool{}

	for _, src := range sources {
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(src.Dir, e.Name())
			resolved, err := filepath.EvalSymlinks(dir)
			if err != nil {
				continue
			}
			info, err := os.Stat(resolved)
			if err != nil || !info.IsDir() {
				continue
			}
			meta, err := readMeta(filepath.Join(resolved, SkillFile))
			if err != nil {
				continue
			}
			name := meta.Name
			if name == "" {
				name = e.Name()
			}
			if byReal[resolved] {
				continue // the same skill reached through a symlink
			}
			byReal[resolved] = true
			if byName[name] {
				idx.Shadowed = append(idx.Shadowed, protocol.ShadowedSkill{Name: name, Source: src.Name, Path: dir})
				continue
			}
			byName[name] = true
			idx.Skills = append(idx.Skills, protocol.Skill{
				Name:        name,
				Description: meta.Description,
				Source:      src.Name,
				Path:        dir,
				Hash:        hashDir(resolved),
			})
		}
	}
	sort.Slice(idx.Skills, func(i, j int) bool { return idx.Skills[i].Name < idx.Skills[j].Name })
	idx.Hash = indexHash(idx)
	return idx
}

func indexHash(idx protocol.SkillIndex) string {
	h := sha256.New()
	for _, s := range idx.Skills {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\n", s.Name, s.Source, s.Description, s.Hash)
	}
	for _, s := range idx.Shadowed {
		fmt.Fprintf(h, "shadowed\x00%s\x00%s\n", s.Name, s.Source)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// readMeta parses the YAML frontmatter of a SKILL.md file.
func readMeta(path string) (meta, error) {
	f, err := os.Open(path) //nolint:gosec // Path comes from the local skill directories.
	if err != nil {
		return meta{}, err
	}
	defer f.Close()

	r := bufio.NewReader(io.LimitReader(f, 64<<10))
	first, err := r.ReadString('\n')
	if err != nil && first == "" {
		return meta{}, nil
	}
	if strings.TrimSpace(first) != "---" {
		return meta{}, nil
	}
	var buf bytes.Buffer
	for {
		line, err := r.ReadString('\n')
		if strings.TrimSpace(line) == "---" {
			break
		}
		buf.WriteString(line)
		if err != nil {
			break
		}
	}
	var m meta
	if err := yaml.Unmarshal(buf.Bytes(), &m); err != nil {
		return meta{}, nil
	}
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.Join(strings.Fields(m.Description), " ")
	return m, nil
}

// hashDir hashes relative paths and contents of the files in dir.
func hashDir(dir string) string {
	type file struct{ rel, sum string }
	var files []file
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= maxHashFiles {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(dir, p)
		files = append(files, file{rel: filepath.ToSlash(rel), sum: hashFile(p)})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\n", f.rel, f.sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(p string) string {
	f, err := os.Open(p) //nolint:gosec // Path is inside a local skill directory.
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

// ErrNotFound is returned when a skill or file does not exist.
var ErrNotFound = errors.New("not found")

// ErrOutside is returned for paths that escape the skill directory.
var ErrOutside = errors.New("path is outside the skill directory")

// Find returns the skill called name from idx.
func Find(idx protocol.SkillIndex, name string) (protocol.Skill, bool) {
	for _, s := range idx.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return protocol.Skill{}, false
}

// Read reads SKILL.md (when p.Path is empty, together with the file tree) or a
// file inside the skill directory.
func Read(idx protocol.SkillIndex, p protocol.SkillReadParams) (protocol.SkillReadResult, error) {
	s, ok := Find(idx, p.Skill)
	if !ok {
		return protocol.SkillReadResult{}, fmt.Errorf("skill %q: %w", p.Skill, ErrNotFound)
	}
	rel := p.Path
	withTree := rel == ""
	if withTree {
		rel = SkillFile
	}
	rel = filepath.FromSlash(rel)
	if !filepath.IsLocal(rel) {
		return protocol.SkillReadResult{}, ErrOutside
	}
	res, err := ReadFile(filepath.Join(s.Path, rel), p.Offset, p.Limit, "")
	if err != nil {
		return protocol.SkillReadResult{}, err
	}
	out := protocol.SkillReadResult{
		Skill: s.Name, Dir: s.Path, Path: filepath.ToSlash(rel),
		Content: res.Content, Encoding: res.Encoding, Size: res.Size, Offset: res.Offset, Truncated: res.Truncated,
	}
	if withTree {
		out.Files = Tree(s.Path)
	}
	return out, nil
}

// Tree lists files under dir (relative, slash-separated), skipping noise.
func Tree(dir string) []protocol.FileEntry {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	var out []protocol.FileEntry
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if len(out) >= maxTreeEntries {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, p)
		e := protocol.FileEntry{Path: filepath.ToSlash(rel), Dir: d.IsDir()}
		if d.IsDir() {
			out = append(out, e)
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil {
			e.Size = info.Size()
		}
		out = append(out, e)
		return nil
	})
	return out
}

// ReadFile reads up to limit bytes of path from offset. Text is returned as
// UTF-8; binary content (or encoding "base64") as base64.
func ReadFile(path string, offset, limit int64, encoding string) (protocol.FileReadResult, error) {
	if limit <= 0 {
		limit = DefaultReadSize
	}
	if limit > MaxReadSize {
		limit = MaxReadSize
	}
	f, err := os.Open(path) //nolint:gosec // Callers validate the path.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return protocol.FileReadResult{}, fmt.Errorf("%s: %w", path, ErrNotFound)
		}
		return protocol.FileReadResult{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return protocol.FileReadResult{}, err
	}
	if info.IsDir() {
		return protocol.FileReadResult{}, fmt.Errorf("%s is a directory", path)
	}
	if offset < 0 || offset > info.Size() {
		offset = 0
	}
	buf := make([]byte, limit)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return protocol.FileReadResult{}, err
	}
	data := buf[:n]
	res := protocol.FileReadResult{
		Path: path, Size: info.Size(), Offset: offset,
		Truncated: offset+int64(n) < info.Size(),
	}
	if encoding != protocol.EncodingBase64 && isText(data) {
		res.Content, res.Encoding = string(trimPartialRune(data, res.Truncated)), protocol.EncodingUTF8
	} else {
		res.Content, res.Encoding = base64.StdEncoding.EncodeToString(data), protocol.EncodingBase64
	}
	return res, nil
}

func isText(b []byte) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	// Allow a rune cut at the end of a partial read.
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return utf8.Valid(b)
}

func trimPartialRune(b []byte, truncated bool) []byte {
	if !truncated {
		return b
	}
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return b
}
