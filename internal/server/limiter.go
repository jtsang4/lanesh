package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter is a fixed-window rate limiter keyed by string.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	n     int
}

func newLimiter(maxHits int, d time.Duration) *limiter {
	return &limiter{max: maxHits, window: d, hits: map[string]*window{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) >= maxTracked {
		for k, w := range l.hits {
			if now.Sub(w.start) > l.window {
				delete(l.hits, k)
			}
		}
		if len(l.hits) >= maxTracked {
			return false // under a flood, refuse rather than grow without bound
		}
	}
	w := l.hits[key]
	if w == nil || now.Sub(w.start) > l.window {
		l.hits[key] = &window{start: now, n: 1}
		return true
	}
	w.n++
	return w.n <= l.max
}

const maxTracked = 10_000

// clientIP returns the client address. Proxy headers are only honoured when
// the request comes from a loopback or private address, i.e. from a reverse
// proxy in front of Lanesh; otherwise they could be forged to evade limits.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return host
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1]) // added by the nearest proxy
	}
	if xr := r.Header.Get("X-Real-Ip"); xr != "" {
		return xr
	}
	return host
}
