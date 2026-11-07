// Package cli defines the lanesh command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/lanesh/internal/server"
	"github.com/jtsang4/lanesh/internal/version"
	"github.com/jtsang4/lanesh/web"
)

// Execute runs the root command and returns the process exit code.
func Execute() int {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "lanesh",
		Short:         "Share skills and shells across your machines through one MCP endpoint",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd(), newServerCmd(), newHealthcheckCmd())
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

func listenAddr() string {
	if v := os.Getenv("LANESH_LISTEN"); v != "" {
		return v
	}
	return ":8080"
}

func newServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Run the Lanesh server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cfg := server.Config{Listen: listenAddr()}
			if ui, ok := web.Assets(); ok {
				cfg.UI = ui
			}
			return server.Run(ctx, cfg)
		},
	}
}

func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Check that the local server is healthy (for container health checks)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, port, err := net.SplitHostPort(listenAddr())
			if err != nil {
				return fmt.Errorf("parse LANESH_LISTEN: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet,
				"http://"+net.JoinHostPort("127.0.0.1", port)+"/healthz", nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return errors.New("unhealthy: " + resp.Status)
			}
			return nil
		},
	}
}
