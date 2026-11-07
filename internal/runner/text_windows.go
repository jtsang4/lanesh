//go:build windows

package runner

import (
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

var procMultiByteToWideChar = windows.NewLazySystemDLL("kernel32.dll").NewProc("MultiByteToWideChar")

// decodeANSI decodes b with the active ANSI code page (for example 936/GBK).
func decodeANSI(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", true
	}
	const cpACP = 0
	n, _, _ := procMultiByteToWideChar.Call(cpACP, 0, uintptrOf(&b[0]), uintptr(len(b)), 0, 0)
	if n == 0 {
		return "", false
	}
	out := make([]uint16, n)
	r, _, _ := procMultiByteToWideChar.Call(cpACP, 0, uintptrOf(&b[0]), uintptr(len(b)), uintptrOf16(&out[0]), n)
	if r == 0 {
		return "", false
	}
	return string(utf16.Decode(out[:r])), true
}
