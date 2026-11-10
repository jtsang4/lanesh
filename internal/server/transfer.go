package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
)

// transfers brokers streaming file transfers between two daemons. The source
// uploads and the destination downloads; bytes pass through memory and never
// touch the disk.
//
// The source uploads either in one request (daemons without
// protocol.FeatureTransferParts) or as numbered parts, POST ?part=0, 1, ...,
// the last one with final=1. Parts keep each request below the request size
// limit of proxies such as Cloudflare. Before each part the source sends an
// empty ?part=N&wait=1, which returns once the queue has room for a part; no
// request waits long for the downloader. 202 means "not yet, send again",
// and sending any request again is safe.
type transfers struct {
	maxBytes  int64
	partBytes int64
	// partWait is how long a part request waits for room in the buffer. It
	// stays well below Cloudflare's 100 s response timeout.
	partWait time.Duration
	mu       sync.Mutex
	active   map[string]*transfer
}

type transfer struct {
	laneID     string
	ticketHash string
	buf        *streamBuffer
	timer      *time.Timer

	mu         sync.Mutex
	uploader   bool
	downloader bool

	// upMu serializes upload requests. nextPart changes only under it, but
	// is read without it to spot resent parts early.
	upMu         sync.Mutex
	nextPart     atomic.Int64
	written      int64
	readFailures atomic.Int32 // part bodies cut off in a row
	failedFirst  atomic.Int32 // the side whose cancel failed the transfer
	// reading admits one part body at a time, which bounds the memory of a
	// transfer to its queue and one part in flight. It is a channel so that
	// uploaded can wait for it with a context.
	reading chan struct{}
}

// attach records that one side connected; the connect timeout stops once
// both sides are streaming.
func (tr *transfer) attach(upload bool) {
	tr.mu.Lock()
	if upload {
		tr.uploader = true
	} else {
		tr.downloader = true
	}
	both := tr.uploader && tr.downloader
	tr.mu.Unlock()
	if both {
		tr.timer.Stop()
	}
}

const transferConnectTimeout = 60 * time.Second

// newTransfers brokers transfers whose upload parts hold at most partBytes.
// Each transfer queues up to two parts
// for its downloader, so that one part uploads while the previous one drains.
func newTransfers(maxBytes, partBytes int64) *transfers {
	return &transfers{maxBytes: maxBytes, partBytes: partBytes, partWait: 30 * time.Second, active: map[string]*transfer{}}
}

// maxLaneTransfers bounds the transfers a lane runs at once, and with it the
// memory they buffer: up to three parts each.
const maxLaneTransfers = 8

// create registers a transfer of a lane and returns its ID and one-time
// ticket.
func (t *transfers) create(laneID string) (id, ticket string, err error) {
	id, ticket = auth.NewID("tr_"), auth.NewToken(auth.PrefixTicket)
	tr := &transfer{laneID: laneID, ticketHash: auth.Hash(ticket), buf: newStreamBuffer(int(2 * t.partBytes)), reading: make(chan struct{}, 1)}
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, other := range t.active {
		if other.laneID == laneID {
			n++
		}
	}
	if n >= maxLaneTransfers {
		return "", "", fmt.Errorf("this lane already runs %d transfers; try again when one has finished", maxLaneTransfers)
	}
	tr.timer = time.AfterFunc(transferConnectTimeout, func() {
		tr.fail(errors.New("transfer timed out waiting for both machines"))
	})
	t.active[id] = tr
	return id, ticket, nil
}

func (tr *transfer) fail(err error) { tr.buf.fail(err) }

// Sides of a transfer, for telling which one failed first.
const (
	sideSource      = 1
	sideDestination = 2
)

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

func (t *transfers) get(id string) *transfer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active[id]
}

// cancel aborts a transfer because one side failed.
func (t *transfers) cancel(id string, side int32, err error) {
	if tr := t.get(id); tr != nil && tr.buf.fail(err) {
		tr.failedFirst.Store(side)
	}
}

// destinationFailedFirst reports whether the destination's failure ended the
// transfer, rather than the source's.
func (t *transfers) destinationFailedFirst(id string) bool {
	tr := t.get(id)
	return tr != nil && tr.failedFirst.Load() == sideDestination
}

