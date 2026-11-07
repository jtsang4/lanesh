package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// ErrOffline is returned when a machine is not connected.
var ErrOffline = errors.New("machine is offline")

const (
	pingInterval = 25 * time.Second
	readLimit    = 32 << 20
	writeTimeout = 30 * time.Second
)

// Conn is a connected daemon within one lane.
type Conn struct {
	ws        *websocket.Conn
	LaneID    string
	MachineID string
	Hello     protocol.Hello
	Connected time.Time

	writeMu sync.Mutex
	nextID  atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan protocol.Message
	env     protocol.Environment
	skills  protocol.SkillIndex
	closed  chan struct{}
	once    sync.Once
}

func newConn(ws *websocket.Conn, laneID string, hello protocol.Hello) *Conn {
	return &Conn{
		ws: ws, LaneID: laneID, MachineID: hello.MachineID, Hello: hello, Connected: time.Now(),
		pending: map[string]chan protocol.Message{}, closed: make(chan struct{}),
	}
}

func (c *Conn) send(ctx context.Context, m protocol.Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, raw)
}

// Call sends a request and decodes the result into out.
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	msg, err := protocol.NewMessage(id, method, params)
	if err != nil {
		return err
	}
	ch := make(chan protocol.Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.send(ctx, msg); err != nil {
		return fmt.Errorf("%w: %w", ErrOffline, err)
	}
	select {
	case resp := <-ch:
		if resp.Type == protocol.TypeError {
			if resp.Error == nil {
				return protocol.Errorf(protocol.CodeInternal, "unknown error")
			}
			return resp.Error
		}
		if out != nil && len(resp.Payload) > 0 {
			return json.Unmarshal(resp.Payload, out)
		}
		return nil
	case <-ctx.Done():
		cancelMsg, _ := protocol.NewMessage(id, protocol.TypeCancel, nil)
		_ = c.send(context.WithoutCancel(ctx), cancelMsg)
		return ctx.Err()
	case <-c.closed:
		return ErrOffline
	}
}

// readLoop dispatches responses and events until the connection closes.
func (c *Conn) readLoop(ctx context.Context, onEvent func(*Conn, protocol.Message)) error {
	for {
		_, raw, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		var m protocol.Message
		if err := json.Unmarshal(raw, &m); err != nil {
			slog.Warn("bad message from daemon", "machine", c.MachineID, "err", err)
			continue
		}
		switch m.Type {
		case protocol.TypeResult, protocol.TypeError:
			c.mu.Lock()
			ch := c.pending[m.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		default:
			onEvent(c, m)
		}
	}
}

func (c *Conn) pingLoop(ctx context.Context) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 3*pingInterval)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.Close(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}

// Close closes the connection.
func (c *Conn) Close(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		close(c.closed)
		_ = c.ws.Close(code, reason)
	})
}

// State returns the machine's environment and skill index as last reported.
func (c *Conn) State() (protocol.Environment, protocol.SkillIndex) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.env, c.skills
}

func (c *Conn) setDescribe(d protocol.DescribeResult) {
	c.mu.Lock()
	c.env, c.skills = d.Environment, d.Skills
	c.mu.Unlock()
}

func (c *Conn) setSkills(idx protocol.SkillIndex) {
	c.mu.Lock()
	c.skills = idx
	c.mu.Unlock()
}

// Hub tracks live daemon connections by lane and machine.
type Hub struct {
	mu    sync.RWMutex
	conns map[string]map[string]*Conn
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{conns: map[string]map[string]*Conn{}} }

// add registers c, closing any previous connection of the same machine.
func (h *Hub) add(c *Conn) {
	h.mu.Lock()
	lane := h.conns[c.LaneID]
	if lane == nil {
		lane = map[string]*Conn{}
		h.conns[c.LaneID] = lane
	}
	old := lane[c.MachineID]
	lane[c.MachineID] = c
	h.mu.Unlock()
	if old != nil {
		old.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
}

func (h *Hub) remove(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if lane := h.conns[c.LaneID]; lane != nil && lane[c.MachineID] == c {
		delete(lane, c.MachineID)
	}
}

// Get returns the live connection for a machine in a lane.
func (h *Hub) Get(laneID, machineID string) *Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[laneID][machineID]
}

// Lane returns all live connections of a lane.
func (h *Hub) Lane(laneID string) map[string]*Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]*Conn, len(h.conns[laneID]))
	for k, v := range h.conns[laneID] {
		out[k] = v
	}
	return out
}

// Count returns the number of live connections.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, l := range h.conns {
		n += len(l)
	}
	return n
}

// Disconnect closes a machine's connection in a lane (or all of the lane's
// connections when machineID is empty).
func (h *Hub) Disconnect(laneID, machineID, reason string) {
	for id, c := range h.Lane(laneID) {
		if machineID == "" || id == machineID {
			c.Close(websocket.StatusPolicyViolation, reason)
		}
	}
}
