package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareAndIsRelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"1.2.3", "1.10.0", -1}, {"v2.0.0", "1.9.9", 1},
		{"1.0.0-rc.1", "1.0.0", -1}, {"dev", "0.1.0", -1}, {"0.1.0", "dev", 1},
		{"1.0.0-rc.10", "1.0.0-rc.9", 1}, {"1.0.0-rc.1", "1.0.0-rc.1.1", -1}, {"1.0.0-alpha", "1.0.0-1", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for v, want := range map[string]bool{"1.0.0": true, "1.0.0-rc.2": true, "dev": false, "0.0.0-SNAPSHOT-abc": false} {
		if IsRelease(v) != want {
			t.Errorf("IsRelease(%q) != %v", v, want)
		}
	}
}

func TestApply(t *testing.T) {
	payload := []byte("new binary")
	sum := sha256.Sum256(payload)
	asset := AssetName("1.2.3")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.2.3/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		case "/v1.2.3/" + asset:
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	bin := filepath.Join(t.TempDir(), "bin", "lanesh")
	if err := Apply(context.Background(), srv.URL, "v1.2.3", bin); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "new binary" {
		t.Fatalf("binary = %q", got)
	}
	if err := Apply(context.Background(), srv.URL, "9.9.9", bin); err == nil {
		t.Fatal("missing release must fail")
	}
}
