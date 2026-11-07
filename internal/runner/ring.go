package runner

import "sync"

// ring is a bounded byte buffer addressed by absolute stream offsets. When it
// is full, the oldest bytes are dropped.
type ring struct {
	mu      sync.Mutex
	buf     []byte
	cap     int
	total   int64 // bytes ever written
	dropped int64 // bytes discarded from the start
}

func newRing(capacity int) *ring { return &ring{cap: capacity} }

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	r.total += int64(len(p))
	if over := len(r.buf) - r.cap; over > 0 {
		r.buf = append(r.buf[:0], r.buf[over:]...)
		r.dropped += int64(over)
	}
	return len(p), nil
}

// ReadAt returns up to limit bytes starting at absolute offset off. If off
// points at dropped data, reading starts at the oldest retained byte.
func (r *ring) ReadAt(off int64, limit int) (data []byte, start, total, dropped int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if off < r.dropped {
		off = r.dropped
	}
	if off > r.total {
		off = r.total
	}
	i := int(off - r.dropped)
	j := len(r.buf)
	if limit > 0 && i+limit < j {
		j = i + limit
	}
	return append([]byte(nil), r.buf[i:j]...), off, r.total, r.dropped
}

// Snapshot returns all retained bytes and the total written.
func (r *ring) Snapshot() (data []byte, total, dropped int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf...), r.total, r.dropped
}
