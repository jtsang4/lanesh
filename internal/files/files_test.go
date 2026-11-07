package files

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jtsang4/lanesh/internal/protocol"
)

func TestExpand(t *testing.T) {
	home := t.TempDir()
	for in, want := range map[string]string{
		"~":           home,
		"~/a/b":       filepath.Join(home, "a", "b"),
		"rel/x":       filepath.Join(home, "rel", "x"),
		home + "/../": filepath.Dir(home),
	} {
		got, err := Expand(in, home)
		if err != nil || got != want {
			t.Errorf("Expand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Expand(" ", home); err == nil {
		t.Error("empty path must fail")
	}
}

func TestWriteAndReadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.txt")
	if _, err := WriteFile(p, protocol.FileWriteParams{Content: "hello"}); err == nil {
		t.Fatal("missing parent without create_dirs must fail")
	}
	if _, err := WriteFile(p, protocol.FileWriteParams{Content: "hello", CreateDirs: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(p, protocol.FileWriteParams{Content: "x"}); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite without flag: %v", err)
	}
	if _, err := WriteFile(p, protocol.FileWriteParams{Content: "aGk=", Encoding: protocol.EncodingBase64, Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	res, err := ReadFile(p, 0, 0, "")
	if err != nil || res.Content != "hi" || res.Encoding != protocol.EncodingUTF8 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, err := ReadFile(filepath.Join(dir, "missing"), 0, 0, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestPackUnpackDirectory(t *testing.T) {
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "a", "b"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o600)
	_ = os.WriteFile(filepath.Join(src, "a", "b", "deep.bin"), []byte{0, 1, 2}, 0o600)
	_ = os.MkdirAll(filepath.Join(src, "empty"), 0o755)

	var buf bytes.Buffer
	sent, err := Pack(&buf, src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	got, err := Unpack(bytes.NewReader(buf.Bytes()), dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA != sent.SHA || got.Bytes != 6 || sent.Bytes != 6 || got.Files != 2 || sent.Files != 2 {
		t.Fatalf("sent %+v got %+v", sent, got)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "a", "b", "deep.bin")); !bytes.Equal(b, []byte{0, 1, 2}) {
		t.Fatalf("deep.bin = %v", b)
	}
	if fi, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Fatal("empty dir missing")
	}
	if _, err := Unpack(bytes.NewReader(buf.Bytes()), dst, false); !errors.Is(err, ErrExists) {
		t.Fatalf("existing dst: %v", err)
	}
	if _, err := Unpack(bytes.NewReader(buf.Bytes()), dst, true); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
}

func TestPackUnpackFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "f.txt")
	_ = os.WriteFile(src, []byte("content"), 0o600)
	var buf bytes.Buffer
	if _, err := Pack(&buf, src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "nested", "g.txt")
	if _, err := Unpack(&buf, dst, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "content" {
		t.Fatalf("dst = %q", b)
	}
}

func TestUnpackRejectsTruncatedStream(t *testing.T) {
	src := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o600)
	// A stream that ends cleanly at an entry boundary but lacks the end marker.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "a.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("a"))
	_ = tw.Close()

	dst := filepath.Join(t.TempDir(), "dst")
	_ = os.MkdirAll(dst, 0o755)
	_ = os.WriteFile(filepath.Join(dst, "precious.txt"), []byte("keep"), 0o600)
	if _, err := Unpack(&buf, dst, true); err == nil {
		t.Fatal("truncated stream must be rejected")
	}
	if b, err := os.ReadFile(filepath.Join(dst, "precious.txt")); err != nil || string(b) != "keep" {
		t.Fatal("existing destination must be left untouched")
	}
}

func TestUnpackRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	dst := filepath.Join(t.TempDir(), "out")
	if _, err := Unpack(&buf, dst, false); err == nil {
		t.Fatal("traversal must be rejected")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("failed unpack must not create dst")
	}
}
