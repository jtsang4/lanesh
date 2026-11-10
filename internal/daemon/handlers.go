package daemon

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/files"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/runner"
	"github.com/jtsang4/lanesh/internal/skills"
	"github.com/jtsang4/lanesh/internal/version"
)

func toProtocolError(err error) *protocol.Error {
	var pe *protocol.Error
	switch {
	case errors.As(err, &pe):
		return pe
	case errors.Is(err, files.ErrNotFound), errors.Is(err, runner.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return protocol.Errorf(protocol.CodeNotFound, "%s", err.Error())
	case errors.Is(err, files.ErrExists):
		return protocol.Errorf(protocol.CodeExists, "%s", err.Error())
	case errors.Is(err, files.ErrTooLarge):
		return protocol.Errorf(protocol.CodeTooLarge, "%s", err.Error())
	case errors.Is(err, runner.ErrUnsupported):
		return protocol.Errorf(protocol.CodeUnsupported, "%s", err.Error())
	case errors.Is(err, skills.ErrOutside):
		return protocol.Errorf(protocol.CodeForbidden, "%s", err.Error())
	case errors.Is(err, context.Canceled):
		return protocol.Errorf(protocol.CodeTimeout, "request cancelled")
	default:
		return protocol.Errorf(protocol.CodeInvalid, "%s", err.Error())
	}
}

var errExecDenied = protocol.Errorf(protocol.CodeForbidden, "this machine does not allow command execution or file access for this lane")

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, protocol.Errorf(protocol.CodeInvalid, "invalid parameters: %v", err)
	}
	return v, nil
}

