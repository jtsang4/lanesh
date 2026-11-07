package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	s := String()
	if !strings.HasPrefix(s, "lanesh "+Version) {
		t.Fatalf("unexpected version string %q", s)
	}
}