// uploaded reports whether the whole stream of a transfer arrived. It first
// waits for a part request in progress, such as a final part whose answer
// the source did not get, or until ctx ends.
func (t *transfers) uploaded(ctx context.Context, id string) bool {
	tr := t.get(id)
	if tr == nil {
		return false
	}
	if tr.buf.done() {
		return true
	}
	select {
	case tr.reading <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	tr.upMu.Lock()
	done := tr.buf.done()
	tr.upMu.Unlock()
	<-tr.reading
	if done {
		return true
	}
	// The final part may still be on its way, for example held by a proxy
	// after the source gave up waiting for the answer.
	late, cancel := context.WithTimeout(ctx, t.partWait)
	defer cancel()
	return tr.buf.waitDone(late)
}

func (t *transfers) lookup(r *http.Request) *transfer {
	tr := t.get(r.PathValue("id"))
	if tr == nil || !auth.Equal(tr.ticketHash, auth.Hash(bearer(r))) {
		return nil
	}
	return tr
}

// uploadError is an upload request that failed with an HTTP status. Unless
// keep is set, it also fails the transfer.
type uploadError struct {
	status int
	err    error
	keep   bool
}

func (e *uploadError) Error() string { return e.err.Error() }
func (e *uploadError) Unwrap() error { return e.err }

var (
	errUploadComplete = errors.New("the upload is already complete")
	// errBusy answers 202: the request was not taken, send it again. A
	// success status passes proxies that replace error pages.
	errBusy = errors.New("the receiving machine has not caught up yet")
	// errPartCutOff is a part whose body ended early. It is answered like
	// errBusy, so that the source sends it again, up to maxPartReadFailures
	// times in a row.
	errPartCutOff = errors.New("the part was cut off")
)

const maxPartReadFailures = 5

// partReadTimeout bounds reading one part body: at least 10 minutes, and
// enough for a part at 32 KiB/s.
func partReadTimeout(partBytes int64) time.Duration {
	return max(10*time.Minute, time.Duration(partBytes/(32<<10))*time.Second)
}

func (t *transfers) upload(w http.ResponseWriter, r *http.Request) {
	tr := t.lookup(r)
	if tr == nil {
		writeError(w, http.StatusNotFound, "unknown transfer")
		return
	}
	tr.attach(true)
	if r.URL.Query().Has("part") {
		// A part body that stops arriving (a connection that died silently)
		// must not hold the request, or the read slot, for long. An upload
		// in one request from an older daemon may take as long as it needs.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(partReadTimeout(t.partBytes)))
	}
	err := t.receive(tr, r)
	// Read what is left of the body (up to a part) before answering, as when
	// a part is turned away or found to be a resend, so that the connection
	// stays usable and the source gets the answer rather than a reset.
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, t.partBytes+1))
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, errBusy) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var ue *uploadError
	if !errors.As(err, &ue) {
		ue = &uploadError{status: http.StatusBadGateway, err: err}
	}
	if !ue.keep {
		tr.fail(err)
	}
	writeError(w, ue.status, "transfer failed: %v", err)
}

// receive handles one upload request: a numbered part, or without a part
// number the whole stream.
func (t *transfers) receive(tr *transfer, r *http.Request) error {
	q := r.URL.Query()
	if !q.Has("part") {
		return t.receiveWhole(tr, r)
	}
	part, err := strconv.Atoi(q.Get("part"))
	if err != nil || part < 0 {
		return &uploadError{status: http.StatusBadRequest, err: fmt.Errorf("invalid part %q", q.Get("part"))}
	}
	if err := tr.buf.failed(); err != nil {
		return &uploadError{status: http.StatusBadGateway, err: err, keep: true}
	}
	if q.Get("wait") == "1" {
		return t.waitRoom(tr, r, part)
	}
	if int64(part) < tr.nextPart.Load() {
		// The source missed the answer for a part already in the stream
		// and sent it again.
		return nil
	}
	// One deadline covers every wait of this request, waiting for the read
	// slot and for room in the queue, so that its answer stays well within a
	// proxy's response timeout.
	wait, cancelWait := context.WithTimeout(r.Context(), t.partWait)
	defer cancelWait()
	// Another request for this transfer may be reading a part, such as the
	// original of a resend; wait for it a while before turning this one away.
	select {
	case tr.reading <- struct{}{}:
		defer func() { <-tr.reading }()
	case <-wait.Done():
		return errBusy
	}
	if int64(part) < tr.nextPart.Load() {
		return nil // the original arrived meanwhile
	}
	// Read the whole part before touching the stream: a request that fails
	// midway then leaves the stream intact, and the source sends it again.
	data, err := readBody(r, t.partBytes)
	if errors.Is(err, errPartCutOff) {
		return t.cutOff(tr)
	}
	if err != nil {
		return err
	}

	tr.upMu.Lock()
	defer tr.upMu.Unlock()
	if err := tr.buf.failed(); err != nil {
		return &uploadError{status: http.StatusBadGateway, err: err, keep: true}
	}
	if final := q.Get("final") == "1"; (!final && int64(len(data)) != t.partBytes) || (final && len(data) == 0) {
		// The source fills every part but the last, and the last is never
		// empty; a shorter one lost its body on the way (for example to a
		// proxy) and is sent again.
		return t.cutOff(tr)
	}
	next := int(tr.nextPart.Load())
	switch {
	case part < next:
		return nil // a resend that raced with the original
	case tr.buf.done():
		return &uploadError{status: http.StatusConflict, err: errUploadComplete, keep: true}
	case part != next:
		return &uploadError{status: http.StatusConflict, err: fmt.Errorf("expected part %d, got %d", next, part)}
	case t.maxBytes > 0 && tr.written+int64(len(data)) > t.maxBytes:
		return t.tooLarge()
	}
	if err := tr.buf.push(wait, data); err != nil {
		if waitEnded(err) {
			return errBusy
		}
		return err
	}
	tr.readFailures.Store(0)
	tr.written += int64(len(data))
	tr.nextPart.Add(1)
	if q.Get("final") == "1" {
		tr.buf.close()
	}
	return nil
}