// dispatch executes a request from the server, enforcing this machine's policy.
func (d *Daemon) dispatch(ctx context.Context, c *laneConn, method string, raw json.RawMessage) (any, error) {
	needExec := map[string]bool{
		protocol.MethodExecStart: true, protocol.MethodJobRead: true, protocol.MethodJobWrite: true,
		protocol.MethodJobKill: true, protocol.MethodJobList: true, protocol.MethodFileRead: true,
		protocol.MethodFileWrite: true, protocol.MethodStat: true, protocol.MethodPathInspect: true,
		protocol.MethodTransferSend: true, protocol.MethodTransferReceive: true,
	}
	if needExec[method] && !c.policy.AllowExec {
		return nil, errExecDenied
	}

	switch method {
	case protocol.MethodDescribe:
		idx := d.skillIndex()
		if !c.policy.ExposeSkills {
			idx = protocol.SkillIndex{Skills: []protocol.Skill{}}
		}
		return protocol.DescribeResult{Environment: d.environment(), Policy: c.policy, Skills: idx}, nil

	case protocol.MethodSkillRead:
		if !c.policy.ExposeSkills {
			return nil, protocol.Errorf(protocol.CodeForbidden, "this machine does not share its skills with this lane")
		}
		p, err := decodeParams[protocol.SkillReadParams](raw)
		if err != nil {
			return nil, err
		}
		return skills.Read(d.skillIndex(), p)

	case protocol.MethodSkillGrep:
		if !c.policy.ExposeSkills {
			return nil, protocol.Errorf(protocol.CodeForbidden, "this machine does not share its skills with this lane")
		}
		p, err := decodeParams[protocol.SkillGrepParams](raw)
		if err != nil {
			return nil, err
		}
		return skills.Grep(d.skillIndex(), p)

	case protocol.MethodExecStart:
		p, err := decodeParams[protocol.ExecParams](raw)
		if err != nil {
			return nil, err
		}
		dir, err := d.execDir(p)
		if err != nil {
			return nil, err
		}
		p.Lane = c.lane.Key()
		st, err := d.jobs.Start(ctx, p, dir)
		if err != nil {
			return nil, err
		}
		d.audit.write("exec", map[string]any{"lane": c.lane.LaneID, "job_id": st.JobID, "command": p.Command, "cwd": dir, "owner": p.Owner})
		return st, nil

	case protocol.MethodJobRead:
		p, err := decodeParams[protocol.JobReadParams](raw)
		if err != nil {
			return nil, err
		}
		if !c.ownsJob(p.JobID) {
			return nil, fmt.Errorf("%s: %w", p.JobID, runner.ErrNotFound)
		}
		return d.jobs.Read(ctx, p)

	case protocol.MethodJobWrite:
		p, err := decodeParams[protocol.JobWriteParams](raw)
		if err != nil {
			return nil, err
		}
		if !c.ownsJob(p.JobID) {
			return nil, fmt.Errorf("%s: %w", p.JobID, runner.ErrNotFound)
		}
		return struct{}{}, d.jobs.Write(p)

	case protocol.MethodJobKill:
		p, err := decodeParams[protocol.JobKillParams](raw)
		if err != nil {
			return nil, err
		}
		if !c.ownsJob(p.JobID) {
			return nil, fmt.Errorf("%s: %w", p.JobID, runner.ErrNotFound)
		}
		d.audit.write("job.kill", map[string]any{"lane": c.lane.LaneID, "job_id": p.JobID})
		return d.jobs.Kill(p)

	case protocol.MethodJobList:
		p, err := decodeParams[protocol.JobListParams](raw)
		if err != nil {
			return nil, err
		}
		return protocol.JobListResult{Jobs: d.jobs.List(c.lane.Key(), p.Owner)}, nil

	case protocol.MethodFileRead:
		p, err := decodeParams[protocol.FileReadParams](raw)
		if err != nil {
			return nil, err
		}
		path, err := files.Expand(p.Path, d.home)
		if err != nil {
			return nil, err
		}
		d.audit.write("file.read", map[string]any{"lane": c.lane.LaneID, "path": path})
		return files.ReadFile(path, p.Offset, p.Limit, p.Encoding)

	case protocol.MethodFileWrite:
		p, err := decodeParams[protocol.FileWriteParams](raw)
		if err != nil {
			return nil, err
		}
		path, err := files.Expand(p.Path, d.home)
		if err != nil {
			return nil, err
		}
		d.audit.write("file.write", map[string]any{"lane": c.lane.LaneID, "path": path, "overwrite": p.Overwrite})
		return files.WriteFile(path, p)

	case protocol.MethodStat:
		p, err := decodeParams[protocol.StatParams](raw)
		if err != nil {
			return nil, err
		}
		path, err := files.Expand(p.Path, d.home)
		if err != nil {
			return nil, err
		}
		return files.Stat(path), nil

	case protocol.MethodPathInspect:
		p, err := decodeParams[protocol.PathInspectParams](raw)
		if err != nil {
			return nil, err
		}
		if len(p.Paths) > files.MaxInspectPaths {
			return nil, fmt.Errorf("at most %d paths at once", files.MaxInspectPaths)
		}
		res := protocol.PathInspectResult{Paths: make([]protocol.PathInfo, len(p.Paths))}
		for i, path := range p.Paths {
			res.Paths[i] = files.Inspect(path, d.home)
		}
		return res, nil

	case protocol.MethodTransferSend:
		p, err := decodeParams[protocol.TransferParams](raw)
		if err != nil {
			return nil, err
		}
		d.audit.write("transfer.send", map[string]any{"lane": c.lane.LaneID, "path": p.Path, "transfer_id": p.TransferID})
		return d.transferSend(ctx, c, p)

	case protocol.MethodTransferReceive:
		p, err := decodeParams[protocol.TransferParams](raw)
		if err != nil {
			return nil, err
		}
		d.audit.write("transfer.receive", map[string]any{"lane": c.lane.LaneID, "path": p.Path, "transfer_id": p.TransferID})
		return d.transferReceive(ctx, c, p)
	}
	return nil, protocol.Errorf(protocol.CodeUnsupported, "unknown method %q", method)
}

// execDir resolves the working directory for a command.
func (d *Daemon) execDir(p protocol.ExecParams) (string, error) {
	base := d.home
	if p.Skill != "" {
		s, ok := skills.Find(d.skillIndex(), p.Skill)
		if !ok {
			return "", protocol.Errorf(protocol.CodeNotFound, "skill %q is not installed on this machine", p.Skill)
		}
		base = s.Path
	}
	dir := base
	if p.Cwd != "" {
		if p.Skill != "" && !filepath.IsAbs(p.Cwd) && !strings.HasPrefix(p.Cwd, "~") {
			dir = filepath.Join(base, filepath.FromSlash(p.Cwd))
		} else {
			var err error
			if dir, err = files.Expand(p.Cwd, d.home); err != nil {
				return "", err
			}
		}
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", protocol.Errorf(protocol.CodeNotFound, "working directory %s does not exist", dir)
	}
	return dir, nil
}

