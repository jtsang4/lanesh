package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// auditLog appends JSON lines describing operations performed on this machine.
type auditLog struct {
	mu   sync.Mutex
	path string
}

func newAuditLog(path string) *auditLog { return &auditLog{path: path} }

func (a *auditLog) write(action string, detail map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(a.path), 0o700)
	rotate(a.path, 10<<20)
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // Path is inside the Lanesh home.
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "action": action, "detail": detail})
	_, _ = f.Write(append(line, '\n'))
}

// rotate renames path to path.1 when it exceeds maxBytes.
func rotate(path string, maxBytes int64) {
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
}

// OpenLog opens the daemon log file for appending, rotating it when large.
func OpenLog(p Paths) (*os.File, error) {
	if err := os.MkdirAll(p.LogDir(), 0o700); err != nil {
		return nil, err
	}
	rotate(p.DaemonLog(), 10<<20)
	return os.OpenFile(p.DaemonLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}
