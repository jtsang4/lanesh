package runner

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// cmds returns OS-appropriate commands for the default shell.
func cmds() (echo, stderr, exit3, sleep, readLine string) { //nolint:unparam // exit3 is the same on every OS.
	if runtime.GOOS == "windows" {
		return "Write-Output hello", "[Console]::Error.WriteLine('oops')", "exit 3",
			"Start-Sleep -Seconds 30", "$l = [Console]::In.ReadLine(); Write-Output \"got $l\""
	}
	return "echo hello", "echo oops >&2", "exit 3", "sleep 30", "read l; echo \"got $l\""
}

func TestExecCapturesOutputAndExitCode(t *testing.T) {
	m := NewManager(Options{})
	echo, stderr, exit3, _, _ := cmds()
	ctx := context.Background()

	st, err := m.Start(ctx, protocol.ExecParams{Command: echo}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != protocol.JobExited || *st.ExitCode != 0 || strings.TrimSpace(st.Stdout) != "hello" {
		t.Fatalf("st = %+v", st)
	}

	st, _ = m.Start(ctx, protocol.ExecParams{Command: stderr}, "")
	if strings.TrimSpace(st.Stderr) != "oops" {
		t.Fatalf("stderr = %q", st.Stderr)
	}

	st, _ = m.Start(ctx, protocol.ExecParams{Command: exit3}, "")
	if *st.ExitCode != 3 {
		t.Fatalf("exit code = %d", *st.ExitCode)
	}
}

func TestLongJobContinuesInBackgroundAndCanBeKilled(t *testing.T) {
	var mu sync.Mutex
	var exited []protocol.JobExitedEvent
	m := NewManager(Options{OnExit: func(e protocol.JobExitedEvent) { mu.Lock(); exited = append(exited, e); mu.Unlock() }})
	_, _, _, sleep, _ := cmds()

	st, err := m.Start(context.Background(), protocol.ExecParams{Command: sleep, TimeoutMS: 300, Owner: "k1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != protocol.JobRunning || st.ExitCode != nil {
		t.Fatalf("st = %+v", st)
	}
	if got := m.List("k1"); len(got) != 1 || got[0].JobID != st.JobID {
		t.Fatalf("list = %+v", got)
	}
	if got := m.List("other"); len(got) != 0 {
		t.Fatalf("list other = %+v", got)
	}

	start := time.Now()
	killed, err := m.Kill(protocol.JobKillParams{JobID: st.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if killed.Status != protocol.JobKilled || time.Since(start) > 6*time.Second {
		t.Fatalf("killed = %+v", killed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(exited) != 1 || exited[0].Status != protocol.JobKilled {
		t.Fatalf("exited = %+v", exited)
	}
}

func TestBackgroundJobStdin(t *testing.T) {
	m := NewManager(Options{})
	_, _, _, _, readLine := cmds()
	st, err := m.Start(context.Background(), protocol.ExecParams{Command: readLine, Background: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Write(protocol.JobWriteParams{JobID: st.JobID, Data: "ping\n", CloseStdin: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := m.Read(protocol.JobReadParams{JobID: st.JobID})
		if err != nil {
			t.Fatal(err)
		}
		if r.EOF {
			if strings.TrimSpace(r.Data) != "got ping" {
				t.Fatalf("data = %q", r.Data)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}

func TestPTYUnsupported(t *testing.T) {
	m := NewManager(Options{})
	if _, err := m.Start(context.Background(), protocol.ExecParams{Command: "x", PTY: true}, ""); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestRingAndInlineTruncation(t *testing.T) {
	r := newRing(10)
	_, _ = r.Write([]byte("0123456789abcdef"))
	data, start, total, dropped := r.ReadAt(0, 4)
	if string(data) != "6789" || start != 6 || total != 16 || dropped != 6 {
		t.Fatalf("got %q %d %d %d", data, start, total, dropped)
	}

	big := newRing(1 << 20)
	_, _ = big.Write([]byte(strings.Repeat("a", 500) + strings.Repeat("b", 500)))
	text, total, truncated := inline(big, 100)
	if !truncated || total != 1000 || !strings.HasPrefix(text, strings.Repeat("a", 50)) ||
		!strings.HasSuffix(text, strings.Repeat("b", 50)) || !strings.Contains(text, "900 bytes omitted") {
		t.Fatalf("inline = %q", text)
	}
}
