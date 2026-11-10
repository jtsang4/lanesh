package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jtsang4/lanesh/internal/protocol"
)

type uploadStep struct {
	query  string
	body   string
	status int
}

func TestTransferUpload(t *testing.T) {
	// Parts hold 6 bytes; every part but the final one is full.
	tests := []struct {
		name     string
		maxBytes int64
		steps    []uploadStep
		want     string // stream the downloader reads; "" when it fails
	}{
		{
			name:  "whole stream in one request",
			steps: []uploadStep{{"", "hello world", http.StatusNoContent}},
			want:  "hello world",
		},
		{
			name: "parts in order",
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1", "world!", http.StatusNoContent},
				{"?part=2&final=1", "?", http.StatusNoContent},
			},
			want: "hello world!?",
		},
		{
			name: "data in the final part",
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1&final=1", "world", http.StatusNoContent},
			},
			want: "hello world",
		},
		{
			name: "skipped part",
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=2", "world!", http.StatusConflict},
			},
		},
		{
			name: "part sent again",
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1&final=1", "world", http.StatusNoContent},
				{"?part=1&final=1", "world", http.StatusNoContent},
			},
			want: "hello world",
		},
		{
			name: "earlier part sent again",
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1", "world!", http.StatusNoContent},
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=2&final=1", "!", http.StatusNoContent},
			},
			want: "hello world!!",
		},
		{
			name: "short part that is not final",
			steps: []uploadStep{
				{"?part=0", "hel", http.StatusAccepted},
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1&final=1", "x", http.StatusNoContent},
			},
			want: "hello x",
		},
		{
			name: "empty final part",
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1&final=1", "", http.StatusAccepted},
				{"?part=1&final=1", "world", http.StatusNoContent},
			},
			want: "hello world",
		},
		{
			name:  "part too large",
			steps: []uploadStep{{"?part=0", "hello w", http.StatusRequestEntityTooLarge}},
		},
		{
			name: "whole stream after parts",
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"", "world", http.StatusConflict},
			},
		},
		{
			name:  "invalid part",
			steps: []uploadStep{{"?part=x", "hello", http.StatusBadRequest}},
		},
		{
			name: "part after the end",
			steps: []uploadStep{
				{"?part=0&final=1", "hello", http.StatusNoContent},
				{"?part=1", "more!!", http.StatusConflict},
			},
			want: "hello",
		},
		{
			name:     "limit across parts",
			maxBytes: 8,
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1", "world!", http.StatusRequestEntityTooLarge},
			},
		},
		{
			name:     "limit exactly reached",
			maxBytes: 11,
			steps: []uploadStep{
				{"?part=0", "hello ", http.StatusNoContent},
				{"?part=1&final=1", "world", http.StatusNoContent},
			},
			want: "hello world",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf := newTransfers(tt.maxBytes, 6)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /transfers/{id}/upload", tf.upload)
			mux.HandleFunc("GET /transfers/{id}/download", tf.download)
			ts := httptest.NewServer(mux)
			defer ts.Close()
			id, ticket, _ := tf.create("ln_test")
			defer tf.remove(id)

			type result struct {
				body string
				err  error
			}
			got := make(chan result, 1)
			go func() {
				req, _ := http.NewRequest(http.MethodGet, ts.URL+"/transfers/"+id+"/download", nil)
				req.Header.Set("Authorization", "Bearer "+ticket)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					got <- result{err: err}
					return
				}
				defer resp.Body.Close()
				b, err := io.ReadAll(resp.Body)
				got <- result{string(b), err}
			}()

			for i, st := range tt.steps {
				req, _ := http.NewRequest(http.MethodPost, ts.URL+"/transfers/"+id+"/upload"+st.query, strings.NewReader(st.body))
				req.Header.Set("Authorization", "Bearer "+ticket)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != st.status {
					t.Fatalf("step %d (%s): status %d, want %d", i, st.query, resp.StatusCode, st.status)
				}
			}
			if tt.want == "" {
				tf.cancel(id, sideDestination, io.ErrUnexpectedEOF)
			}
			r := <-got
			switch {
			case tt.want != "" && (r.err != nil || r.body != tt.want):
				t.Fatalf("download = %q, %v; want %q", r.body, r.err, tt.want)
			case tt.want == "" && r.err == nil:
				t.Fatalf("download succeeded with %q; want an error", r.body)
			}
		})
	}
}

