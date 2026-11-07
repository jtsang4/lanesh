//go:build !windows

package daemon

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked is returned when another daemon holds the lock.
var ErrLocked = errors.New("another lanesh daemon is already running")

// Lock takes an exclusive, process-lifetime lock on path.
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // Path is inside the Lanesh home.
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // File descriptors fit in int.
		_ = f.Close()
		return nil, ErrLocked
	}
	return func() { _ = f.Close() }, nil
}