func (c *laneConn) transferURL(path string) string {
	return strings.TrimRight(c.lane.Server, "/") + path
}

func (d *Daemon) transferSend(ctx context.Context, c *laneConn, p protocol.TransferParams) (protocol.TransferResult, error) {
	src, err := files.Expand(p.Path, d.home)
	if err != nil {
		return protocol.TransferResult{}, err
	}
	pr, pw := io.Pipe()
	statsCh := make(chan files.Stats, 1)
	go func() {
		st, err := packStream(pw, src, p.Encoding)
		statsCh <- st
		_ = pw.CloseWithError(err)
	}()
	// Unblock reading the stream when the server cancels the transfer.
	stop := context.AfterFunc(ctx, func() { _ = pr.CloseWithError(ctx.Err()) })
	defer stop()
	err = uploadStream(ctx, c.transferURL(p.URL), p.Ticket, pr, p.PartBytes)
	_ = pr.CloseWithError(errors.New("upload ended"))
	if err != nil && !errors.Is(err, errFinalUnconfirmed) {
		return protocol.TransferResult{}, err // the packer stops on its next write
	}
	st := <-statsCh
	if errors.Is(err, errFinalUnconfirmed) {
		// Report the checksum anyway: the server fails the transfer if the
		// stream did not arrive completely, and otherwise compares it.
		slog.Warn("the answer to the final transfer part was lost", "err", err)
		err = nil
	}
	if err != nil {
		return protocol.TransferResult{}, err
	}
	if st.SHA == "" {
		return protocol.TransferResult{}, errors.New("reading the source failed")
	}
	return protocol.TransferResult{Path: src, Bytes: st.Bytes, Files: st.Files, SHA256: st.SHA}, nil
}

// packStream writes src as a tar stream to w, compressed when encoding is gzip.
func packStream(w io.Writer, src, encoding string) (files.Stats, error) {
	if encoding != protocol.EncodingGzip {
		return files.Pack(w, src)
	}
	// BestSpeed keeps compression from slowing fast links while still
	// shrinking text such as logs and HAR files several times.
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return files.Stats{}, err
	}
	st, err := files.Pack(gz, src)
	if err != nil {
		return st, err
	}
	return st, gz.Close()
}

// errFinalUnconfirmed: the final part was sent, but its answer never came.
var errFinalUnconfirmed = errors.New("the final part may not have arrived")

// uploadStream posts body to url. With partBytes set it splits the body into
// numbered requests of at most partBytes each, the last one marked final, so
// that no single request exceeds a proxy's size limit. Each part is read into
// memory first: it then has a Content-Length, and the HTTP client can resend
// it when a pooled connection turns out to be closed.
func uploadStream(ctx context.Context, url, ticket string, body io.Reader, partBytes int64) error {
	if partBytes <= 0 {
		// One request, as older servers expect. They answer only after the
		// receiver read the whole stream, so no header timeout applies.
		busy, err := postPart(ctx, http.DefaultClient, url, ticket, body, false)
		if err == nil && busy {
			err = errors.New("upload failed: the server did not take the stream")
		}
		return err
	}
	br := bufio.NewReader(body)
	// Two buffers in turn: the HTTP transport may still hold the previous
	// request body for a moment after it returned.
	var bufs [2][]byte
	for part := 0; ; part++ {
		base := url + "?part=" + strconv.Itoa(part)
		// Wait for room before reading the part: the server then knows the
		// source is connected, and the part is taken as soon as it is sent.
		if err := waitForRoom(ctx, base+"&wait=1", ticket); err != nil {
			return err
		}
		buf, err := readPart(br, bufs[part%2][:0], partBytes)
		bufs[part%2] = buf
		final := errors.Is(err, io.EOF)
		if err != nil && !final {
			return err
		}
		if !final {
			// Mark the last data part final, so that no request follows
			// once the receiver may already have stored the files.
			if _, err := br.Peek(1); errors.Is(err, io.EOF) {
				final = true
			} else if err != nil {
				return err
			}
		}
		u := base
		if final {
			u += "&final=1"
		}
		if err := sendPart(ctx, u, ticket, buf); err != nil {
			var ue *uploadError
			if final && ctx.Err() == nil && (!errors.As(err, &ue) || ue.transient) {
				// The final part may have arrived with only its answer lost;
				// the server checks that the stream is complete.
				return fmt.Errorf("%w: %w", errFinalUnconfirmed, err)
			}
			return err
		}
		if final {
			return nil
		}
	}
}