func TestTransferUploadWrongTicket(t *testing.T) {
	tf := newTransfers(0, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transfers/{id}/upload", tf.upload)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	id, _, _ := tf.create("ln_test")
	defer tf.remove(id)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/transfers/"+id+"/upload?part=0", bytes.NewReader([]byte("x")))
	req.Header.Set("Authorization", "Bearer lst_wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}

func TestTransferOptions(t *testing.T) {
	current := protocol.Hello{Features: protocol.Features()}
	old := protocol.Hello{}
	partsOnly := protocol.Hello{Features: []string{protocol.FeatureTransferParts}}
	tests := []struct {
		name     string
		from, to protocol.Hello
		maxBytes int64
		noGzip   bool
		parts    int64
		encoding string
	}{
		{"both current", current, current, 0, false, 1024, protocol.EncodingGzip},
		{"old sender", old, current, 0, false, 0, ""},
		{"old receiver", current, old, 0, false, 1024, ""},
		{"receiver without gzip", current, partsOnly, 0, false, 1024, ""},
		{"transfer limit set", current, current, 1 << 20, false, 1024, ""},
		{"gzip turned off", current, current, 0, true, 1024, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts, enc := transferOptions(tt.from, tt.to, 1024, tt.maxBytes, !tt.noGzip)
			if parts != tt.parts || enc != tt.encoding {
				t.Fatalf("got (%d, %q), want (%d, %q)", parts, enc, tt.parts, tt.encoding)
			}
		})
	}
}

// failingReader returns data, then fails, like a connection that breaks
// while a part is sent.
type failingReader struct{ data *strings.Reader }

func (f failingReader) Read(p []byte) (int, error) {
	if f.data.Len() == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return f.data.Read(p)
}

// A part whose request breaks midway leaves the stream intact, so the
// source can send it again.
func TestTransferPartRetry(t *testing.T) {
	tf := newTransfers(0, 6)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transfers/{id}/upload", tf.upload)
	mux.HandleFunc("GET /transfers/{id}/download", tf.download)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	id, ticket, _ := tf.create("ln_test")
	defer tf.remove(id)
	post := func(query string, body io.Reader, size int64) (int, error) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/transfers/"+id+"/upload"+query, body)
		req.ContentLength = size
		req.Header.Set("Authorization", "Bearer "+ticket)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	if code, err := post("?part=0", strings.NewReader("hello "), 6); err != nil || code != http.StatusNoContent {
		t.Fatalf("part 0: %d %v", code, err)
	}
	if _, err := post("?part=1&final=1", failingReader{strings.NewReader("wo")}, 5); err == nil {
		t.Fatal("the broken request succeeded")
	}
	if code, err := post("?part=1&final=1", strings.NewReader("world"), 5); err != nil || code != http.StatusNoContent {
		t.Fatalf("part 1 again: %d %v", code, err)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/transfers/"+id+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+ticket)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, err := io.ReadAll(resp.Body); err != nil || string(b) != "hello world" {
		t.Fatalf("download = %q, %v", b, err)
	}
}

func TestTransferPartBytesBounds(t *testing.T) {
	tests := []struct {
		name    string
		bytes   int64
		wantErr bool
	}{
		{"default", 0, false},
		{"minimum", MinTransferPartBytes, false},
		{"maximum", MaxTransferPartBytes, false},
		{"too small", 1000, true},
		{"too large", MaxTransferPartBytes + 1, true},
		{"negative", -1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, err := New(context.Background(), Config{DataDir: t.TempDir(), AdminToken: testAdminToken, TransferPartBytes: tt.bytes})
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, want error %v", err, tt.wantErr)
			}
			if s != nil {
				_ = s.Close()
			}
		})
	}
}

