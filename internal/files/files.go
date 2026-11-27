// Package files implements inline file operations and the tar stream format
// used for transfers between machines.
package files

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Limits for inline operations.
const (
	DefaultReadSize  = 256 << 10
	MaxInline        = 1 << 20
	MaxImageInline   = 4 << 20
	maxLineScanBytes = 16 << 20
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
	return ReadFileWithParams(path, protocol.FileReadParams{
		Path: path, Offset: offset, Limit: limit, Encoding: encoding,
	})
}

// ReadFileWithParams reads a file by line range, as a native inline image
// (PNG, JPEG, WebP, GIF up to MaxImageInline), or by byte offset/limit.
func ReadFileWithParams(path string, p protocol.FileReadParams) (protocol.FileReadResult, error) {
	if p.StartLine < 0 || p.EndLine < 0 {
		return protocol.FileReadResult{}, errors.New("start_line and end_line must be positive")
	}
	byLines := p.StartLine > 0 || p.EndLine > 0
	if byLines && p.Offset > 0 {
		return protocol.FileReadResult{}, errors.New("cannot combine start_line/end_line with offset")
	}
	if byLines && p.Encoding == protocol.EncodingBase64 {
		return protocol.FileReadResult{}, errors.New("cannot combine start_line/end_line with base64 encoding")
	}
	limit := p.Limit
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
		return protocol.FileReadResult{}, fmt.Errorf("%s is a directory; use list_dir instead", path)
	}

	if byLines {
		return readFileLines(f, path, info.Size(), p.StartLine, p.EndLine, limit)
	}

	// Detect common raster images when reading from the start without forced base64.
	if p.Offset <= 0 && p.Encoding != protocol.EncodingBase64 && info.Size() > 0 {
		var head [512]byte
		hn, _ := f.ReadAt(head[:], 0)
		if mime := detectImageMIME(head[:hn]); mime != "" {
			if info.Size() > MaxImageInline {
				return protocol.FileReadResult{
					Path: path, Encoding: protocol.EncodingBase64, Size: info.Size(),
					Truncated: true, MIMEType: mime,
				}, nil
			}
			raw := make([]byte, info.Size())
			n, err := f.ReadAt(raw, 0)
			if err != nil && !errors.Is(err, io.EOF) {
				return protocol.FileReadResult{}, err
			}
			return protocol.FileReadResult{
				Path: path, Content: base64.StdEncoding.EncodeToString(raw[:n]),
				Encoding: protocol.EncodingBase64, Size: info.Size(), MIMEType: mime,
			}, nil
		}
	}

	offset := max(p.Offset, 0)
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
	if p.Encoding != protocol.EncodingBase64 && isText(data) {
		res.Content, res.Encoding = string(trimPartialRune(data)), protocol.EncodingUTF8
	} else {
		res.Content, res.Encoding = base64.StdEncoding.EncodeToString(data), protocol.EncodingBase64
	}
	return res, nil
}

func detectImageMIME(head []byte) string {
	if len(head) == 0 {
		return ""
	}
	switch http.DetectContentType(head) {
	case "image/png":
		return "image/png"
	case "image/jpeg":
		return "image/jpeg"
	case "image/webp":
		return "image/webp"
	case "image/gif":
		return "image/gif"
	default:
		return ""
	}
}

func readFileLines(f *os.File, path string, totalSize int64, startLine, endLine int, limit int64) (protocol.FileReadResult, error) {
	start := max(startLine, 1)
	if endLine > 0 && endLine < start {
		return protocol.FileReadResult{}, errors.New("end_line must be greater than or equal to start_line")
	}
	var head [512]byte
	hn, _ := f.ReadAt(head[:], 0)
	if hn > 0 && !isText(head[:hn]) {
		return protocol.FileReadResult{}, fmt.Errorf("%s is binary; use offset/limit instead of start_line/end_line", path)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return protocol.FileReadResult{}, err
	}

	r := bufio.NewReaderSize(f, 64<<10)
	var out strings.Builder
	var scanned int64
	lineNum := 0
	lastLine := 0
	hitBudget := false
	scanCapped := false

	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			scanned += int64(len(line))
			lineNum++
			if lineNum >= start && (endLine == 0 || lineNum <= endLine) && !hitBudget {
				if out.Len() > 0 && int64(out.Len()+len(line)) > limit {
					hitBudget = true
				} else {
					if int64(len(line)) > limit {
						trimmed := trimPartialRune([]byte(line[:limit]))
						out.Write(trimmed)
						lastLine = lineNum
						hitBudget = true
					} else {
						out.WriteString(line)
						lastLine = lineNum
					}
				}
			}
			// Stop scanning past maxLineScanBytes once we already collected the
			// requested range (or hit the byte limit), so huge files stay fast.
			if scanned >= maxLineScanBytes && (hitBudget || (endLine > 0 && lineNum >= endLine)) {
				scanCapped = true
				break
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return protocol.FileReadResult{}, err
		}
	}

	res := protocol.FileReadResult{
		Path:      path,
		Content:   out.String(),
		Encoding:  protocol.EncodingUTF8,
		Size:      totalSize,
		StartLine: start,
		EndLine:   lastLine,
		Truncated: hitBudget,
	}
	if !scanCapped {
		res.TotalLines = lineNum
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
