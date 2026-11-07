//go:build !windows

package service

import "os/exec"

func prepareChild(*exec.Cmd) {}

func bindChild(*exec.Cmd) {}
