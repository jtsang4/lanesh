package server

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
)

// transfers brokers streaming file transfers between two daemons. The source
// uploads and the destination downloads; bytes are piped without buffering.
type transfers struct {
	maxBytes int64
	mu       sync.Mutex
	active   map[string]*transfer
}

type transfer struct {
	ticketHash string
	pr         *io.PipeReader
	pw         *io.PipeWriter
	uploaded   chan struct{}
	once       sync.Once
	timer      *time.Timer

	mu    sync.Mutex
	sides int
}

// attach records that one side connected; the connect timeout stops once
// both sides are streaming.
func (tr *transfer) attach() {
	tr.mu.Lock()
	tr.sides++
	both := tr.sides >= 2
	tr.mu.Unlock()
	if both {
		tr.timer.Stop()
	}
}

const transferConnectTimeout = 60 * time.Second

func newTransfers(maxBytes int64) *transfers {
	return &transfers{maxBytes: maxBytes, active: map[string]*transfer{}}
}

// create registers a transfer and returns its ID and one-time ticket.
func (t *transfers) create() (id, ticket string) {
	id, ticket = auth.NewID("tr_"), auth.NewToken(auth.PrefixTicket)
	pr, pw := io.Pipe()
	tr := &transfer{ticketHash: auth.Hash(ticket), pr: pr, pw: pw, uploaded: make(chan struct{})}
	tr.timer = time.AfterFunc(transferConnectTimeout, func() {
		tr.fail(errors.New("transfer timed out waiting for both machines"))
	})
	t.mu.Lock()
	t.active[id] = tr
	t.mu.Unlock()
	return id, ticket
}

func (tr *transfer) fail(err error) {
	_ = tr.pw.CloseWithError(err)
	_ = tr.pr.CloseWithError(err)
}

func (t *transfers) remove(id string) {
	t.mu.Lock()
	tr := t.active[id]
	delete(t.active, id)
	t.mu.Unlock()
	if tr != nil {
		tr.timer.Stop()
		tr.fail(errors.New("transfer finished"))
	}
}

// cancel aborts a transfer, for example when one side failed.
func (t *transfers) cancel(id string, err error) {
	t.mu.Lock()
	tr := t.active[id]
	t.mu.Unlock()
	if tr != nil {
		tr.fail(err)
	}
}

func (t *transfers) lookup(r *http.Request) *transfer {
	t.mu.Lock()
	tr := t.active[r.PathValue("id")]
	t.mu.Unlock()
	if tr == nil || !auth.Equal(tr.ticketHash, auth.Hash(bearer(r))) {
		return nil
	}
	return tr
}

func (t *transfers) upload(w http.ResponseWriter, r *http.Request) {
	tr := t.lookup(r)
	if tr == nil {
		writeError(w, http.StatusNotFound, "unknown transfer")
		return
	}
	tr.attach()
	var body io.Reader = r.Body
	if t.maxBytes > 0 {
		body = http.MaxBytesReader(w, r.Body, t.maxBytes)
	}
	_, err := io.Copy(tr.pw, body)
	if err != nil {
		tr.fail(err)
		writeError(w, http.StatusBadGateway, "transfer failed: %v", err)
		return
	}
	_ = tr.pw.Close()
	tr.once.Do(func() { close(tr.uploaded) })
	w.WriteHeader(http.StatusNoContent)
}

func (t *transfers) download(w http.ResponseWriter, r *http.Request) {
	tr := t.lookup(r)
	if tr == nil {
		writeError(w, http.StatusNotFound, "unknown transfer")
		return
	}
	tr.attach()
	w.Header().Set("Content-Type", "application/x-tar")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, tr.pr)
}
