package server

import (
	"io"
	"mime"
	"net/http"
	"sync"
	"time"
)

// DefaultSSEHeartbeat is how often an idle MCP event stream carries a
// heartbeat. Like the daemon heartbeat, it stays below the idle timeout of
// common reverse proxies (Cloudflare closes a stream after about 100 s
// without data).
const DefaultSSEHeartbeat = pingInterval

// sseHeartbeat is an SSE comment line; clients ignore it.
const sseHeartbeat = ": keepalive\n\n"

// keepSSEAlive writes a heartbeat comment every interval to the event streams
// that next serves. Without it a proxy closes the standalone stream of an MCP
// session, which rarely carries messages, and clients give up on the session
// after a few reconnects without new events.
//
// Heartbeats start only once next has committed a 200 text/event-stream
// response, so they never choose a status or interleave with an event. An
// interval of zero or less means DefaultSSEHeartbeat.
func keepSSEAlive(next http.Handler, interval time.Duration) http.Handler {
	if interval <= 0 {
		interval = DefaultSSEHeartbeat
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &sseWriter{ResponseWriter: w, interval: interval}
		defer sw.stop()
		next.ServeHTTP(sw, r)
	})
}

// sseWriter serializes the writes of a handler with its heartbeats. It
// implements FlushError so that http.ResponseController flushes through it
// under the same lock.
type sseWriter struct {
	http.ResponseWriter
	interval time.Duration

	mu          sync.Mutex
	wroteHeader bool
	closed      bool
	stopCh      chan struct{} // set when heartbeats are running
	wg          sync.WaitGroup
}

func (w *sseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeHeaderLocked(code)
}

func (w *sseWriter) writeHeaderLocked(code int) {
	if !w.wroteHeader && code >= http.StatusOK {
		w.wroteHeader = true
		if code == http.StatusOK && isEventStream(w.Header().Get("Content-Type")) && !w.closed {
			w.stopCh = make(chan struct{})
			w.wg.Go(w.heartbeat)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *sseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.wroteHeader {
		w.writeHeaderLocked(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *sseWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushLocked()
}

func (w *sseWriter) Flush() { _ = w.FlushError() }

func (w *sseWriter) flushLocked() error {
	if !w.wroteHeader {
		w.writeHeaderLocked(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *sseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *sseWriter) heartbeat() {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-t.C:
			if !w.ping() {
				return
			}
		}
	}
}

// ping writes one heartbeat. It reports false once the response is over or
// the connection is gone.
func (w *sseWriter) ping() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	if _, err := io.WriteString(w.ResponseWriter, sseHeartbeat); err != nil {
		return false
	}
	return w.flushLocked() == nil
}

// stop ends the heartbeats; the response must not be written after the
// handler returns.
func (w *sseWriter) stop() {
	w.mu.Lock()
	w.closed = true
	if w.stopCh != nil {
		close(w.stopCh)
	}
	w.mu.Unlock()
	w.wg.Wait()
}

func isEventStream(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "text/event-stream"
}