// A stray request after the final part must not abort a finished upload
// whose bytes are still buffered for the downloader.
func TestTransferStrayRequestAfterEnd(t *testing.T) {
	tf := newTransfers(0, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transfers/{id}/upload", tf.upload)
	mux.HandleFunc("GET /transfers/{id}/download", tf.download)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	id, ticket, _ := tf.create("ln_test")
	defer tf.remove(id)
	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+"/transfers/"+id+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ticket)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return -1, err.Error()
		}
		return resp.StatusCode, string(b)
	}
	if code, _ := do(http.MethodPost, "/upload?part=0&final=1", "hello world"); code != http.StatusNoContent {
		t.Fatalf("final part: %d", code)
	}
	if code, _ := do(http.MethodPost, "/upload?part=1", strings.Repeat("x", 64)); code != http.StatusConflict {
		t.Fatalf("stray part: %d, want 409", code)
	}
	if code, body := do(http.MethodGet, "/download", ""); code != http.StatusOK || body != "hello world" {
		t.Fatalf("download = %d %q", code, body)
	}
}

// harnessTransfer serves one transfer and returns helpers to upload and
// download it.
func harnessTransfer(t *testing.T, tf *transfers) (id string, post func(query, body string) (int, string), download func() (string, error)) {
	t.Helper()
	id, postBody, download := harnessTransferBody(t, tf)
	post = func(query, body string) (int, string) {
		return postBody(query, strings.NewReader(body), int64(len(body)))
	}
	return id, post, download
}

// harnessTransferBody is harnessTransfer with uploads from any reader.
func harnessTransferBody(t *testing.T, tf *transfers) (id string, post func(query string, body io.Reader, size int64) (int, string), download func() (string, error)) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transfers/{id}/upload", tf.upload)
	mux.HandleFunc("GET /transfers/{id}/download", tf.download)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	id, ticket, _ := tf.create("ln_test")
	t.Cleanup(func() { tf.remove(id) })
	post = func(query string, body io.Reader, size int64) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/transfers/"+id+"/upload"+query, body)
		req.ContentLength = size
		req.Header.Set("Authorization", "Bearer "+ticket)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	download = func() (string, error) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/transfers/"+id+"/download", nil)
		req.Header.Set("Authorization", "Bearer "+ticket)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	}
	return id, post, download
}

// The queue holds two parts. A wait request reports when the next part
// fits; a part that does not fit is not taken (202) and goes through once
// the downloader caught up.
func TestTransferBusy(t *testing.T) {
	tf := newTransfers(0, 64)
	tf.partWait = 20 * time.Millisecond
	_, post, download := harnessTransfer(t, tf)
	part := strings.Repeat("a", 64)
	for i, st := range []struct{ query, body string }{
		{"?part=0&wait=1", ""}, {"?part=0", part}, {"?part=1&wait=1", ""}, {"?part=1", part},
	} {
		if code, body := post(st.query, st.body); code != http.StatusNoContent {
			t.Fatalf("step %d (%s): %d %s", i, st.query, code, body)
		}
	}
	if code, _ := post("?part=2&final=1&wait=1", ""); code != http.StatusAccepted {
		t.Fatalf("wait with a full queue: %d, want 202", code)
	}
	if code, _ := post("?part=2&final=1", "b"); code != http.StatusAccepted {
		t.Fatalf("part with a full queue: %d, want 202", code)
	}
	got := make(chan string, 1)
	go func() {
		s, _ := download()
		got <- s
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body := post("?part=2&final=1&wait=1", "")
		if code == http.StatusNoContent {
			break
		}
		if code != http.StatusAccepted || time.Now().After(deadline) {
			t.Fatalf("waiting for room: %d %s", code, body)
		}
	}
	if code, body := post("?part=2&final=1", "b"); code != http.StatusNoContent {
		t.Fatalf("part 2 after the wait: %d %s", code, body)
	}
	if s := <-got; s != part+part+"b" {
		t.Fatalf("download = %d bytes", len(s))
	}
}

// An upload in one request from an older daemon that breaks off fails the
// transfer instead of ending the stream early.
func TestTransferWholeUploadCutOff(t *testing.T) {
	tf := newTransfers(0, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transfers/{id}/upload", tf.upload)
	mux.HandleFunc("GET /transfers/{id}/download", tf.download)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	id, ticket, _ := tf.create("ln_test")
	defer tf.remove(id)
	got := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/transfers/"+id+"/download", nil)
		req.Header.Set("Authorization", "Bearer "+ticket)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- err
			return
		}
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		got <- err
	}()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/transfers/"+id+"/upload", failingReader{strings.NewReader("hello")})
	req.ContentLength = 100
	req.Header.Set("Authorization", "Bearer "+ticket)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	if tf.uploaded(context.Background(), id) {
		t.Fatal("a cut-off upload counts as complete")
	}
	if err := <-got; err == nil {
		t.Fatal("the download ended cleanly after a cut-off upload")
	}
}

