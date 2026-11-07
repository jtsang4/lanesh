//go:build !linux

package service

// LingerEnabled is only meaningful on Linux.
func LingerEnabled() (bool, error) { return true, nil }
