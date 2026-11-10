package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestStreamBuffer(t *testing.T) {
	tests := []struct {
		name   string
		limit  int
		chunks []string
	}{
		{"fits", 64, []string{"hello ", "world"}},
		{"one chunk at a time", 6, []string{"hello ", "world", "!"}},
		{"chunk larger than the buffer", 3, []string{"hello world", "!"}},
		{"empty chunks", 8, []string{"", "a", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newStreamBuffer(tt.limit)
			got := make(chan []byte, 1)
			go func() {
				out, _ := io.ReadAll(b.reader(context.Background()))
				got <- out
			}()
			var want []byte
			for _, c := range tt.chunks {
				if err := b.push(context.Background(), []byte(c)); err != nil {
					t.Fatal(err)
				}
				want = append(want, c...)
			}
			b.close()
			if out := <-got; !bytes.Equal(out, want) {
				t.Fatalf("read %q, want %q", out, want)
			}
		})
	}
}

func TestStreamBufferWait(t *testing.T) {
	b := newStreamBuffer(8)
	ctx := context.Background()
	if err := b.push(ctx, []byte("12345")); err != nil {
		t.Fatal(err)
	}
	// No room: the push gives up when its context ends and changes nothing.
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := b.push(short, []byte("6789")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("push into a full buffer = %v, want deadline exceeded", err)
	}
	// Once the reader makes room, a waiting push goes through.
	done := make(chan error, 1)
	go func() { done <- b.push(ctx, []byte("6789")) }()
	p := make([]byte, 5)
	if n, err := b.reader(context.Background()).Read(p); n != 5 || err != nil {
		t.Fatalf("read %d, %v", n, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	b.close()
	if out, err := io.ReadAll(b.reader(context.Background())); err != nil || string(out) != "6789" {
		t.Fatalf("rest = %q, %v", out, err)
	}
}

func TestStreamBufferFail(t *testing.T) {
	boom := errors.New("boom")
	ctx := context.Background()

	// A failure reaches a reader even when bytes are still queued.
	b := newStreamBuffer(16)
	_ = b.push(ctx, []byte("partial"))
	b.fail(boom)
	if _, err := io.ReadAll(b.reader(context.Background())); !errors.Is(err, boom) {
		t.Fatalf("reader error = %v, want boom", err)
	}
	if !errors.Is(b.failed(), boom) {
		t.Fatalf("failed() = %v", b.failed())
	}

	// It unblocks a push waiting for room.
	b = newStreamBuffer(2)
	_ = b.push(ctx, []byte("ab"))
	errc := make(chan error, 1)
	go func() { errc <- b.push(ctx, []byte("cd")) }()
	time.Sleep(10 * time.Millisecond)
	b.fail(boom)
	if err := <-errc; !errors.Is(err, boom) {
		t.Fatalf("push error = %v, want boom", err)
	}

	// Pushing after a normal close fails.
	b = newStreamBuffer(4)
	b.close()
	if err := b.push(ctx, []byte("x")); !errors.Is(err, errStreamClosed) {
		t.Fatalf("push after close = %v", err)
	}
	if !b.done() {
		t.Fatal("done() = false after close")
	}
}
