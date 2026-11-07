//go:build windows

// The Win32 calls in text_windows.go take raw pointers.

package runner

import "unsafe"

func uintptrOf(p *byte) uintptr     { return uintptr(unsafe.Pointer(p)) } // #nosec G103 -- Win32 API
func uintptrOf16(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) } // #nosec G103 -- Win32 API
