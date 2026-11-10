package daemon

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jtsang4/lanesh/internal/files"
	"github.com/jtsang4/lanesh/internal/protocol"
)

func TestUploadStream(t *testing.T) {
	tests := []struct {
		name      string
		size      int
		partBytes int64
		want      []string // the query of each request
	}{
		{"whole", 10, 0, []string{""}},
		{"one part", 10, 16, []string{"part=0&wait=1", "part=0&final=1"}},
		{"exact parts", 32, 16, []string{"part=0&wait=1", "part=0", "part=1&wait=1", "part=1&final=1"}},
		{"exactly one part", 16, 16, []string{"part=0&wait=1", "part=0&final=1"}},
		{"partial last part", 40, 16, []string{"part=0&wait=1", "part=0", "part=1&wait=1", "part=1", "part=2&wait=1", "part=2&final=1"}},
		{"empty", 0, 16, []string{"part=0&wait=1", "part=0&final=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu      sync.Mutex
				queries []string
				got     bytes.Buffer
			)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer tick" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				defer mu.Unlock()
				if tt.partBytes > 0 && (int64(len(b)) > tt.partBytes || r.ContentLength != int64(len(b))) {
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				queries = append(queries, r.URL.RawQuery)
				got.Write(b)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer ts.Close()
			data := bytes.Repeat([]byte("x"), tt.size)
			if err := uploadStream(context.Background(), ts.URL+"/up", "tick", bytes.NewReader(data), tt.partBytes); err != nil {
				t.Fatal(err)
			}
			if strings.Join(queries, " ") != strings.Join(tt.want, " ") {
				t.Fatalf("requests = %q, want %q", queries, tt.want)
			}
			if !bytes.Equal(got.Bytes(), data) {
				t.Fatalf("server received %d bytes, want %d", got.Len(), len(data))
			}
		})
	}
}

// fastRetries shortens the delays between part retries for a test.
func fastRetries(t *testing.T) {
	saved, savedBusy := partRetryDelays, busyDelay
	partRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	busyDelay = time.Millisecond
	t.Cleanup(func() { partRetryDelays, busyDelay = saved, savedBusy })
}

func TestUploadStreamRetries(t *testing.T) {
	fastRetries(t)
	const lanesh = -1 // a Lanesh JSON error with the next status
	tests := []struct {
		name     string
		waits    []int // answers to the wait requests; then 204
		parts    []int // answers to the part requests; then 204
		wantErr  bool
		wantReqs int
	}{
		{"proxy error, then success", nil, []int{http.StatusBadGateway}, false, 3},
		{"cloudflare timeout twice", nil, []int{524, 524}, false, 4},
		{"too many failures", nil, []int{502, 502, 502}, true, 4},
		{"failures count only in a row", nil, []int{502, 202, 502, 202, 502}, false, 9},
		{"lanesh error is final", nil, []int{lanesh, http.StatusBadGateway}, true, 2},
		{"proxy size limit is final", nil, []int{http.StatusRequestEntityTooLarge}, true, 2},
		{"busy waits do not use up retries", []int{202, 202, 202, 202, 202, 202}, nil, false, 8},
		{"busy part waits again", nil, []int{202, 202}, false, 6},
		{"wait fails like a part", []int{502}, nil, false, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			reqs, waits, parts := 0, 0, 0
			next := func(list []int, i *int) (int, bool) {
				defer func() { *i++ }()
				if *i >= len(list) {
					return http.StatusNoContent, false
				}
				if list[*i] == lanesh {
					*i++
					return list[*i], true
				}
				return list[*i], false
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				mu.Lock()
				defer mu.Unlock()
				reqs++
				var status int
				var fromLanesh bool
				if r.URL.Query().Get("wait") == "1" {
					status, fromLanesh = next(tt.waits, &waits)
				} else {
					status, fromLanesh = next(tt.parts, &parts)
				}
				switch {
				case fromLanesh:
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":"transfer failed"}`)
				case status >= 400:
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "<html>error</html>")
				default:
					w.WriteHeader(status)
				}
			}))
			defer ts.Close()
			err := uploadStream(context.Background(), ts.URL, "tick", strings.NewReader("data"), 1024)
			if (err != nil) != tt.wantErr || reqs != tt.wantReqs {
				t.Fatalf("err = %v after %d requests; want error %v after %d", err, reqs, tt.wantErr, tt.wantReqs)
			}
		})
	}
}

