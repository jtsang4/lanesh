//go:build !windows

package runner

func decodeANSI([]byte) (string, bool) { return "", false }
