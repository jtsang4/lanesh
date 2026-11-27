package files

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jtsang4/lanesh/internal/protocol"
)

func makeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadFileLinesAndImage(t *testing.T) {
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "lines.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(txtPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := ReadFileWithParams(txtPath, protocol.FileReadParams{StartLine: 2, EndLine: 4})
	if err != nil {
		t.Fatal(err)
	}
	if res.StartLine != 2 || res.EndLine != 4 || res.TotalLines != 5 || res.Truncated || res.Content != "line2\nline3\nline4\n" {
		t.Fatalf("line range = %+v", res)
	}

	// Byte limit truncates mid-range and reports last returned line.
	res, err = ReadFileWithParams(txtPath, protocol.FileReadParams{StartLine: 1, EndLine: 5, Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	if res.StartLine != 1 || res.EndLine != 2 || res.TotalLines != 5 || !res.Truncated || res.Content != "line1\nline2\n" {
		t.Fatalf("budgeted lines = %+v", res)
	}

	// Past EOF reports 0 returned lines and total line count.
	res, err = ReadFileWithParams(txtPath, protocol.FileReadParams{StartLine: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.EndLine != 0 || res.TotalLines != 5 || res.Content != "" {
		t.Fatalf("past EOF = %+v", res)
	}

	// Validation errors.
	if _, err := ReadFileWithParams(txtPath, protocol.FileReadParams{StartLine: 2, Offset: 1}); err == nil {
		t.Fatal("start_line + offset must fail")
	}
	if _, err := ReadFileWithParams(txtPath, protocol.FileReadParams{StartLine: 4, EndLine: 2}); err == nil {
		t.Fatal("end_line < start_line must fail")
	}

	// Raster image detection returns MIME type and full base64 data.
	pngBytes := makeTestPNG(t)
	imgPath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(imgPath, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = ReadFileWithParams(imgPath, protocol.FileReadParams{})
	if err != nil {
		t.Fatal(err)
	}
	if res.MIMEType != "image/png" || res.Encoding != protocol.EncodingBase64 || res.Truncated {
		t.Fatalf("image read = %+v", res)
	}
	decoded, err := base64.StdEncoding.DecodeString(res.Content)
	if err != nil || !bytes.Equal(decoded, pngBytes) {
		t.Fatalf("decoded image mismatch: err=%v len=%d want=%d", err, len(decoded), len(pngBytes))
	}
	// Binary image refuses line-range reading.
	if _, err := ReadFileWithParams(imgPath, protocol.FileReadParams{StartLine: 1, EndLine: 2}); err == nil {
		t.Fatal("line range on binary image must fail")
	}
}

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	orig := "# Title\n\nalpha line\nbeta line\ngamma line\n"
	if err := os.WriteFile(p, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}

	// Batch atomic edit succeeds and returns a unified diff.
	res, err := EditFile(p, []protocol.FileEditOp{
		{OldText: "alpha line", NewText: "ALPHA"},
		{OldText: "gamma line", NewText: "GAMMA\ndelta line"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 2 || !strings.Contains(res.Diff, "-alpha line\n+ALPHA\n") || !strings.Contains(res.Diff, "+delta line\n") {
		t.Fatalf("edit result = %+v", res)
	}
	got, _ := os.ReadFile(p)
	want := "# Title\n\nALPHA\nbeta line\nGAMMA\ndelta line\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}

	// Failed second edit must leave the file untouched (atomic batch).
	if _, err := EditFile(p, []protocol.FileEditOp{
		{OldText: "ALPHA", NewText: "should-not-commit"},
		{OldText: "does-not-exist", NewText: "x"},
	}); err == nil || !strings.Contains(err.Error(), "occurs 0 times") {
		t.Fatalf("expected missing match error, got %v", err)
	}
	got, _ = os.ReadFile(p)
	if string(got) != want {
		t.Fatalf("failed batch modified file: %q", got)
	}

	// Non-unique match fails.
	dup := filepath.Join(dir, "dup.txt")
	_ = os.WriteFile(dup, []byte("x\nx\n"), 0o600)
	if _, err := EditFile(dup, []protocol.FileEditOp{{OldText: "x", NewText: "y"}}); err == nil || !strings.Contains(err.Error(), "occurs 2 times") {
		t.Fatalf("expected ambiguous match error, got %v", err)
	}

	// Missing file returns ErrNotFound.
	if _, err := EditFile(filepath.Join(dir, "missing.txt"), []protocol.FileEditOp{{OldText: "a", NewText: "b"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file err = %v", err)
	}
}

func TestListDirAndGrep(t *testing.T) {
	dir := t.TempDir()
	filesToWrite := map[string]string{
		"README.md":          "# Hello\nworld match\n",
		"src/main.go":        "package main\n// world match here\n",
		"src/sub/deep/x.txt": "deep world\n",
		".hidden/secret.txt": "secret world\n",
		".git/config":        "world in git\n",
		"node_modules/a.js":  "world in node_modules\n",
		"bin.dat":            "world\x00binary",
	}
	for rel, content := range filesToWrite {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Default ListDir skips hidden files/dirs and noise dirs, respects maxDepth.
	listed, err := ListDir(dir, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, e := range listed.Entries {
		paths[e.Path] = true
	}
	if !paths["README.md"] || !paths["src"] || !paths["src/main.go"] || !paths["src/sub"] {
		t.Fatalf("missing expected entries: %+v", listed.Entries)
	}
	if paths[".hidden"] || paths[".git"] || paths["node_modules/a.js"] || paths["src/sub/deep/x.txt"] {
		t.Fatalf("unexpected entries in depth-2 non-hidden list: %+v", listed.Entries)
	}

	// Hidden entries included when requested.
	listedHidden, err := ListDir(dir, 4, true)
	if err != nil {
		t.Fatal(err)
	}
	foundHidden := false
	for _, e := range listedHidden.Entries {
		if e.Path == ".hidden/secret.txt" {
			foundHidden = true
		}
		if e.Path == ".git/config" || e.Path == "node_modules/a.js" {
			t.Fatalf("noise dir contents must still be skipped: %+v", e)
		}
	}
	if !foundHidden {
		t.Fatalf("expected hidden file when includeHidden=true: %+v", listedHidden.Entries)
	}

	// Grep across directory skips .git, node_modules, binary files; honors glob.
	gr, err := Grep(dir, protocol.FileGrepParams{Pattern: "WORLD", IgnoreCase: true, Glob: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gr.Matches) != 1 || gr.Matches[0].Path != "src/main.go" || gr.Matches[0].Line != 2 {
		t.Fatalf("grep *.go = %+v", gr)
	}

	// Grep on single file works too.
	grFile, err := Grep(filepath.Join(dir, "README.md"), protocol.FileGrepParams{Pattern: "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(grFile.Matches) != 1 || grFile.Matches[0].Path != "README.md" || grFile.Matches[0].Line != 2 {
		t.Fatalf("grep single file = %+v", grFile)
	}
}
