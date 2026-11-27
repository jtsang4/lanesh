package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/jtsang4/lanesh/internal/daemon"
	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/version"
)

type clientConfig struct {
	LaneURL   string `toml:"lane_url"`
	AccessKey string `toml:"access_key"`
	Machine   string `toml:"machine,omitempty"`
}

type laneClient struct {
	server    string
	laneID    string
	accessKey string
	machine   string
	http      *http.Client
	longHTTP  *http.Client
}

var clientFlags struct {
	laneURL string
	key     string
	machine string
}

func addClientFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&clientFlags.laneURL, "lane-url", "", "lane or MCP URL (default: from `lanesh connect` or LANESH_LANE_URL)")
	cmd.PersistentFlags().StringVar(&clientFlags.key, "access-key", "", "lane access key (default: from `lanesh connect` or LANESH_ACCESS_KEY)")
	cmd.PersistentFlags().StringVar(&clientFlags.machine, "machine-name", "", "identify this machine in the lane (default: auto-detected)")
}

func newLaneClient(laneURL, key, machine string) (laneClient, error) {
	server, laneID, err := daemon.ParseClientLaneURL(laneURL)
	if err != nil {
		return laneClient{}, err
	}
	return laneClient{
		server:    strings.TrimRight(server, "/"),
		laneID:    laneID,
		accessKey: strings.TrimSpace(key),
		machine:   strings.TrimSpace(machine),
		http:      &http.Client{Timeout: 90 * time.Second},
		longHTTP:  &http.Client{},
	}, nil
}

func loadClient() (laneClient, error) {
	laneURL := strings.TrimSpace(clientFlags.laneURL)
	key := strings.TrimSpace(clientFlags.key)
	machine := strings.TrimSpace(clientFlags.machine)
	if laneURL == "" {
		if v := strings.TrimSpace(os.Getenv("LANESH_LANE_URL")); v != "" {
			laneURL = v
		} else {
			laneURL = strings.TrimSpace(os.Getenv("LANESH_MCP_URL"))
		}
	}
	if key == "" {
		key = strings.TrimSpace(os.Getenv("LANESH_ACCESS_KEY"))
	}
	if machine == "" {
		machine = strings.TrimSpace(os.Getenv("LANESH_MACHINE"))
	}
	if laneURL == "" || key == "" || machine == "" {
		p, err := paths()
		if err != nil {
			return laneClient{}, err
		}
		if raw, err := os.ReadFile(p.ClientConfig()); err == nil {
			var cc clientConfig
			if err := toml.Unmarshal(raw, &cc); err == nil {
				if laneURL == "" {
					laneURL = strings.TrimSpace(cc.LaneURL)
				}
				if key == "" {
					key = strings.TrimSpace(cc.AccessKey)
				}
				if machine == "" {
					machine = strings.TrimSpace(cc.Machine)
				}
			}
		}
	}
	if laneURL == "" || key == "" {
		return laneClient{}, errors.New("not connected: run `lanesh connect <lane-url> --key <access-key>` (or set LANESH_LANE_URL and LANESH_ACCESS_KEY)")
	}
	return newLaneClient(laneURL, key, machine)
}

func (c laneClient) laneURL() string {
	return c.server + "/lanes/" + c.laneID
}

func (c laneClient) do(ctx context.Context, method, path string, body, out any) error {
	return c.doWith(ctx, c.http, method, path, body, out)
}

func (c laneClient) doLong(ctx context.Context, method, path string, body, out any) error {
	return c.doWith(ctx, c.longHTTP, method, path, body, out)
}

func (c laneClient) doWith(ctx context.Context, hc *http.Client, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.laneURL()+"/v1"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessKey)
	req.Header.Set("User-Agent", "lanesh/"+version.Version)
	if c.machine != "" {
		req.Header.Set(mcp.HeaderMachine, c.machine)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if msg := protocol.HTTPErrorMessage(raw); msg != "" {
			return errors.New(msg)
		}
		return errors.New(resp.Status)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type clientMachine struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	OS          string               `json:"os"`
	Arch        string               `json:"arch"`
	Version     string               `json:"version"`
	Online      bool                 `json:"online"`
	LastSeen    time.Time            `json:"last_seen"`
	Policy      protocol.Policy      `json:"policy"`
	SkillCount  int                  `json:"skill_count"`
	Environment protocol.Environment `json:"environment"`
}

