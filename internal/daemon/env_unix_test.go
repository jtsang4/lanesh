//go:build !windows

package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestParseEnvIgnoresShellNoise(t *testing.T) {
	out := "Welcome to my shell!\nPATH=/bad\n" + envMarker + "\x00PATH=/usr/bin:/opt/bin\x00HOME=/home/u\x00SHLVL=2\x00MULTI=a\nb\x00bad key=x\x00"
	env := parseEnv(out)
	want := []string{"PATH=/usr/bin:/opt/bin", "HOME=/home/u", "MULTI=a\nb"}
	if !slices.Equal(env, want) {
		t.Fatalf("env = %q, want %q", env, want)
	}
	merged := mergeEnv(env, []string{"PATH=/other", "LANG=C"})
	if !slices.Contains(merged, "LANG=C") || slices.Contains(merged, "PATH=/other") {
		t.Fatalf("merged = %q", merged)
	}
}

func TestCaptureEnvReturnsPath(t *testing.T) {
	env := CaptureEnv(context.Background())
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") && len(kv) > 5 {
			found = true
		}
	}
	if !found {
		t.Fatal("captured environment has no PATH")
	}
}
