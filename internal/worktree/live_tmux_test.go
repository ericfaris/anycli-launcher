package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestTmuxLiveness checks a real tmux session on a private socket (skipped
// without tmux unless AC_REQUIRE_TMUX is set): alive is running, quiet
// past IdleAfter is idle, killed is finished.
func TestTmuxLiveness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no tmux on Windows")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("AC_REQUIRE_TMUX") != "" {
			t.Fatal("AC_REQUIRE_TMUX is set but tmux is not installed")
		}
		t.Skip("tmux not installed")
	}
	base := t.TempDir()
	t.Setenv("TMUX_TMPDIR", base)
	t.Setenv("TMUX", "")
	t.Setenv("HOME", base)
	sock := fmt.Sprintf("ac-live-%d", os.Getpid())
	t.Cleanup(func() { exec.Command(tmux, "-L", sock, "kill-server").Run() })
	if out, err := exec.Command(tmux, "-L", sock, "-f", os.DevNull, "new-session", "-d", "-s", "api-fix", "sleep", "60").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	p := Probe{TmuxSocket: sock}
	task := Task{Backend: "tmux", Session: "api-fix"}
	if got := p.Check(task); got.State != Running {
		t.Fatalf("%+v", got)
	}
	p.Now = func() time.Time { return time.Now().Add(IdleAfter + time.Minute) }
	if got := p.Check(task); got.State != Idle {
		t.Fatalf("idle: %+v", got)
	}
	p.Now = nil
	// "=" makes the match exact: a prefix of a live session is not it.
	if got := p.Check(Task{Backend: "tmux", Session: "api"}); got.State != Finished {
		t.Fatalf("prefix: %+v", got)
	}
	exec.Command(tmux, "-L", sock, "kill-session", "-t", "=api-fix").Run()
	if got := p.Check(task); got.State != Finished {
		t.Fatalf("killed: %+v", got)
	}
}
