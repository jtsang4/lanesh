package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
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

// envNumber reads a numeric setting from the environment. A value that is not
// a number is recorded in errs under its flag, so that the command fails
// instead of quietly using the default, unless the flag is given.
func envNumber(key, flag string, def int64, errs map[string]error) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		errs[flag] = fmt.Errorf("%s=%q is not a whole number: %w", key, v, err)
		return def
	}
	return n
}

// envBool reads a true/false setting from the environment, like envNumber.
func envBool(key, flag string, def bool, errs map[string]error) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		errs[flag] = fmt.Errorf("%s=%q is not true or false: %w", key, v, err)
		return def
	}
	return b
}

func setupLogging(level string, w *os.File) {
	var l slog.Level
	_ = l.UnmarshalText([]byte(strings.ToUpper(level)))
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l})))
}

// maxRetentionDays keeps the retention duration within time.Duration.
const maxRetentionDays = 100 * 365

func newServerCmd() *cobra.Command {
	var cfg server.Config
	var retentionDays int
	var logLevel string
	var transferGzip bool
	envErrs := map[string]error{}
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the Lanesh server",
		Long: `Run the Lanesh server. Settings can be given as flags or environment variables:

  LANESH_LISTEN               listen address (default :8080)
  LANESH_DATA_DIR             data directory (default ./data)
  LANESH_PUBLIC_URL           public URL, used in install snippets
  LANESH_ADMIN_TOKEN          admin token (generated on first start when unset)
  LANESH_TRANSFER_MAX_BYTES   maximum size of one transfer (default unlimited)
  LANESH_TRANSFER_PART_BYTES  largest upload request of a transfer (default 16 MiB)
  LANESH_TRANSFER_GZIP        compress transfers between current daemons (default true)
  LANESH_AUDIT_RETENTION_DAYS audit log retention (default 90)
  LANESH_LOG_LEVEL            debug, info, warn or error (default info)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, flag := range slices.Sorted(maps.Keys(envErrs)) {
				if !cmd.Flags().Changed(flag) {
					return envErrs[flag]
				}
			}
			cfg.DisableTransferGzip = !transferGzip
			// Zero means the default in server.Config; given here it is a
			// mistake, so check the value before New fills it in.
			if err := server.ValidateTransferPartBytes(cfg.TransferPartBytes); err != nil {
				return err
			}
			if retentionDays < 0 || retentionDays > maxRetentionDays {
				return fmt.Errorf("the audit retention must be between 0 and %d days", maxRetentionDays)
			}
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
	maxBytes := envNumber("LANESH_TRANSFER_MAX_BYTES", "transfer-max-bytes", 0, envErrs)
	partBytes := envNumber("LANESH_TRANSFER_PART_BYTES", "transfer-part-bytes", server.DefaultTransferPartBytes, envErrs)
	gzipOn := envBool("LANESH_TRANSFER_GZIP", "transfer-gzip", true, envErrs)
	retention := envNumber("LANESH_AUDIT_RETENTION_DAYS", "audit-retention-days", 90, envErrs)
	if retention < 0 || retention > maxRetentionDays {
		envErrs["audit-retention-days"] = fmt.Errorf("LANESH_AUDIT_RETENTION_DAYS must be between 0 and %d", maxRetentionDays)
		retention = 90
	}
	f.StringVar(&cfg.Listen, "listen", envOr("LANESH_LISTEN", ":8080"), "listen address")
	f.StringVar(&cfg.DataDir, "data-dir", envOr("LANESH_DATA_DIR", "data"), "data directory")
	f.StringVar(&cfg.PublicURL, "public-url", os.Getenv("LANESH_PUBLIC_URL"), "public URL of this server")
	f.StringVar(&cfg.AdminToken, "admin-token", os.Getenv("LANESH_ADMIN_TOKEN"), "admin token")
	f.Int64Var(&cfg.TransferMaxBytes, "transfer-max-bytes", maxBytes, "maximum bytes per transfer (0 = unlimited)")
	f.Int64Var(&cfg.TransferPartBytes, "transfer-part-bytes", partBytes, "largest upload request of a transfer; keep it below any proxy's request size limit")
	f.BoolVar(&transferGzip, "transfer-gzip", gzipOn, "compress transfers between daemons that support it")
	f.IntVar(&retentionDays, "audit-retention-days", int(retention), "days to keep audit records (0 = forever)")
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
