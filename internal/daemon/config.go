// Package daemon implements the per-machine Lanesh daemon and its local state.
package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
)

// Paths locates the daemon's local files.
type Paths struct {
	Home string
}

// DefaultPaths returns paths under LANESH_HOME or ~/.lanesh.
func DefaultPaths() (Paths, error) {
	if h := os.Getenv("LANESH_HOME"); h != "" {
		return Paths{Home: h}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{Home: filepath.Join(home, ".lanesh")}, nil
}

// Config returns the config file path.
func (p Paths) Config() string { return filepath.Join(p.Home, "config.toml") }

// Credentials returns the credentials file path.
func (p Paths) Credentials() string { return filepath.Join(p.Home, "credentials.toml") }

// MachineIDFile returns the machine ID file path.
func (p Paths) MachineIDFile() string { return filepath.Join(p.Home, "machine-id") }

// LogDir returns the log directory.
func (p Paths) LogDir() string { return filepath.Join(p.Home, "logs") }

// DaemonLog returns the daemon log file.
func (p Paths) DaemonLog() string { return filepath.Join(p.LogDir(), "daemon.log") }

// AuditLog returns the local audit log file.
func (p Paths) AuditLog() string { return filepath.Join(p.LogDir(), "audit.log") }

// State returns the daemon state file.
func (p Paths) State() string { return filepath.Join(p.Home, "state.json") }

// Lock returns the single-instance lock file.
func (p Paths) Lock() string { return filepath.Join(p.Home, "daemon.lock") }

// BinDir returns the managed binary directory.
func (p Paths) BinDir() string { return filepath.Join(p.Home, "bin") }

// Binary returns the managed binary path.
func (p Paths) Binary() string {
	name := "lanesh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(p.BinDir(), name)
}

// AdminConfig returns the CLI's admin credentials file.
func (p Paths) AdminConfig() string { return filepath.Join(p.Home, "admin.toml") }

// PolicyConfig holds optional policy settings; nil means "use the default".
type PolicyConfig struct {
	ExposeSkills *bool `toml:"expose_skills,omitempty"`
	AllowExec    *bool `toml:"allow_exec,omitempty"`
}

// UpdateConfig controls self-update.
type UpdateConfig struct {
	Auto   *bool  `toml:"auto,omitempty"`
	Source string `toml:"source,omitempty"`
}

// LaneConfig is a lane this machine has joined.
type LaneConfig struct {
	Server      string       `toml:"server"`
	LaneID      string       `toml:"lane_id"`
	LaneName    string       `toml:"lane_name,omitempty"`
	MachineName string       `toml:"machine_name,omitempty"`
	Policy      PolicyConfig `toml:"policy,omitempty"`
}

// Key identifies a lane membership in the credentials file.
func (l LaneConfig) Key() string { return strings.TrimRight(l.Server, "/") + "/lanes/" + l.LaneID }

// Config is the daemon configuration file.
type Config struct {
	MachineName string       `toml:"machine_name,omitempty"`
	Policy      PolicyConfig `toml:"policy"`
	Update      UpdateConfig `toml:"update"`
	Lanes       []LaneConfig `toml:"lanes"`
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// EffectivePolicy returns the effective policy for a lane: lane overrides, then global
// settings, then defaults (everything allowed).
func (c Config) EffectivePolicy(lane LaneConfig) protocol.Policy {
	return protocol.Policy{
		ExposeSkills: boolOr(lane.Policy.ExposeSkills, boolOr(c.Policy.ExposeSkills, true)),
		AllowExec:    boolOr(lane.Policy.AllowExec, boolOr(c.Policy.AllowExec, true)),
	}
}

// AutoUpdate reports whether self-update is enabled.
func (c Config) AutoUpdate() bool { return boolOr(c.Update.Auto, true) }

// FindLane returns the index of the lane matching ref (ID, name or URL).
func (c Config) FindLane(ref string) int {
	ref = strings.TrimRight(strings.TrimSpace(ref), "/")
	for i, l := range c.Lanes {
		if l.LaneID == ref || strings.EqualFold(l.LaneName, ref) || l.Key() == ref {
			return i
		}
	}
	return -1
}

// LoadConfig reads the config file; a missing file yields an empty config.
func LoadConfig(p Paths) (Config, error) {
	var c Config
	raw, err := os.ReadFile(p.Config())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := toml.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", p.Config(), err)
	}
	return c, nil
}

// SaveConfig writes the config file atomically.
func SaveConfig(p Paths, c Config) error {
	raw, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return writePrivate(p.Config(), raw, 0o644)
}

type credentialsFile struct {
	Credentials map[string]string `toml:"credentials"`
}

// LoadCredentials reads machine credentials keyed by LaneConfig.Key.
func LoadCredentials(p Paths) (map[string]string, error) {
	var f credentialsFile
	raw, err := os.ReadFile(p.Credentials())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.Credentials(), err)
	}
	if f.Credentials == nil {
		f.Credentials = map[string]string{}
	}
	return f.Credentials, nil
}

// SaveCredentials writes machine credentials with owner-only permissions.
func SaveCredentials(p Paths, creds map[string]string) error {
	raw, err := toml.Marshal(credentialsFile{Credentials: creds})
	if err != nil {
		return err
	}
	return writePrivate(p.Credentials(), raw, 0o600)
}

// writePrivate atomically replaces path with data. The temporary file is
// unique, so concurrent writers never clobber each other's partial files, and
// the destination is never removed before the new content is in place.
func writePrivate(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) //nolint:errcheck // No-op after a successful rename.
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	// On Windows a rename can fail briefly while another process reads the file.
	for i := 0; ; i++ {
		err = os.Rename(tmp, path)
		if err == nil || i == 10 {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// MachineID returns the persistent machine ID, creating it on first use.
func MachineID(p Paths) (string, error) {
	raw, err := os.ReadFile(p.MachineIDFile())
	if err == nil {
		if id := strings.TrimSpace(string(raw)); strings.HasPrefix(id, "mch_") {
			return id, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	id := auth.NewID("mch_")
	if err := writePrivate(p.MachineIDFile(), []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// ParseLaneURL splits https://host/lanes/<id> into the server base URL and lane ID.
// Plain HTTP is only accepted for loopback hosts.
func ParseLaneURL(raw string) (server, laneID string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("invalid lane URL %q", raw)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "lanes" || parts[len(parts)-1] == "" {
		return "", "", fmt.Errorf("invalid lane URL %q: expected https://<server>/lanes/<lane-id>", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return "", "", fmt.Errorf("refusing plain HTTP for %s; use https", u.Host)
		}
	default:
		return "", "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	prefix := strings.Join(parts[:len(parts)-2], "/")
	base := u.Scheme + "://" + u.Host
	if prefix != "" {
		base += "/" + prefix
	}
	return base, parts[len(parts)-1], nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}