// readPart appends up to limit bytes from r to buf. It returns io.EOF when r
// ends first. The buffer grows with the data, so a small transfer does not
// allocate a whole part.
func readPart(r io.Reader, buf []byte, limit int64) ([]byte, error) {
	for int64(len(buf)) < limit {
		if len(buf) == cap(buf) {
			grown := make([]byte, len(buf), min(limit, int64(max(2*cap(buf), 64<<10))))
			copy(grown, buf)
			buf = grown
		}
		n, err := r.Read(buf[len(buf):int(min(int64(cap(buf)), limit))])
		buf = buf[:len(buf)+n]
		if err != nil {
			return buf, err
		}
	}
	return buf, nil
}

// Retries of a request that failed in transit. The server keeps a part only
// once it has received all of it, and acknowledges a part it already has, so
// sending one again is safe.
// The delays add up to about 90 s, which rides out a proxy or network blip.
var partRetryDelays = []time.Duration{
	time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
}

// busyDelay paces requests the server answered with 202 ("not yet"); the
// server already waited before answering.
var busyDelay = 100 * time.Millisecond

// sendPart posts one part, which the server normally takes at once after
// waitForRoom. If it answers 202 instead, sendPart waits for room again and
// resends the part. Waits and sends share one retry budget.
func sendPart(ctx context.Context, url, ticket string, data []byte) error {
	ready := true // waitForRoom ran before the part was read
	return retryUpload(ctx, func() (bool, error) {
		if !ready {
			busy, err := postPart(ctx, transferClient, url+"&wait=1", ticket, http.NoBody, false)
			if err != nil || busy {
				return busy, err
			}
			ready = true
		}
		// The header timeout starts only once the body is written; this
		// deadline also ends a body write stuck on a dead connection, which
		// is then retried.
		reqCtx, cancel := context.WithTimeout(ctx, partRequestTimeout(len(data)))
		defer cancel()
		busy, err := postPart(reqCtx, transferClient, url, ticket, bytes.NewReader(data), true)
		ready = !busy
		return busy, err
	})
}

// partRequestTimeout bounds one part request: 10 minutes, or enough to send
// the part at 32 KiB/s, plus time for the server's answer.
func partRequestTimeout(n int) time.Duration {
	return max(10*time.Minute, time.Duration(n/(32<<10))*time.Second) + 2*time.Minute
}

// waitForRoom asks the server to wait until the next part fits. 202 means
// it is still full and the request is sent again, for as long as the
// transfer runs, so that a slow receiving machine never makes the source
// upload a part twice.
func waitForRoom(ctx context.Context, url, ticket string) error {
	return retryUpload(ctx, func() (bool, error) {
		return postPart(ctx, transferClient, url, ticket, http.NoBody, false)
	})
}

