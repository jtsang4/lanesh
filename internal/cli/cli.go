// Package cli defines the lanesh command tree.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jtsang4/lanesh/internal/daemon"
	"github.com/jtsang4/lanesh/internal/service"
	"github.com/jtsang4/lanesh/internal/version"
)

// exitError carries a specific process exit code.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// Execute runs the root command and returns the process exit code.
func Execute() int {
	err := newRootCmd().Execute()
	var ee exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lanesh",
		Short: "Share skills and shells across your machines through one MCP endpoint",
		Long: `Lanesh connects your machines to a self-hosted server so that AI agents can
discover the skills installed on each machine, run commands and move files
between them through one MCP endpoint.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddGroup(
		&cobra.Group{ID: "machine", Title: "Machine commands:"},
		&cobra.Group{ID: "admin", Title: "Administration:"},
		&cobra.Group{ID: "server", Title: "Server:"},
	)
	for _, c := range []*cobra.Command{
		newJoinCmd(), newLeaveCmd(), newStatusCmd(), newLogsCmd(), newStartCmd(), newStopCmd(), newRestartCmd(),
		newUpdateCmd(), newDoctorCmd(), newConfigCmd(),
	} {
		c.GroupID = "machine"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{newAdminCmd(), newLaneCmd(), newKeyCmd(), newMachineCmd()} {
		c.GroupID = "admin"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{newServerCmd(), newHealthcheckCmd()} {
		c.GroupID = "server"
		root.AddCommand(c)
	}
	root.AddCommand(newVersionCmd(), newDaemonCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			if short {
				fmt.Fprintln(cmd.OutOrStdout(), version.Version)
				return
			}
			fmt.Fprintln(cmd.OutOrStdout(), version.String())
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print only the version number")
	return cmd
}

func paths() (daemon.Paths, error) { return daemon.DefaultPaths() }

func serviceManager() (service.Manager, error) { return service.New() }
