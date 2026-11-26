//go:build windows

package cli

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/jtsang4/lanesh/internal/daemon"
)

var (
	user32DLL           = windows.NewLazySystemDLL("user32.dll")
	sendMessageTimeoutW = user32DLL.NewProc("SendMessageTimeoutW")
)

func exposeManagedBinary(p daemon.Paths) pathExposeResult {
	home, _ := os.UserHomeDir()
	if modifyPathDisabled(os.Getenv("LANESH_NO_MODIFY_PATH")) || !isDefaultHome(p, home) {
		return pathExposeResult{Kind: pathSkipped, BinDir: p.BinDir()}
	}
	pathEnv := os.Getenv("PATH")
	if pathContainsDir(pathEnv, p.BinDir()) {
		return pathExposeResult{Kind: pathNoop}
	}
	if found := findLaneshInPath(pathEnv); found != "" {
		if isSameExecutable(found, p.Binary()) || isPersistentNPMLauncher(found) {
			return pathExposeResult{Kind: pathNoop}
		}
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return pathExposeResult{Kind: pathManualHint, BinDir: p.BinDir()}
	}
	defer k.Close()

	userPath, valType, err := k.GetStringValue("Path")
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			return pathExposeResult{Kind: pathManualHint, BinDir: p.BinDir()}
		}
		userPath = ""
		valType = registry.EXPAND_SZ
	}

	updated, added := addWindowsPathEntry(userPath, p.BinDir())
	if !added {
		return pathExposeResult{Kind: pathWindowsExists, BinDir: p.BinDir()}
	}

	if valType == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", updated)
	} else {
		err = k.SetStringValue("Path", updated)
	}
	if err != nil {
		return pathExposeResult{Kind: pathManualHint, BinDir: p.BinDir()}
	}
	broadcastEnvironmentChange()
	return pathExposeResult{Kind: pathWindowsAdded, BinDir: p.BinDir()}
}

func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	envPtr, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = sendMessageTimeoutW.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(envPtr)), //nolint:gosec // Win32 SendMessageTimeoutW expects a UTF-16 string pointer.
		smtoAbortIfHung,
		5000,
		uintptr(unsafe.Pointer(&result)), //nolint:gosec // Output parameter for Win32 SendMessageTimeoutW.
	)
}
