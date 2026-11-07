package files

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Stats summarizes a packed or unpacked stream.
type Stats struct {
	Files int
	Bytes int64 // bytes of the tar stream
	SHA   string
}

type countingHash struct {
	h hash.Hash
	n int64
}

func (c *countingHash) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return c.h.Write(p)
}

// Pack writes src (a file or directory) to w as a tar stream. The root is
// stored as "." and directory contents as slash-separated relative paths.
// Symbolic links are skipped.
func Pack(w io.Writer, src string) (Stats, error) {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Stats{}, fmt.Errorf("%s: %w", src, ErrNotFound)
		}
		return Stats{}, err
	}
	ch := &countingHash{h: sha256.New()}
	tw := tar.NewWriter(io.MultiWriter(w, ch))
	files := 0

	add := func(p, name string, fi fs.FileInfo) error {
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if fi.IsDir() && name != "." {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p) //nolint:gosec // The source path was validated by the caller.
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		files++
		return nil
	}

	if !info.IsDir() {
		err = add(src, ".", info)
	} else {
		err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if !fi.IsDir() && !fi.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			return add(p, filepath.ToSlash(rel), fi)
		})
	}
	if err != nil {
		return Stats{}, err
	}
	if err := tw.Close(); err != nil {
		return Stats{}, err
	}
	return Stats{Files: files, Bytes: ch.n, SHA: hex.EncodeToString(ch.h.Sum(nil))}, nil
}

// Unpack reads a stream produced by Pack and materializes it at dst. Content
// is first extracted next to dst and then moved into place, so a failed
// transfer never leaves a partial result. Existing dst requires overwrite.
func Unpack(r io.Reader, dst string, overwrite bool) (Stats, error) {
	if _, err := os.Lstat(dst); err == nil && !overwrite {
		return Stats{}, fmt.Errorf("%s: %w (set overwrite to replace it)", dst, ErrExists)
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // Directories follow the user's umask.
		return Stats{}, err
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(dst)+".lanesh-*")
	if err != nil {
		return Stats{}, err
	}
	defer os.RemoveAll(stage) //nolint:errcheck // Best-effort cleanup.
	root := filepath.Join(stage, "root")

	ch := &countingHash{h: sha256.New()}
	tr := tar.NewReader(io.TeeReader(r, ch))
	files := 0
	sawRoot := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Stats{}, fmt.Errorf("read transfer stream: %w", err)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		var target string
		if name == "." {
			target, sawRoot = root, true
		} else {
			clean := path.Clean(name)
			if !filepath.IsLocal(filepath.FromSlash(clean)) {
				return Stats{}, fmt.Errorf("unsafe path %q in transfer stream", hdr.Name)
			}
			target = filepath.Join(root, filepath.FromSlash(clean))
		}
		mode := fs.FileMode(hdr.Mode) & fs.ModePerm //nolint:gosec // Tar modes are small.
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil { //nolint:gosec // Mode applied below.
				return Stats{}, err
			}
			_ = os.Chmod(target, mode|0o700)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // See above.
				return Stats{}, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600) //nolint:gosec // Target is inside the staging dir.
			if err != nil {
				return Stats{}, err
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // Size is bounded by the server's transfer limit.
				_ = f.Close()
				return Stats{}, err
			}
			if err := f.Close(); err != nil {
				return Stats{}, err
			}
			files++
		default:
			// Links and special files are not transferred.
		}
	}
	// Drain trailing padding so the hash covers the whole stream.
	_, _ = io.Copy(io.Discard, io.TeeReader(r, ch))
	if !sawRoot {
		return Stats{}, errors.New("transfer stream is empty")
	}
	if err := replace(root, dst); err != nil {
		return Stats{}, err
	}
	return Stats{Files: files, Bytes: ch.n, SHA: hex.EncodeToString(ch.h.Sum(nil))}, nil
}
