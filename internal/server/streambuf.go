package server

import (
	"context"
	"errors"
	"io"
	"sync"
)

var errStreamClosed = errors.New("stream closed")

// streamBuffer forwards an upload to the downloader through a bounded queue
// of byte slices. An upload part enters the queue as a whole once there is
// room, so that its request finishes, and the next part uploads, while the
// downloader still reads the previous one.
type streamBuffer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte
	size   int
	limit  int
	closed bool  // the writer finished; the reader drains, then gets io.EOF
	err    error // the stream failed; both sides get err at once
}

func newStreamBuffer(limit int) *streamBuffer {
	b := &streamBuffer{limit: max(limit, 1)}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// wait blocks until n more bytes fit, or the buffer is empty, or the stream
// ended. It returns ctx.Err() if ctx ends first. The caller holds b.mu.
func (b *streamBuffer) wait(ctx context.Context, n int) error {
	err := b.sleep(ctx, func() bool { return b.err == nil && !b.closed && b.size > 0 && b.size+n > b.limit })
	if err != nil && b.err == nil {
		return err
	}
	switch {
	case b.err != nil:
		return b.err
	case b.closed:
		return errStreamClosed
	}
	return nil
}

// sleep waits while blocked reports true, or until ctx ends. The caller
// holds b.mu. It watches ctx only when it has to wait, which keeps reads of
// queued data cheap.
func (b *streamBuffer) sleep(ctx context.Context, blocked func() bool) error {
	if !blocked() {
		return nil
	}
	stop := context.AfterFunc(ctx, func() {
		b.mu.Lock()
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	defer stop()
	for blocked() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.cond.Wait()
	}
	return nil
}

// waitRoom blocks until n more bytes fit; see wait.
func (b *streamBuffer) waitRoom(ctx context.Context, n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wait(ctx, n)
}

// push queues data, which the buffer then owns. It waits until data fits,
// or the buffer is empty, and returns ctx.Err() if ctx ends first, leaving
// the stream unchanged.
func (b *streamBuffer) push(ctx context.Context, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.wait(ctx, len(data)); err != nil {
		return err
	}
	if len(data) > 0 {
		b.chunks = append(b.chunks, data)
		b.size += len(data)
		b.cond.Broadcast()
	}
	return nil
}

// reader reads the stream: it blocks until data is queued, the stream ends
// or ctx ends.
func (b *streamBuffer) reader(ctx context.Context) io.Reader {
	return readerFunc(func(p []byte) (int, error) { return b.read(ctx, p) })
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func (b *streamBuffer) read(ctx context.Context, p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.sleep(ctx, func() bool { return b.size == 0 && b.err == nil && !b.closed })
	if err != nil && b.err == nil {
		return 0, err
	}
	if b.err != nil {
		return 0, b.err
	}
	if b.size == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && len(b.chunks) > 0 {
		k := copy(p[n:], b.chunks[0])
		n += k
		if k == len(b.chunks[0]) {
			b.chunks[0] = nil
			b.chunks = b.chunks[1:]
		} else {
			b.chunks[0] = b.chunks[0][k:]
		}
	}
	b.size -= n
	b.cond.Broadcast()
	return n, nil
}

// close ends the stream normally: the reader gets the queued bytes, then
// io.EOF.
func (b *streamBuffer) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.cond.Broadcast()
}

// done reports whether the writer finished normally.
func (b *streamBuffer) done() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// waitDone waits until the writer finished normally and reports whether it
// did; it gives up when the stream fails or ctx ends.
func (b *streamBuffer) waitDone(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.sleep(ctx, func() bool { return !b.closed && b.err == nil })
	return b.closed && b.err == nil
}

// failed returns the error the stream failed with, if any.
func (b *streamBuffer) failed() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// fail aborts the stream and drops the queued bytes. The first error wins;
// first reports whether this call set it.
func (b *streamBuffer) fail(err error) (first bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	first = b.err == nil
	if first {
		b.err = err
	}
	b.chunks, b.size = nil, 0
	b.cond.Broadcast()
	return first
}
