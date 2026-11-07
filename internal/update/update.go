// Package update replaces the managed lanesh binary with a release from GitHub.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultSource is where release assets are downloaded from.
const DefaultSource = "https://github.com/jtsang4/lanesh/releases/download"

const latestURL = "https://github.com/jtsang4/lanesh/releases/latest"

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

// IsRelease reports whether v is a published release version (not a dev or snapshot build).
func IsRelease(v string) bool {
	m := semver.FindStringSubmatch(v)
	return m != nil && (m[4] == "" || strings.HasPrefix(m[4], "rc."))
}

// Compare orders semantic versions; unparseable versions sort lowest.
func Compare(a, b string) int {
	ma, mb := semver.FindStringSubmatch(a), semver.FindStringSubmatch(b)
	switch {
	case ma == nil && mb == nil:
		return 0
	case ma == nil:
		return -1
	case mb == nil:
		return 1
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ma[4] == mb[4]:
		return 0
	case ma[4] == "":
		return 1
	case mb[4] == "":
		return -1
	case ma[4] < mb[4]:
		return -1
	default:
		return 1
	}
}

// AssetName is the raw binary asset for a version on this platform.
func AssetName(version string) string {
	name := fmt.Sprintf("lanesh_%s_%s_%s", strings.TrimPrefix(version, "v"), runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

var client = &http.Client{Timeout: 10 * time.Minute}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

// Latest returns the latest release version by following GitHub's redirect.
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, nil)
	if err != nil {
		return "", err
	}
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/v")
	if i < 0 {
		return "", errors.New("no release has been published yet")
	}
	return loc[i+len("/tag/v"):], nil
}

// Apply downloads version from source, verifies its checksum and atomically
// replaces binary.
func Apply(ctx context.Context, source, version, binary string) error {
	if source == "" {
		source = DefaultSource
	}
	version = strings.TrimPrefix(version, "v")
	base := strings.TrimRight(source, "/") + "/v" + version + "/"
	asset := AssetName(version)

	sums, err := get(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == asset {
			expected = strings.ToLower(f[0])
		}
	}
	if expected == "" {
		return fmt.Errorf("no checksum for %s", asset)
	}
	data, err := get(ctx, base+asset)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expected {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return Install(data, binary)
}

// Install atomically writes data to binary. On Windows the running executable
// is renamed out of the way first.
func Install(data []byte, binary string) error {
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil { //nolint:gosec // Binary directory.
		return err
	}
	tmp := binary + ".download"
	if err := os.WriteFile(tmp, data, 0o755); err != nil { //nolint:gosec // Executables need the execute bit.
		return err
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(binary); err == nil {
			_ = os.Remove(binary + ".old")
			if err := os.Rename(binary, binary+".old"); err != nil {
				return err
			}
		}
	}
	return os.Rename(tmp, binary)
}

// Cleanup removes leftovers of a previous Windows update.
func Cleanup(binary string) {
	_ = os.Remove(binary + ".old")
}

// SameFile reports whether the running executable is binary.
func SameFile(binary string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	a, err1 := os.Stat(exe)
	b, err2 := os.Stat(binary)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}
