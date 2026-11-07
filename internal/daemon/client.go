package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/version"
)

const (
	pingInterval = 25 * time.Second
	readLimit    = 32 << 20
	maxBackoff   = 60 * time.Second
)

// laneConn maintains the connection to one lane, reconnecting as needed.
type laneConn struct {
	d      *Daemon
	lane   LaneConfig
	cred   string
	policy protocol.Policy

	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	ws        *websocket.Conn
	writeMu   sync.Mutex
	connected bool
	since     time.Time
	lastErr   string
	serverVer string
	laneName  string
	name      string
	inflight  map[string]context.CancelFunc
	jobs      map[string]bool
}

func newLaneConn(d *Daemon, lane LaneConfig, cred string, policy protocol.Policy) *laneConn {
	return &laneConn{
		d: d, lane: lane, cred: cred, policy: policy, done: make(chan struct{}),
		since: time.Now(), laneName: lane.LaneName, name: lane.MachineName,
		inflight: map[string]context.CancelFunc{}, jobs: map[string]bool{},
	}
}

func (c *laneConn) stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *laneConn) state() LaneState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return LaneState{
		Server: c.lane.Server, LaneID: c.lane.LaneID, LaneName: c.laneName, MachineName: c.name,
		Connected: c.connected, Since: c.since, LastError: c.lastErr, ServerVersion: c.serverVer,
	}
}

func (c *laneConn) setConnected(ok bool, err error) {
	c.mu.Lock()
	if ok != c.connected {
		c.since = time.Now()
	}
	c.connected = ok
	if err != nil {
		c.lastErr = err.Error()
	} else if ok {
		c.lastErr = ""
	}
	c.mu.Unlock()
	c.d.writeState()
}

func (c *laneConn) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	defer close(c.done)

	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := c.session(ctx)
		c.setConnected(false, err)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		wait := backoff + time.Duration(rand.Int64N(int64(backoff/2)+1)) //nolint:gosec // Jitter does not need a CSPRNG.
		slog.Warn("lane connection lost; retrying", "lane", c.lane.Key(), "err", err, "in", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func wsURL(server string) string {
	if rest, ok := strings.CutPrefix(server, "https://"); ok {
		return "wss://" + rest
	}
	if rest, ok := strings.CutPrefix(server, "http://"); ok {
		return "ws://" + rest
	}
	return server
}

func (c *laneConn) session(ctx context.Context) error {
	url := wsURL(strings.TrimRight(c.lane.Server, "/")) + "/lanes/" + c.lane.LaneID + "/connect"
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.cred}, "User-Agent": {"lanesh/" + version.Version}},
	})
	cancel()
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errors.New("the server rejected this machine's credential; it may have been removed from the lane (run lanesh join again)")
		}
		return err
	}
	defer ws.CloseNow() //nolint:errcheck // Close errors are irrelevant here.
	ws.SetReadLimit(readLimit)
	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()

	idx := c.d.skillIndex()
	hello := protocol.Hello{
		Protocol: protocol.Version, Version: version.Version, MachineID: c.d.machineID, Name: c.d.machineName(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: c.d.hostname, Policy: c.policy,
		SkillsHash: idx.Hash, SkillCount: len(idx.Skills),
	}
	if !c.policy.ExposeSkills {
		hello.SkillsHash, hello.SkillCount = "", 0
	}
	if err := c.send(ctx, "", protocol.TypeHello, hello, nil); err != nil {
		return err
	}
	welcome, err := readWelcome(ctx, ws)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.serverVer, c.laneName, c.name = welcome.ServerVersion, welcome.LaneName, welcome.MachineName
	c.mu.Unlock()
	c.setConnected(true, nil)
	slog.Info("connected to lane", "lane", welcome.LaneName, "server", c.lane.Server, "as", welcome.MachineName, "server_version", welcome.ServerVersion)
	if c.d.opts.OnServerVersion != nil {
		go c.d.opts.OnServerVersion(welcome.ServerVersion)
	}

	sessCtx, cancelSess := context.WithCancel(ctx)
	defer cancelSess()
	go c.pingLoop(sessCtx, ws)

	for {
		_, raw, err := ws.Read(sessCtx)
		if err != nil {
			if s := websocket.CloseStatus(err); s != -1 {
				return fmt.Errorf("server closed the connection: %s", closeReason(err))
			}
			return err
		}
		var m protocol.Message
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m.Type == protocol.TypeCancel {
			c.mu.Lock()
			if cancel := c.inflight[m.ID]; cancel != nil {
				cancel()
			}
			c.mu.Unlock()
			continue
		}
		go c.handle(sessCtx, m)
	}
}

func closeReason(err error) string {
	var ce websocket.CloseError
	if errors.As(err, &ce) && ce.Reason != "" {
		return ce.Reason
	}
	return err.Error()
}

func readWelcome(ctx context.Context, ws *websocket.Conn) (protocol.Welcome, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, raw, err := ws.Read(ctx)
	if err != nil {
		return protocol.Welcome{}, err
	}
	var m protocol.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return protocol.Welcome{}, err
	}
	if m.Type == protocol.TypeError && m.Error != nil {
		return protocol.Welcome{}, m.Error
	}
	if m.Type != protocol.TypeWelcome {
		return protocol.Welcome{}, fmt.Errorf("unexpected %q before welcome", m.Type)
	}
	var w protocol.Welcome
	err = json.Unmarshal(m.Payload, &w)
	return w, err
}

func (c *laneConn) pingLoop(ctx context.Context, ws *websocket.Conn) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 3*pingInterval)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				_ = ws.Close(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}

func (c *laneConn) send(ctx context.Context, id, typ string, payload any, perr *protocol.Error) error {
	m, err := protocol.NewMessage(id, typ, payload)
	if err != nil {
		return err
	}
	m.Error = perr
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return errors.New("not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return ws.Write(ctx, websocket.MessageText, raw)
}

// event sends an event if connected.
func (c *laneConn) event(typ string, payload any) {
	c.mu.Lock()
	connected := c.connected
	c.mu.Unlock()
	if connected {
		_ = c.send(context.Background(), "", typ, payload, nil)
	}
}

func (c *laneConn) ownsJob(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.jobs[id]
}

func (c *laneConn) handle(ctx context.Context, m protocol.Message) {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.inflight[m.ID] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.inflight, m.ID)
		c.mu.Unlock()
	}()

	result, err := c.d.dispatch(ctx, c, m.Type, m.Payload)
	if err != nil {
		_ = c.send(ctx, m.ID, protocol.TypeError, nil, toProtocolError(err))
		return
	}
	_ = c.send(ctx, m.ID, protocol.TypeResult, result, nil)
}