// detectLocalMachine checks whether this machine is joined to the same lane
// and returns its machine name in the lane if found.
func (c laneClient) detectLocalMachine(ctx context.Context, ms []clientMachine) string {
	if c.machine != "" {
		return c.machine
	}
	p, err := paths()
	if err != nil {
		return ""
	}
	cfg, err := daemon.LoadConfig(p)
	if err != nil {
		return ""
	}
	key := c.laneURL()
	joined := false
	for _, l := range cfg.Lanes {
		if l.Key() == key {
			joined = true
			break
		}
	}
	if !joined {
		return ""
	}
	rawID, err := os.ReadFile(p.MachineIDFile())
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(rawID))
	if id == "" {
		return ""
	}
	if ms == nil {
		if err := c.do(ctx, http.MethodGet, "/machines", nil, &ms); err != nil {
			return ""
		}
	}
	for _, m := range ms {
		if m.ID == id {
			return m.Name
		}
	}
	return ""
}

// splitRemote splits "<machine>:<path>" into its machine name and path.
// Single-letter Windows drive prefixes such as "C:\..." or "D:/..." and
// prefixes containing path separators are treated as local paths.
func splitRemote(spec string) (machine, path string, ok bool) {
	i := strings.IndexByte(spec, ':')
	if i <= 0 {
		return "", spec, false
	}
	prefix := spec[:i]
	if len(prefix) == 1 && ((prefix[0] >= 'a' && prefix[0] <= 'z') || (prefix[0] >= 'A' && prefix[0] <= 'Z')) {
		return "", spec, false
	}
	if strings.ContainsAny(prefix, `/\`) {
		return "", spec, false
	}
	return prefix, spec[i+1:], true
}

// parseTarget accepts either "<machine>:<path>" (1 arg) or "<machine> <path>" (2 args).
func parseTarget(args []string, allowEmptyPath bool) (machine, path string, err error) {
	switch len(args) {
	case 1:
		if m, p, ok := splitRemote(args[0]); ok {
			if p == "" && !allowEmptyPath {
				return "", "", fmt.Errorf("path is required in %q", args[0])
			}
			return m, p, nil
		}
		if allowEmptyPath {
			return args[0], "", nil
		}
		return "", "", fmt.Errorf("expected <machine>:<path> or <machine> <path>, got %q", args[0])
	case 2:
		return args[0], args[1], nil
	default:
		return "", "", fmt.Errorf("expected <machine>:<path> or <machine> <path>")
	}
}

// parseLineRange parses "N", "N-M", "N-", or "-M" (also accepting ':' as separator).
func parseLineRange(spec string) (start, end int, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, nil
	}
	sep := "-"
	if strings.Contains(spec, ":") && !strings.Contains(spec, "-") {
		sep = ":"
	}
	a, b, hasSep := strings.Cut(spec, sep)
	if !hasSep {
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 {
			return 0, 0, fmt.Errorf("invalid line number %q", spec)
		}
		return n, n, nil
	}
	if a != "" {
		start, err = strconv.Atoi(a)
		if err != nil || start < 1 {
			return 0, 0, fmt.Errorf("invalid start line in %q", spec)
		}
	}
	if b != "" {
		end, err = strconv.Atoi(b)
		if err != nil || end < 1 {
			return 0, 0, fmt.Errorf("invalid end line in %q", spec)
		}
	}
	if start == 0 && end == 0 {
		return 0, 0, fmt.Errorf("invalid line range %q", spec)
	}
	if start > 0 && end > 0 && end < start {
		return 0, 0, fmt.Errorf("end line %d is before start line %d", end, start)
	}
	return start, end, nil
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func fmtBytes(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}
