// Package server implements the Lanesh server: daemon connections, the MCP
// endpoint, the management API and the embedded UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/store"
	"github.com/jtsang4/lanesh/internal/version"
)

// Config holds server settings.
type Config struct {
	Listen    string
	DataDir   string
	PublicURL string
	// AdminToken overrides the stored admin token when set.
	AdminToken       string
	TransferMaxBytes int64
	AuditRetention   time.Duration
	// UI is the compiled management UI. A nil UI serves a placeholder page.
	UI fs.FS
}

// Server is a running Lanesh server.
type Server struct {
	cfg       Config
	store     *store.Store
	hub       *Hub
	briefings *auth.Briefings
	transfers *transfers
	adminHash string
	started   time.Time
	limiter   *limiter
	mcp       http.Handler
}

// New opens the store and prepares the server. It returns the generated
// admin token when one had to be created (empty otherwise).
func New(ctx context.Context, cfg Config) (*Server, string, error) {
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return nil, "", err
	}
	return newWithStore(ctx, cfg, st)
}

func newWithStore(ctx context.Context, cfg Config, st *store.Store) (*Server, string, error) {
	secret, err := st.Secret(ctx, "briefing")
	if err != nil {
		return nil, "", err
	}
	s := &Server{
		cfg: cfg, store: st, hub: NewHub(), briefings: auth.NewBriefings(secret),
		transfers: newTransfers(cfg.TransferMaxBytes), started: time.Now(), limiter: newLimiter(10, time.Minute),
	}
	generated, err := s.initAdminToken(ctx)
	if err != nil {
		return nil, "", err
	}
	s.mcp = s.mcpHandler()
	return s, generated, nil
}

func (s *Server) initAdminToken(ctx context.Context) (string, error) {
	if s.cfg.AdminToken != "" {
		s.adminHash = auth.Hash(s.cfg.AdminToken)
		return "", nil
	}
	h, err := s.store.MetaGet(ctx, "admin_token_hash")
	if err == nil {
		s.adminHash = h
		return "", nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	token := auth.NewToken(auth.PrefixAdmin)
	if err := s.store.MetaSet(ctx, "admin_token_hash", auth.Hash(token)); err != nil {
		return "", err
	}
	s.adminHash = auth.Hash(token)
	return token, nil
}

// Close releases resources.
func (s *Server) Close() error { return s.store.Close() }

// Handler builds the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /install.sh", s.installScript)
	mux.HandleFunc("GET /install.ps1", s.installScript)

	mux.HandleFunc("POST /lanes/{lane}/join", s.handleJoin)
	mux.HandleFunc("GET /lanes/{lane}/connect", s.handleConnect)
	mux.HandleFunc("DELETE /lanes/{lane}/membership", s.handleLeave)
	mux.Handle("/lanes/{lane}/mcp", s.mcp)

	mux.HandleFunc("POST /transfers/{id}/upload", s.transfers.upload)
	mux.HandleFunc("GET /transfers/{id}/download", s.transfers.download)

	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", s.adminAPI()))
	mux.Handle("/", uiHandler(s.cfg.UI))
	return logRequests(mux)
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go s.maintenance(ctx)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("lanesh server listening", "addr", s.cfg.Listen, "version", version.Version, "data_dir", s.cfg.DataDir)
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
		for _, lane := range s.lanesSnapshot() {
			s.hub.Disconnect(lane, "", "server shutting down")
		}
		return srv.Shutdown(shutdownCtx)
	}
}

func (s *Server) lanesSnapshot() []string {
	s.hub.mu.RLock()
	defer s.hub.mu.RUnlock()
	out := make([]string, 0, len(s.hub.conns))
	for id := range s.hub.conns {
		out = append(out, id)
	}
	return out
}

func (s *Server) maintenance(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := s.store.Prune(ctx, s.cfg.AuditRetention); err != nil && ctx.Err() == nil {
			slog.Warn("prune failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": version.Version})
}

func (s *Server) audit(ctx context.Context, e store.AuditEntry) {
	if err := s.store.AddAudit(context.WithoutCancel(ctx), e); err != nil {
		slog.Warn("audit write failed", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// bearer extracts a bearer token from the Authorization header or ?key=.
func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if tok, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(tok)
		}
	}
	return r.URL.Query().Get("key")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests logs API and daemon requests at debug level without query strings.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/lanes/") || strings.HasPrefix(r.URL.Path, "/transfers/") {
			slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start))
		}
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
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if name == "" || name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, ui, "index.html")
			return
		}
		if _, err := fs.Stat(ui, name); err != nil {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, ui, "index.html")
			return
		}
		if strings.HasPrefix(name, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