// Once a transfer failed, a resent part reports the failure instead of
// being acknowledged.
func TestTransferResendAfterFailure(t *testing.T) {
	tf := newTransfers(0, 64)
	id, post, _ := harnessTransfer(t, tf)
	full := strings.Repeat("a", 64)
	if code, _ := post("?part=0", full); code != http.StatusNoContent {
		t.Fatalf("part 0: %d", code)
	}
	tf.cancel(id, sideDestination, errors.New("the destination failed"))
	code, body := post("?part=0", full)
	if code != http.StatusBadGateway || !strings.Contains(body, "the destination failed") || strings.Contains(body, `"retry":true`) {
		t.Fatalf("resend after failure: %d %s", code, body)
	}
}

// uploaded tells whether the end of the stream arrived, which the server
// checks when the source reports success.
func TestTransferUploaded(t *testing.T) {
	tf := newTransfers(0, 3)
	tf.partWait = 20 * time.Millisecond
	id, post, download := harnessTransfer(t, tf)
	if code, _ := post("?part=0", "hel"); code != http.StatusNoContent {
		t.Fatalf("part 0: %d", code)
	}
	if tf.uploaded(context.Background(), id) {
		t.Fatal("uploaded() = true before the final part")
	}
	if code, _ := post("?part=1&final=1", "lo"); code != http.StatusNoContent {
		t.Fatalf("final part: %d", code)
	}
	if !tf.uploaded(context.Background(), id) {
		t.Fatal("uploaded() = false after the final part")
	}
	if s, err := download(); err != nil || s != "hello" {
		t.Fatalf("download = %q, %v", s, err)
	}
}

