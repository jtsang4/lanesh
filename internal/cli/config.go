package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/jtsang4/lanesh/internal/daemon"
)

var configKeys = []string{"machine_name", "policy.expose_skills", "policy.allow_exec", "update.auto", "update.source"}

func setConfig(c *daemon.Config, key string, value *string) error {
	parseBool := func() (*bool, error) {
		if value == nil {
			return nil, nil
		}
		b, err := strconv.ParseBool(*value)
		if err != nil {
			return nil, fmt.Errorf("%s must be true or false", key)
		}
		return &b, nil
	}
	str := func() string {
		if value == nil {
			return ""
		}
		return *value
	}
	var err error
	switch key {
	case "machine_name":
		c.MachineName = str()
	case "policy.expose_skills":
		c.Policy.ExposeSkills, err = parseBool()
	case "policy.allow_exec":
		c.Policy.AllowExec, err = parseBool()
	case "update.auto":
		c.Update.Auto, err = parseBool()
	case "update.source":
		c.Update.Source = str()
	default:
		return fmt.Errorf("unknown key %q (known: %v)", key, configKeys)
	}
	return err
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change the local configuration",
		Long: `Show or change the local configuration. The running daemon picks up changes
within a few seconds.

Keys:
  machine_name           name used when joining new lanes
  policy.expose_skills   share skills with lanes (default true)
  policy.allow_exec      allow commands and file access (default true)
  update.auto            update automatically to the server's version (default true)
  update.source          base URL for release downloads`,
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Print the configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			c, err := daemon.LoadConfig(p)
			if err != nil {
				return err
			}
			raw, err := toml.Marshal(c)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# %s\n%s", p.Config(), raw)
			return nil
		},
	}
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return updateConfig(args[0], &args[1])
		},
	}
	unset := &cobra.Command{
		Use:   "unset <key>",
		Short: "Reset a configuration value to its default",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return updateConfig(args[0], nil)
		},
	}
	cmd.AddCommand(show, set, unset)
	return cmd
}

func updateConfig(key string, value *string) error {
	p, err := paths()
	if err != nil {
		return err
	}
	c, err := daemon.LoadConfig(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := setConfig(&c, key, value); err != nil {
		return err
	}
	return daemon.SaveConfig(p, c)
}
