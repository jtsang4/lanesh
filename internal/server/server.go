// Package server implements the Lanesh HTTP server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/version"
)

// Config holds server settings.
type Config struct {
	Listen string
	// UI is the compiled management UI. A nil UI serves a placeholder page.
	UI fs.FS
}

// Handler builds the root HTTP handler.
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/", uiHandler(cfg.UI))
	return mux
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg Config) error {
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           Handler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("lanesh server listening", "addr", cfg.Listen, "version", version.Version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": version.Version,
	})
}

const placeholderPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Lanesh</title></head>
<body><p>The Lanesh UI has not been built. Run <code>make web</code> and rebuild.</p></body></html>`

// uiHandler serves the SPA, falling back to index.html for client-side routes.
func uiHandler(ui fs.FS) http.Handler {
	if ui == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholderPage))
		})
	}
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(ui, name); err != nil {
			http.ServeFileFS(w, r, ui, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}