// waitRoom answers ?wait=1 once the queue has room for a whole part, so that
// the part sent next is taken without waiting; errBusy when it stays full.
// It also marks the uploader as connected before it reads its first part.
func (t *transfers) waitRoom(tr *transfer, r *http.Request, part int) error {
	if int64(part) < tr.nextPart.Load() {
		return nil // a resend, which the server acknowledges at once
	}
	ctx, cancel := context.WithTimeout(r.Context(), t.partWait)
	defer cancel()
	err := tr.buf.waitRoom(ctx, int(t.partBytes))
	switch {
	case err == nil, errors.Is(err, errStreamClosed):
		// A closed stream answers the part itself.
		return nil
	case waitEnded(err):
		return errBusy
	}
	return &uploadError{status: http.StatusBadGateway, err: err, keep: true}
}

// waitEnded reports a wait that ended without room: its time ran out, or the
// request ended (the source gave up, the server shuts down). Both answer 202,
// "send again".
func waitEnded(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// cutOff answers a part whose body did not fully arrive: 202, so that the
// source sends it again, until maxPartReadFailures in a row fail the
// transfer.
func (t *transfers) cutOff(tr *transfer) error {
	if tr.readFailures.Add(1) < maxPartReadFailures {
		return errBusy
	}
	return &uploadError{status: http.StatusBadRequest, err: fmt.Errorf("%d parts in a row were cut off", maxPartReadFailures)}
}

// readBody reads an upload part of at most limit bytes.
func readBody(r *http.Request, limit int64) ([]byte, error) {
	if r.ContentLength > limit {
		return nil, &uploadError{status: http.StatusRequestEntityTooLarge, err: fmt.Errorf("the part exceeds %d bytes", limit)}
	}
	var data []byte
	var err error
	if r.ContentLength >= 0 {
		data = make([]byte, r.ContentLength)
		_, err = io.ReadFull(r.Body, data)
	} else {
		data, err = io.ReadAll(io.LimitReader(r.Body, limit+1))
	}
	if err != nil {
		return nil, &uploadError{status: http.StatusBadRequest, err: fmt.Errorf("%w: %w", errPartCutOff, err), keep: true}
	}
	if int64(len(data)) > limit {
		return nil, &uploadError{status: http.StatusRequestEntityTooLarge, err: fmt.Errorf("the part exceeds %d bytes", limit)}
	}
	return data, nil
}

// receiveWhole streams an upload that is not split into parts, as daemons
// without protocol.FeatureTransferParts send it.
func (t *transfers) receiveWhole(tr *transfer, r *http.Request) error {
	tr.upMu.Lock()
	defer tr.upMu.Unlock()
	if tr.buf.done() {
		return &uploadError{status: http.StatusConflict, err: errUploadComplete, keep: true}
	}
	if tr.nextPart.Load() != 0 {
		return &uploadError{status: http.StatusConflict, err: errors.New("the upload already started in parts")}
	}
	scratch := make([]byte, 64<<10)
	for {
		// Read, not io.ReadFull: a cut-off body ends in io.ErrUnexpectedEOF,
		// which must fail the transfer rather than end the stream.
		n, err := r.Body.Read(scratch)
		if n > 0 {
			if t.maxBytes > 0 && tr.written+int64(n) > t.maxBytes {
				return t.tooLarge()
			}
			if err := tr.buf.push(r.Context(), bytes.Clone(scratch[:n])); err != nil {
				return err
			}
			tr.written += int64(n)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	tr.nextPart.Add(1)
	tr.buf.close()
	return nil
}

func (t *transfers) tooLarge() error {
	return &uploadError{status: http.StatusRequestEntityTooLarge, err: fmt.Errorf("the transfer exceeds the server's limit of %d bytes", t.maxBytes)}
}

func (t *transfers) download(w http.ResponseWriter, r *http.Request) {
	tr := t.lookup(r)
	if tr == nil {
		writeError(w, http.StatusNotFound, "unknown transfer")
		return
	}
	tr.attach(false)
	// The stream is a tar archive, gzip-compressed for daemons that agreed.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	// Send the headers now: the first bytes may arrive only after a whole
	// part, and proxies such as Cloudflare give up on silent responses.
	_ = http.NewResponseController(w).Flush()
	// The buffer returns io.EOF, which io.Copy reports as nil, only after
	// the final part; on any failure abort the response so that the receiver
	// sees an error instead of a cleanly terminated (and truncated) stream.
	if _, err := io.Copy(w, tr.buf.reader(r.Context())); err != nil {
		panic(http.ErrAbortHandler)
	}
}
