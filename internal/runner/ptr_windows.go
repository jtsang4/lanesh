//go:build windows

package runner

import "unsafe"

func uintptrOf(p *byte) uintptr     { return uintptr(unsafe.Pointer(p)) }
func uintptrOf16(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }
