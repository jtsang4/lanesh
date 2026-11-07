package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
		protocol.MethodFileWrite: true, protocol.MethodStat: true,
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

	case protocol.MethodExecStart:
		p, err := decodeParams[protocol.ExecParams](raw)
		if err != nil {
			return nil, err
		}
		dir, err := d.execDir(p)
		if err != nil {
			return nil, err
		}
		st, err := d.jobs.Start(ctx, p, dir)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.jobs[st.JobID] = true
		c.mu.Unlock()
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
		var jobs []protocol.JobStatus
		for _, j := range d.jobs.List(p.Owner) {
			if c.ownsJob(j.JobID) {
				jobs = append(jobs, j)
			}
		}
		if jobs == nil {
			jobs = []protocol.JobStatus{}
		}
		return protocol.JobListResult{Jobs: jobs}, nil

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
		st, err := files.Pack(pw, src)
		statsCh <- st
		_ = pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.transferURL(p.URL), pr)
	if err != nil {
		return protocol.TransferResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Ticket)
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = pr.CloseWithError(err)
		return protocol.TransferResult{}, err
	}
	defer resp.Body.Close()
	_ = pr.Close()
	st := <-statsCh
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return protocol.TransferResult{}, fmt.Errorf("upload failed: %s %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if st.SHA == "" {
		return protocol.TransferResult{}, errors.New("reading the source failed")
	}
	return protocol.TransferResult{Path: src, Bytes: st.Bytes, Files: st.Files, SHA256: st.SHA}, nil
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
		return protocol.TransferResult{}, fmt.Errorf("download failed: %s %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	st, err := files.Unpack(resp.Body, dst, p.Overwrite)
	if err != nil {
		return protocol.TransferResult{}, err
	}
	return protocol.TransferResult{Path: dst, Bytes: st.Bytes, Files: st.Files, SHA256: st.SHA}, nil
}
