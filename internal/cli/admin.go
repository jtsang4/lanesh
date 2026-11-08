package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type adminConfig struct {
	Server string `toml:"server"`
	Token  string `toml:"token"`
}

type adminClient struct {
	server string
	token  string
	http   *http.Client
}

var adminFlags struct {
	server string
	token  string
}

func loadAdmin() (adminClient, error) {
	c := adminClient{server: adminFlags.server, token: adminFlags.token, http: &http.Client{Timeout: 30 * time.Second}}
	if c.server == "" {
		c.server = os.Getenv("LANESH_SERVER")
	}
	if c.token == "" {
		c.token = os.Getenv("LANESH_ADMIN_TOKEN")
	}
	if c.server == "" || c.token == "" {
		p, err := paths()
		if err != nil {
			return c, err
		}
		if raw, err := os.ReadFile(p.AdminConfig()); err == nil {
			var ac adminConfig
			if err := toml.Unmarshal(raw, &ac); err == nil {
				if c.server == "" {
					c.server = ac.Server
				}
				if c.token == "" {
					c.token = ac.Token
				}
			}
		}
	}
	if c.server == "" || c.token == "" {
		return c, errors.New("not logged in: run `lanesh admin login <server-url>` (or set LANESH_SERVER and LANESH_ADMIN_TOKEN)")
	}
	c.server = strings.TrimRight(c.server, "/")
	return c, nil
}

func (c adminClient) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+"/api/v1"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type apiLane struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	MachineCount int       `json:"machine_count"`
	OnlineCount  int       `json:"online_count"`
}

func (c adminClient) resolveLane(ctx context.Context, ref string) (apiLane, error) {
	var lanes []apiLane
	if err := c.do(ctx, http.MethodGet, "/lanes", nil, &lanes); err != nil {
		return apiLane{}, err
	}
	for _, l := range lanes {
		if l.ID == ref || strings.EqualFold(l.Name, ref) {
			return l, nil
		}
	}
	return apiLane{}, fmt.Errorf("no lane %q", ref)
}

func addAdminFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&adminFlags.server, "server", "", "server URL (default: from `lanesh admin login`)")
	cmd.PersistentFlags().StringVar(&adminFlags.token, "admin-token", "", "admin token (default: from `lanesh admin login`)")
}

func table(w io.Writer, header string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Log in to a server as its administrator"}
	var token string
	login := &cobra.Command{
		Use:   "login <server-url>",
		Short: "Save the server URL and admin token for lane, key and machine commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				fmt.Fprint(cmd.ErrOrStderr(), "Admin token: ")
				if term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // File descriptors fit in int.
					b, err := term.ReadPassword(int(os.Stdin.Fd())) //nolint:gosec // See above.
					fmt.Fprintln(cmd.ErrOrStderr())
					if err != nil {
						return err
					}
					token = string(b)
				} else {
					line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
					token = line
				}
			}
			c := adminClient{server: strings.TrimRight(args[0], "/"), token: strings.TrimSpace(token), http: &http.Client{Timeout: 30 * time.Second}}
			var info map[string]any
			if err := c.do(cmd.Context(), http.MethodGet, "/info", nil, &info); err != nil {
				return fmt.Errorf("login failed: %w", err)
			}
			p, err := paths()
			if err != nil {
				return err
			}
			raw, _ := toml.Marshal(adminConfig{Server: c.server, Token: c.token})
			if err := os.MkdirAll(p.Home, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(p.AdminConfig(), raw, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s (server %v).\n", c.server, info["version"])
			return nil
		},
	}
	login.Flags().StringVar(&token, "token", "", "admin token (prompted when omitted)")
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Forget the saved admin credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			if err := os.Remove(p.AdminConfig()); err != nil && !os.IsNotExist(err) {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return nil
		},
	}
	cmd.AddCommand(login, logout)
	return cmd
}

func newLaneCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "lane", Short: "Manage lanes on the server"}
	addAdminFlags(cmd)
	list := &cobra.Command{
		Use: "list", Short: "List lanes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			var lanes []apiLane
			if err := c.do(cmd.Context(), http.MethodGet, "/lanes", nil, &lanes); err != nil {
				return err
			}
			rows := make([][]string, 0, len(lanes))
			for _, l := range lanes {
				rows = append(rows, []string{l.ID, l.Name, fmt.Sprintf("%d/%d", l.OnlineCount, l.MachineCount), c.server + "/lanes/" + l.ID})
			}
			table(cmd.OutOrStdout(), "ID\tNAME\tONLINE\tURL", rows)
			return nil
		},
	}
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a lane", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			var l apiLane
			if err := c.do(cmd.Context(), http.MethodPost, "/lanes", map[string]string{"name": args[0]}, &l); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created lane %q.\nLane URL: %s/lanes/%s\nMCP URL:  %s/lanes/%s/mcp\n", l.Name, c.server, l.ID, c.server, l.ID)
			return nil
		},
	}
	rename := &cobra.Command{
		Use: "rename <lane> <name>", Short: "Rename a lane", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			l, err := c.resolveLane(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return c.do(cmd.Context(), http.MethodPatch, "/lanes/"+l.ID, map[string]string{"name": args[1]}, nil)
		},
	}
	var yes bool
	del := &cobra.Command{
		Use: "delete <lane>", Short: "Delete a lane, its keys and memberships", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			l, err := c.resolveLane(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("this deletes lane %q with %d machines and all its keys; pass --yes to confirm", l.Name, l.MachineCount)
			}
			if err := c.do(cmd.Context(), http.MethodDelete, "/lanes/"+l.ID, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted lane %q.\n", l.Name)
			return nil
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	cmd.AddCommand(list, create, rename, del)
	return cmd
}

type apiKey struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format(time.DateTime)
}

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Manage join keys and access keys of a lane"}
	addAdminFlags(cmd)
	list := &cobra.Command{
		Use: "list <lane>", Short: "List keys", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			l, err := c.resolveLane(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var keys []apiKey
			if err := c.do(cmd.Context(), http.MethodGet, "/lanes/"+l.ID+"/keys", nil, &keys); err != nil {
				return err
			}
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				state := "active"
				if k.RevokedAt != nil {
					state = "revoked"
				} else if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
					state = "expired"
				}
				rows = append(rows, []string{k.ID, k.Kind, k.Name, k.Prefix + "…", state, fmtTime(k.LastUsedAt)})
			}
			table(cmd.OutOrStdout(), "ID\tKIND\tNAME\tPREFIX\tSTATE\tLAST USED", rows)
			return nil
		},
	}
	var kind, name string
	var days int
	create := &cobra.Command{
		Use: "create <lane>", Short: "Create a join key or access key", Args: cobra.ExactArgs(1),
		Example: "  lanesh key create work --kind join --name laptop\n  lanesh key create work --kind access --name claude-code",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			l, err := c.resolveLane(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if name == "" {
				name = kind
			}
			var res struct {
				Key   apiKey `json:"key"`
				Token string `json:"token"`
			}
			if err := c.do(cmd.Context(), http.MethodPost, "/lanes/"+l.ID+"/keys", map[string]any{"kind": kind, "name": name, "expires_in_days": days}, &res); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Created %s key %q. It is shown only once:\n\n  %s\n\n", kind, name, res.Token)
			if kind == "join" {
				fmt.Fprintf(w, "Join a machine:\n  lanesh join %s/lanes/%s --key %s\n", c.server, l.ID, res.Token)
			} else {
				fmt.Fprintf(w, "Add to an agent (Claude Code, all projects):\n  claude mcp add --scope user --transport http lanesh %s/lanes/%s/mcp --header \"Authorization: Bearer %s\"\n", c.server, l.ID, res.Token)
			}
			return nil
		},
	}
	create.Flags().StringVar(&kind, "kind", "access", "join or access")
	create.Flags().StringVar(&name, "name", "", "a label for the key")
	create.Flags().IntVar(&days, "expires-in-days", 0, "expire the key after this many days (0 = never)")
	revoke := &cobra.Command{
		Use: "revoke <lane> <key-id>", Short: "Revoke a key", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			l, err := c.resolveLane(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.do(cmd.Context(), http.MethodDelete, "/lanes/"+l.ID+"/keys/"+args[1], nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Revoked.")
			return nil
		},
	}
	cmd.AddCommand(list, create, revoke)
	return cmd
}

func newMachineCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "machine", Short: "List or remove the machines of a lane"}
	addAdminFlags(cmd)
	list := &cobra.Command{
		Use: "list <lane>", Short: "List machines", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			l, err := c.resolveLane(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var ms []struct {
				ID         string    `json:"id"`
				Name       string    `json:"name"`
				OS         string    `json:"os"`
				Arch       string    `json:"arch"`
				Version    string    `json:"version"`
				Online     bool      `json:"online"`
				LastSeen   time.Time `json:"last_seen"`
				SkillCount int       `json:"skill_count"`
				Policy     struct {
					AllowExec bool `json:"allow_exec"`
				} `json:"policy"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, "/lanes/"+l.ID+"/machines", nil, &ms); err != nil {
				return err
			}
			rows := make([][]string, 0, len(ms))
			for _, m := range ms {
				state := "online"
				if !m.Online {
					state = "offline since " + m.LastSeen.Local().Format(time.DateTime)
				}
				rows = append(rows, []string{m.Name, m.OS + "/" + m.Arch, m.Version, fmt.Sprint(m.SkillCount), map[bool]string{true: "yes", false: "no"}[m.Policy.AllowExec], state, m.ID})
			}
			table(cmd.OutOrStdout(), "NAME\tPLATFORM\tVERSION\tSKILLS\tEXEC\tSTATE\tID", rows)
			return nil
		},
	}
	remove := &cobra.Command{
		Use: "remove <lane> <machine>", Short: "Remove a machine from a lane", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadAdmin()
			if err != nil {
				return err
			}
			l, err := c.resolveLane(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var ms []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, "/lanes/"+l.ID+"/machines", nil, &ms); err != nil {
				return err
			}
			for _, m := range ms {
				if m.ID == args[1] || strings.EqualFold(m.Name, args[1]) {
					if err := c.do(cmd.Context(), http.MethodDelete, "/lanes/"+l.ID+"/machines/"+m.ID, nil, nil); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s.\n", m.Name, l.Name)
					return nil
				}
			}
			return fmt.Errorf("no machine %q in lane %q", args[1], l.Name)
		},
	}
	cmd.AddCommand(list, remove)
	return cmd
}
