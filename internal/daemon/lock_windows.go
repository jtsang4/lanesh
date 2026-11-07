//go:build windows

package daemon

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// ErrLocked is returned when another daemon holds the lock.
var ErrLocked = errors.New("another lanesh daemon is already running")

// Lock takes an exclusive, process-lifetime lock on path.
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // Path is inside the Lanesh home.
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		_ = f.Close()
		return nil, ErrLocked
	}
	return func() { _ = f.Close() }, nil
}
