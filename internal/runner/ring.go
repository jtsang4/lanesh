package runner

import "sync"

// ring is a bounded circular byte buffer addressed by absolute stream
// offsets. When it is full, the oldest bytes are overwritten.
type ring struct {
	mu      sync.Mutex
	buf     []byte // grows up to cap, then wraps
	cap     int
	head    int   // index of the oldest byte once the buffer has wrapped
	total   int64 // bytes ever written
	dropped int64 // bytes overwritten
}

func newRing(capacity int) *ring { return &ring{cap: capacity} }

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	r.total += int64(n)
	if n >= r.cap {
		// Only the last cap bytes survive.
		r.dropped += int64(len(r.buf) + n - r.cap)
		r.buf = append(r.buf[:0], p[n-r.cap:]...)
		r.head = 0
		return n, nil
	}
	if free := r.cap - len(r.buf); free > 0 {
		k := min(free, len(p))
		r.buf = append(r.buf, p[:k]...)
		p = p[k:]
	}
	for len(p) > 0 {
		k := copy(r.buf[r.head:], p)
		r.head = (r.head + k) % r.cap
		r.dropped += int64(k)
		p = p[k:]
	}
	return n, nil
}

// size returns the number of retained bytes.
func (r *ring) size() int { return len(r.buf) }

// copyOut copies retained bytes [i, j) in logical order into a new slice.
func (r *ring) copyOut(i, j int) []byte {
	out := make([]byte, 0, j-i)
	if len(r.buf) < r.cap {
		return append(out, r.buf[i:j]...)
	}
	for k := i; k < j; {
		p := (r.head + k) % r.cap
		end := min(r.cap, p+(j-k))
		out = append(out, r.buf[p:end]...)
		k += end - p
	}
	return out
}

// ReadAt returns up to limit bytes starting at absolute offset off. If off
// points at dropped data, reading starts at the oldest retained byte.
func (r *ring) ReadAt(off int64, limit int) (data []byte, start, total, dropped int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	off = max(off, r.dropped)
	off = min(off, r.total)
	i := int(off - r.dropped)
	j := r.size()
	if limit > 0 && i+limit < j {
		j = i + limit
	}
	return r.copyOut(i, j), off, r.total, r.dropped
}

// Snapshot returns all retained bytes and the total written.
func (r *ring) Snapshot() (data []byte, total, dropped int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.copyOut(0, r.size()), r.total, r.dropped
}
