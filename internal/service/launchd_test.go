//go:build darwin

package service

import "testing"

func TestParseLaunchctlPrint(t *testing.T) {
	out := "gui/501/dev.lanesh.daemon = {\n\tactive count = 1\n\tstate = running\n\tprogram = /x/lanesh\n\tpid = 23165\n\tendpoints = {\n\t\t\"x\" = {\n\t\t\tstate = active\n\t\t}\n\t}\n\t\tstate = active\n}\n"
	running, pid, state := parseLaunchctlPrint(out)
	if !running || pid != 23165 || state != "running" {
		t.Fatalf("got %v %d %q", running, pid, state)
	}
	running, _, state = parseLaunchctlPrint("x = {\n\tstate = not running\n}\n")
	if running || state != "not running" {
		t.Fatalf("got %v %q", running, state)
	}
}