// retryUpload repeats an upload request while it answers 202, and retries
// network errors and proxy errors such as Cloudflare's 502 and 524 a few
// times in a row. Other errors from Lanesh mean the transfer failed.
func retryUpload(ctx context.Context, do func() (busy bool, err error)) error {
	failures := 0
	for {
		busy, err := do()
		var ue *uploadError
		delay := busyDelay
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err == nil && !busy:
			return nil
		case err == nil:
			failures = 0
		case errors.As(err, &ue) && !ue.transient, permanentNetError(err), failures == len(partRetryDelays):
			return err
		default:
			delay = partRetryDelays[failures]
			failures++
			slog.Warn("a transfer upload request failed; retrying", "err", err, "attempt", failures)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// permanentNetError reports network errors that a retry cannot fix: an
// untrusted or mismatched certificate, or a host name that does not exist.
func permanentNetError(err error) bool {
	var (
		certErr     *tls.CertificateVerificationError
		unknownAuth x509.UnknownAuthorityError
		hostErr     x509.HostnameError
		invalidErr  x509.CertificateInvalidError
		dnsErr      *net.DNSError
	)
	switch {
	case errors.As(err, &certErr), errors.As(err, &unknownAuth), errors.As(err, &hostErr), errors.As(err, &invalidErr):
		return true
	case errors.As(err, &dnsErr):
		return dnsErr.IsNotFound
	}
	return false
}

// uploadError is an upload request the server or a proxy answered with an
// error status. Transient errors come from a proxy and may pass on a retry.
type uploadError struct {
	msg       string
	transient bool
}

func (e *uploadError) Error() string { return e.msg }

// transferClient sends the requests of split uploads. Its header timeout
// turns a request stuck on a dead connection into an error that is retried;
// the server answers each of them within about 30 s once it has the body.
var transferClient = &http.Client{Transport: func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 2 * time.Minute
	return t
}()}

// postPart posts one upload request; part says whether it carries a part of
// a split upload (for the error hints). busy reports a 202 answer: the server
// did not take the request and asks for it again.
func postPart(ctx context.Context, client *http.Client, url, ticket string, body io.Reader, part bool) (busy bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+ticket)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "lanesh/"+version.Version)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		return true, nil
	}
	if resp.StatusCode/100 == 2 {
		return false, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := protocol.HTTPErrorMessage(raw)
	text := responseError(resp, raw, msg)
	if hint := proxyHint(resp, msg, part); hint != "" {
		text += "; " + hint
	}
	return false, &uploadError{msg: "upload failed: " + text, transient: msg == "" && transientStatus(resp.StatusCode)}
}

// transientStatus reports statuses a proxy answers with when the server was
// briefly unreachable or slow, including Cloudflare's 520–527.
func transientStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return code >= 520 && code <= 527
}

// responseError describes a failed data plane response, given the message
// of its Lanesh JSON error, if any. Anything else, such as an HTML page, comes
// from a proxy in front of the server and is summarized instead of quoted.
func responseError(resp *http.Response, body []byte, laneshMsg string) string {
	if laneshMsg != "" {
		return resp.Status + ": " + laneshMsg
	}
	if text := strings.TrimSpace(string(body)); text != "" && !strings.HasPrefix(text, "<") {
		return resp.Status + ": " + text
	}
	return resp.Status
}

// proxyHint explains upload failures that a proxy in front of the server
// causes, such as Cloudflare's request size limit and response timeout.
func proxyHint(resp *http.Response, laneshMsg string, split bool) string {
	if laneshMsg != "" {
		return ""
	}
	switch resp.StatusCode {
	case http.StatusRequestEntityTooLarge:
		if !split {
			return "a proxy in front of the Lanesh server limits the request size; upgrade the server so that it splits uploads"
		}
		return "a proxy in front of the Lanesh server limits the request size; lower LANESH_TRANSFER_PART_BYTES on the server below that limit"
	case http.StatusGatewayTimeout, 524: // 524: Cloudflare's origin timeout.
		if split {
			return "a proxy in front of the Lanesh server timed out; lower LANESH_TRANSFER_PART_BYTES on the server"
		}
	}
	return ""
}

func (d *Daemon) transferReceive(ctx context.Context, c *laneConn, p protocol.TransferParams) (protocol.TransferResult, error) {
	dst, err := files.Expand(p.Path, d.home)
	if err != nil {
		return protocol.TransferResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.transferURL(p.URL), nil)
	if err != nil {
		return protocol.TransferResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Ticket)
	req.Header.Set("User-Agent", "lanesh/"+version.Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return protocol.TransferResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return protocol.TransferResult{}, fmt.Errorf("download failed: %s", responseError(resp, msg, protocol.HTTPErrorMessage(msg)))
	}
	var stream io.Reader = resp.Body
	if p.Encoding == protocol.EncodingGzip {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return protocol.TransferResult{}, fmt.Errorf("download failed: %w", err)
		}
		defer gz.Close() //nolint:errcheck // Reading is complete or failed already.
		stream = gz
	}
	st, err := files.Unpack(stream, dst, p.Overwrite)
	if err != nil {
		return protocol.TransferResult{}, err
	}
	return protocol.TransferResult{Path: dst, Bytes: st.Bytes, Files: st.Files, SHA256: st.SHA}, nil
}
