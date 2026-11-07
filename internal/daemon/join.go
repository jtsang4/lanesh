package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/version"
)

// JoinOptions describes a join request.
type JoinOptions struct {
	LaneURL string
	Key     string
	Name    string
	// AllowExec, when non-nil, sets the lane's exec policy override.
	AllowExec *bool
}

// JoinResult is returned after joining.
type JoinResult struct {
	Lane          LaneConfig
	Policy        protocol.Policy
	ServerVersion string
}

type joinResponse struct {
	LaneID        string `json:"lane_id"`
	LaneName      string `json:"lane_name"`
	MachineName   string `json:"machine_name"`
	Credential    string `json:"credential"`
	ServerVersion string `json:"server_version"`
}

// Join registers this machine with a lane and saves the configuration and credential.
func Join(ctx context.Context, p Paths, o JoinOptions) (JoinResult, error) {
	server, laneID, err := ParseLaneURL(o.LaneURL)
	if err != nil {
		return JoinResult{}, err
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		return JoinResult{}, err
	}
	id, err := MachineID(p)
	if err != nil {
		return JoinResult{}, err
	}
	lane := LaneConfig{Server: server, LaneID: laneID}
	if i := cfg.FindLane(lane.Key()); i >= 0 {
		lane = cfg.Lanes[i]
	}
	if o.AllowExec != nil {
		lane.Policy.AllowExec = o.AllowExec
	}
	policy := cfg.EffectivePolicy(lane)

	name := o.Name
	if name == "" {
		name = cfg.MachineName
	}
	hostname, _ := os.Hostname()
	if name == "" {
		name = hostname
	}
	creds, err := LoadCredentials(p)
	if err != nil {
		return JoinResult{}, err
	}
	body, _ := json.Marshal(map[string]any{
		"machine_id": id, "name": name, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"hostname": hostname, "version": version.Version, "policy": policy,
		"credential": creds[lane.Key()],
	})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/lanes/"+laneID+"/join", bytes.NewReader(body))
	if err != nil {
		return JoinResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(o.Key))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return JoinResult{}, fmt.Errorf("contact server: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return JoinResult{}, fmt.Errorf("join failed: %s", e.Error)
		}
		return JoinResult{}, fmt.Errorf("join failed: %s", resp.Status)
	}
	var jr joinResponse
	if err := json.Unmarshal(raw, &jr); err != nil {
		return JoinResult{}, fmt.Errorf("invalid server response: %w", err)
	}

	lane.LaneName, lane.MachineName = jr.LaneName, jr.MachineName
	if i := cfg.FindLane(lane.Key()); i >= 0 {
		cfg.Lanes[i] = lane
	} else {
		cfg.Lanes = append(cfg.Lanes, lane)
	}
	creds[lane.Key()] = jr.Credential
	if err := SaveCredentials(p, creds); err != nil {
		return JoinResult{}, err
	}
	if err := SaveConfig(p, cfg); err != nil {
		return JoinResult{}, err
	}
	return JoinResult{Lane: lane, Policy: policy, ServerVersion: jr.ServerVersion}, nil
}

// Leave removes a lane from the local configuration and asks the server to
// drop the membership. The local removal happens even if the server is unreachable.
func Leave(ctx context.Context, p Paths, ref string) (LaneConfig, error) {
	cfg, err := LoadConfig(p)
	if err != nil {
		return LaneConfig{}, err
	}
	i := cfg.FindLane(ref)
	if i < 0 {
		return LaneConfig{}, fmt.Errorf("this machine has not joined %q", ref)
	}
	lane := cfg.Lanes[i]
	creds, err := LoadCredentials(p)
	if err != nil {
		return LaneConfig{}, err
	}
	var serverErr error
	if cred := creds[lane.Key()]; cred != "" {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, lane.Server+"/lanes/"+lane.LaneID+"/membership", nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		if resp, err := http.DefaultClient.Do(req); err != nil {
			serverErr = err
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusUnauthorized {
				serverErr = fmt.Errorf("server answered %s", resp.Status)
			}
		}
	}
	delete(creds, lane.Key())
	cfg.Lanes = append(cfg.Lanes[:i], cfg.Lanes[i+1:]...)
	if err := SaveCredentials(p, creds); err != nil {
		return lane, err
	}
	if err := SaveConfig(p, cfg); err != nil {
		return lane, err
	}
	if serverErr != nil {
		return lane, fmt.Errorf("removed locally, but the server could not be notified: %w", serverErr)
	}
	return lane, nil
}