func TestUploadStreamErrors(t *testing.T) {
	fastRetries(t)
	tests := []struct {
		name        string
		partBytes   int64
		contentType string
		status      int
		body        string
		want        string
	}{
		{"lanesh error", 2, "application/json", http.StatusConflict, `{"error":"transfer failed: expected part 1, got 2"}`, "upload failed: 409 Conflict: transfer failed: expected part 1, got 2"},
		{"lanesh size limit", 2, "application/json", http.StatusRequestEntityTooLarge, `{"error":"transfer failed: the transfer exceeds the server's limit of 3 bytes"}`, "limit of 3 bytes"},
		{"proxy size limit", 2, "text/html", http.StatusRequestEntityTooLarge, "<html><center>cloudflare</center></html>", "lower LANESH_TRANSFER_PART_BYTES"},
		{"proxy size limit without parts", 0, "text/html", http.StatusRequestEntityTooLarge, "<html><center>cloudflare</center></html>", "upgrade the server so that it splits uploads"},
		{"proxy timeout", 2, "text/html", 524, "<html>timeout</html>", "a proxy in front of the Lanesh server timed out"},
		{"proxy html", 2, "text/html", http.StatusBadGateway, "<html>bad gateway</html>", "upload failed: 502 Bad Gateway"},
		{"plain text", 2, "text/plain", http.StatusBadGateway, "no upstream", "upload failed: 502 Bad Gateway: no upstream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("wait") == "1" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer ts.Close()
			err := uploadStream(context.Background(), ts.URL, "tick", strings.NewReader("data"), tt.partBytes)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "<html") {
				t.Fatalf("error quotes HTML: %v", err)
			}
			if tt.contentType == "application/json" && strings.Contains(err.Error(), "LANESH_TRANSFER_PART_BYTES") {
				t.Fatalf("a Lanesh error got a proxy hint: %v", err)
			}
		})
	}
}

func TestPackStreamGzip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	content := bytes.Repeat([]byte("compressible line\n"), 10000)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	sent, err := packStream(&buf, src, protocol.EncodingGzip)
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() >= len(content)/10 {
		t.Fatalf("compressed stream is %d bytes for %d bytes of text", buf.Len(), len(content))
	}
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	recv, err := files.Unpack(gz, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if recv.SHA != sent.SHA || recv.Files != 1 || recv.Bytes != int64(len(content)) {
		t.Fatalf("received %+v, sent %+v", recv, sent)
	}
	got, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("unpacked %d bytes, %v", len(got), err)
	}
}

func TestReadPart(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		limit   int64
		want    int
		wantEOF bool
		maxCap  int
	}{
		{"small stream, huge part", 10, 1 << 30, 10, true, 64 << 10},
		{"exactly the limit", 100 << 10, 100 << 10, 100 << 10, false, 100 << 10},
		{"more than the limit", 300 << 10, 100 << 10, 100 << 10, false, 100 << 10},
		{"empty", 0, 1 << 20, 0, true, 64 << 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readPart(bytes.NewReader(make([]byte, tt.size)), nil, tt.limit)
			if len(got) != tt.want || errors.Is(err, io.EOF) != tt.wantEOF || (err != nil && !errors.Is(err, io.EOF)) {
				t.Fatalf("read %d bytes, err %v; want %d, EOF %v", len(got), err, tt.want, tt.wantEOF)
			}
			if cap(got) > tt.maxCap {
				t.Fatalf("buffer capacity %d, want at most %d", cap(got), tt.maxCap)
			}
		})
	}
}

// When only the answer to the final part is lost, uploadStream says so, and
// transferSend reports its checksum for the server to compare.
func TestUploadStreamFinalUnconfirmed(t *testing.T) {
	fastRetries(t)
	tests := []struct {
		name        string
		status      int
		lanesh      bool
		unconfirmed bool
	}{
		{"proxy errors", http.StatusBadGateway, false, true},
		{"lanesh error", http.StatusBadGateway, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				if r.URL.Query().Get("final") != "1" || r.URL.Query().Get("wait") == "1" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if tt.lanesh {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, `{"error":"transfer failed"}`)
					return
				}
				w.WriteHeader(tt.status)
			}))
			defer ts.Close()
			err := uploadStream(context.Background(), ts.URL, "tick", strings.NewReader(strings.Repeat("x", 40)), 16)
			if err == nil || errors.Is(err, errFinalUnconfirmed) != tt.unconfirmed {
				t.Fatalf("err = %v; want unconfirmed %v", err, tt.unconfirmed)
			}
		})
	}
}

func TestPermanentNetError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"unknown host", &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}, true},
		{"dns timeout", &net.DNSError{Err: "timeout", Name: "x", IsTimeout: true}, false},
		{"untrusted certificate", fmt.Errorf("post: %w", x509.UnknownAuthorityError{}), true},
		{"wrong host certificate", x509.HostnameError{Host: "x"}, true},
		{"connection reset", errors.New("connection reset by peer"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permanentNetError(tt.err); got != tt.want {
				t.Fatalf("permanentNetError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