// The download's headers go out before any data, so that a proxy does not
// time out while the first part uploads.
func TestTransferDownloadHeadersFirst(t *testing.T) {
	tf := newTransfers(0, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /transfers/{id}/download", tf.download)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	id, ticket, _ := tf.create("ln_test")
	defer tf.remove(id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/transfers/"+id+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+ticket)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("no response headers before any data: %v", err)
	}
	_ = resp.Body.Close()
}

// A wait request for a part the server already has returns at once, even
// with a full queue.
func TestTransferWaitForResend(t *testing.T) {
	tf := newTransfers(0, 64)
	tf.partWait = 5 * time.Second
	_, post, _ := harnessTransfer(t, tf)
	part := strings.Repeat("a", 64)
	for _, q := range []string{"?part=0", "?part=1"} {
		if code, _ := post(q, part); code != http.StatusNoContent {
			t.Fatalf("%s: %d", q, code)
		}
	}
	start := time.Now()
	if code, _ := post("?part=1&wait=1", ""); code != http.StatusNoContent || time.Since(start) > time.Second {
		t.Fatalf("wait for a resend: %d after %s", code, time.Since(start))
	}
	if code, _ := post("?part=1", part); code != http.StatusNoContent {
		t.Fatalf("resend: %d", code)
	}
}

// Cut-off part bodies are answered like "busy" so that the source resends
// them, but only up to maxPartReadFailures times in a row.
func TestTransferCutOffPartsLimit(t *testing.T) {
	tf := newTransfers(0, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transfers/{id}/upload", tf.upload)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	id, ticket, _ := tf.create("ln_test")
	defer tf.remove(id)
	tr := tf.get(id)
	for i := 1; i <= maxPartReadFailures; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/transfers/"+id+"/upload?part=0", failingReader{strings.NewReader("he")})
		req.ContentLength = 5
		req.Header.Set("Authorization", "Bearer "+ticket)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
		// The server handles the broken request after the client gave up.
		deadline := time.Now().Add(5 * time.Second)
		handled := func() bool {
			if i == maxPartReadFailures {
				return tr.buf.failed() != nil
			}
			return int(tr.readFailures.Load()) >= i || tr.buf.failed() != nil
		}
		for !handled() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if failed := tr.buf.failed() != nil; failed != (i == maxPartReadFailures) {
			t.Fatalf("after %d cut-off parts: failed = %v", i, failed)
		}
	}
}

// Only one part body per transfer is read at a time; a concurrent request,
// such as a resend racing its original, waits a while, then is told to come
// back (202).
func TestTransferOnePartBodyAtATime(t *testing.T) {
	tf := newTransfers(0, 64)
	tf.partWait = 20 * time.Millisecond
	_, post, download := harnessTransferBody(t, tf)

	// A slow request whose body is still arriving holds the read slot.
	pr, pw := io.Pipe()
	slow := make(chan int, 1)
	go func() {
		code, _ := post("?part=0&final=1", pr, 5)
		slow <- code
	}()
	_, _ = pw.Write([]byte("he"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, _ := post("?part=0&final=1", strings.NewReader("hello"), 5)
		if code == http.StatusAccepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("concurrent part: %d, want 202", code)
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, _ = pw.Write([]byte("llo"))
	_ = pw.Close()
	if code := <-slow; code != http.StatusNoContent {
		t.Fatalf("slow part: %d", code)
	}
	if s, err := download(); err != nil || s != "hello" {
		t.Fatalf("download = %q, %v", s, err)
	}
}

// uploaded waits for a final part whose body is still arriving.
func TestTransferUploadedWaitsForFinalPart(t *testing.T) {
	tf := newTransfers(0, 64)
	id, post, _ := harnessTransferBody(t, tf)
	pr, pw := io.Pipe()
	go func() { _, _ = post("?part=0&final=1", pr, 5) }()
	_, _ = pw.Write([]byte("he"))
	// Wait until the server holds the part body.
	deadline := time.Now().Add(5 * time.Second)
	for len(tf.get(id).reading) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got := make(chan bool, 1)
	go func() { got <- tf.uploaded(context.Background(), id) }()
	time.Sleep(20 * time.Millisecond)
	_, _ = pw.Write([]byte("llo"))
	_ = pw.Close()
	if !<-got {
		t.Fatal("uploaded() did not wait for the final part")
	}
}

// A large part that is turned away while another one is read gets a clean
// 202, not a connection reset.
func TestTransferBusyLargePartNoReset(t *testing.T) {
	const size = 1 << 20
	tf := newTransfers(0, size)
	tf.partWait = 20 * time.Millisecond
	_, post, _ := harnessTransferBody(t, tf)
	pr, pw := io.Pipe()
	slow := make(chan int, 1)
	go func() {
		code, _ := post("?part=0", pr, size)
		slow <- code
	}()
	_, _ = pw.Write([]byte("x"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body := post("?part=0", bytes.NewReader(make([]byte, size)), size)
		if code == http.StatusAccepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("concurrent large part: %d %s, want 202", code, body)
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, _ = pw.Write(make([]byte, size-1))
	_ = pw.Close()
	if code := <-slow; code != http.StatusNoContent {
		t.Fatalf("slow part: %d", code)
	}
}

func TestTransferLaneLimit(t *testing.T) {
	tf := newTransfers(0, 64)
	for i := range maxLaneTransfers {
		id, _, err := tf.create("ln_a")
		if err != nil {
			t.Fatalf("transfer %d: %v", i, err)
		}
		defer tf.remove(id)
	}
	if _, _, err := tf.create("ln_a"); err == nil {
		t.Fatal("a transfer beyond the lane's limit was created")
	}
	id, _, err := tf.create("ln_b")
	if err != nil {
		t.Fatalf("another lane is limited too: %v", err)
	}
	tf.remove(id)
}

// uploaded waits a while for a final part that has not reached the server
// yet, such as one a proxy still holds.
func TestTransferUploadedWaitsForLateFinalPart(t *testing.T) {
	tf := newTransfers(0, 64)
	tf.partWait = 5 * time.Second
	id, post, _ := harnessTransfer(t, tf)
	got := make(chan bool, 1)
	go func() { got <- tf.uploaded(context.Background(), id) }()
	time.Sleep(50 * time.Millisecond)
	if code, _ := post("?part=0&final=1", "hello"); code != http.StatusNoContent {
		t.Fatalf("final part: %d", code)
	}
	if !<-got {
		t.Fatal("uploaded() missed a late final part")
	}

	// Without one it gives up after the wait.
	tf.partWait = 20 * time.Millisecond
	id2, _, _ := harnessTransfer(t, tf)
	if tf.uploaded(context.Background(), id2) {
		t.Fatal("uploaded() = true without a final part")
	}
}
