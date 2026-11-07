package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/lanesh/internal/server"
	"github.com/jtsang4/lanesh/web"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setupLogging(level string, w *os.File) {
	var l slog.Level
	_ = l.UnmarshalText([]byte(strings.ToUpper(level)))
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l})))
}

func newServerCmd() *cobra.Command {
	var cfg server.Config
	var retentionDays int
	var logLevel string
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the Lanesh server",
		Long: `Run the Lanesh server. Settings can be given as flags or environment variables:

  LANESH_LISTEN               listen address (default :8080)
  LANESH_DATA_DIR             data directory (default ./data)
  LANESH_PUBLIC_URL           public URL, used in install snippets
  LANESH_ADMIN_TOKEN          admin token (generated on first start when unset)
  LANESH_TRANSFER_MAX_BYTES   maximum size of one transfer (default unlimited)
  LANESH_AUDIT_RETENTION_DAYS audit log retention (default 90)
  LANESH_LOG_LEVEL            debug, info, warn or error (default info)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogging(logLevel, os.Stderr)
			cfg.AuditRetention = time.Duration(retentionDays) * 24 * time.Hour
			if ui, ok := web.Assets(); ok {
				cfg.UI = ui
			} else {
				slog.Warn("the web UI is not embedded in this build")
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			s, generated, err := server.New(ctx, cfg)
			if err != nil {
				return err
			}
			defer s.Close() //nolint:errcheck
			if generated != "" {
				fmt.Fprintf(os.Stderr, "\n  Generated admin token (shown only once, store it safely):\n\n    %s\n\n", generated)
			}
			if cfg.PublicURL == "" {
				slog.Warn("LANESH_PUBLIC_URL is not set; install snippets will use the request host")
			}
			return s.Run(ctx)
		},
	}
	f := cmd.Flags()
	maxBytes, _ := strconv.ParseInt(envOr("LANESH_TRANSFER_MAX_BYTES", "0"), 10, 64)
	retention, _ := strconv.Atoi(envOr("LANESH_AUDIT_RETENTION_DAYS", "90"))
	f.StringVar(&cfg.Listen, "listen", envOr("LANESH_LISTEN", ":8080"), "listen address")
	f.StringVar(&cfg.DataDir, "data-dir", envOr("LANESH_DATA_DIR", "data"), "data directory")
	f.StringVar(&cfg.PublicURL, "public-url", os.Getenv("LANESH_PUBLIC_URL"), "public URL of this server")
	f.StringVar(&cfg.AdminToken, "admin-token", os.Getenv("LANESH_ADMIN_TOKEN"), "admin token")
	f.Int64Var(&cfg.TransferMaxBytes, "transfer-max-bytes", maxBytes, "maximum bytes per transfer (0 = unlimited)")
	f.IntVar(&retentionDays, "audit-retention-days", retention, "days to keep audit records (0 = forever)")
	f.StringVar(&logLevel, "log-level", envOr("LANESH_LOG_LEVEL", "info"), "log level")
	return cmd
}

func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Check that the local server is healthy (for container health checks)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, port, err := net.SplitHostPort(envOr("LANESH_LISTEN", ":8080"))
			if err != nil {
				return fmt.Errorf("parse LANESH_LISTEN: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", port)+"/healthz", nil)
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
