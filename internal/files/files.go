// Package files implements inline file operations and the tar stream format
// used for transfers between machines.
package files

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Limits for inline operations.
const (
	DefaultReadSize = 256 << 10
	MaxInline       = 1 << 20
)

// Errors returned by file operations.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrTooLarge = errors.New("too large for inline transfer; use transfer instead")
)

// Expand resolves "~" and relative paths against home and cleans the result.
func Expand(p, home string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is empty")
	}
	if p == "~" {
		return home, nil
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		p = filepath.Join(home, filepath.FromSlash(rest))
	} else if rest, ok := strings.CutPrefix(p, `~\`); ok {
		p = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(home, p)
	}
	return filepath.Clean(p), nil
}

// ReadFile reads up to limit bytes of path from offset. Text is returned as
// UTF-8; binary content (or encoding "base64") as base64.
func ReadFile(path string, offset, limit int64, encoding string) (protocol.FileReadResult, error) {
	if limit <= 0 {
		limit = DefaultReadSize
	}
	limit = min(limit, MaxInline)
	f, err := os.Open(path) //nolint:gosec // Callers validate the path against policy.
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
	offset = max(offset, 0)
	offset = min(offset, info.Size())
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
		res.Content, res.Encoding = string(trimPartialRune(data)), protocol.EncodingUTF8
	} else {
		res.Content, res.Encoding = base64.StdEncoding.EncodeToString(data), protocol.EncodingBase64
	}
	return res, nil
}

func isText(b []byte) bool {
	return bytes.IndexByte(b, 0) < 0 && utf8.Valid(trimPartialRune(b))
}

// trimPartialRune drops a UTF-8 sequence cut at the end of a partial read.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return b
}

// WriteFile writes content to path atomically.
func WriteFile(path string, p protocol.FileWriteParams) (protocol.FileWriteResult, error) {
	var data []byte
	switch p.Encoding {
	case "", protocol.EncodingUTF8:
		data = []byte(p.Content)
	case protocol.EncodingBase64:
		var err error
		if data, err = base64.StdEncoding.DecodeString(p.Content); err != nil {
			return protocol.FileWriteResult{}, fmt.Errorf("decode base64: %w", err)
		}
	default:
		return protocol.FileWriteResult{}, fmt.Errorf("unknown encoding %q", p.Encoding)
	}
	if len(data) > MaxInline {
		return protocol.FileWriteResult{}, ErrTooLarge
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return protocol.FileWriteResult{}, fmt.Errorf("%s is a directory", path)
		}
		if !p.Overwrite {
			return protocol.FileWriteResult{}, fmt.Errorf("%s: %w (set overwrite to replace it)", path, ErrExists)
		}
	}
	dir := filepath.Dir(path)
	if p.CreateDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // Directories follow the user's umask.
			return protocol.FileWriteResult{}, err
		}
	}
	mode := fs.FileMode(0o644)
	if p.Mode != 0 {
		mode = fs.FileMode(p.Mode) & fs.ModePerm
	}
	if err := writeAtomic(path, bytes.NewReader(data), mode); err != nil {
		return protocol.FileWriteResult{}, err
	}
	return protocol.FileWriteResult{Path: path, Size: int64(len(data))}, nil
}

func writeAtomic(path string, r io.Reader, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".lanesh-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // Cleanup of the temp file after rename is a no-op.
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return replace(tmp.Name(), path)
}

// replace renames src over dst, removing dst first where rename cannot overwrite.
func replace(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Stat describes path.
func Stat(path string) protocol.StatResult {
	info, err := os.Stat(path)
	if err != nil {
		return protocol.StatResult{Path: path}
	}
	return protocol.StatResult{Path: path, Exists: true, Dir: info.IsDir(), Size: info.Size()}
}
