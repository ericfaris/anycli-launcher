package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Status values. The running view never guesses: when ac can't tell, it
// says "unknown".
const (
	Running  = "running"  // the session or CLI process is alive
	Idle     = "idle"     // alive, but no output for IdleAfter (tmux only)
	Finished = "finished" // positively gone: session closed / process exited
	Unknown  = "unknown"  // can't check (no tmux, no pid recorded, ...)
)

// IdleAfter is how long a tmux session must be silent to count as idle.
const IdleAfter = 10 * time.Minute

// Liveness is a task's status plus a short reason.
type Liveness struct {
	State  string
	Detail string
}

// Probe checks liveness. Zero fields get real defaults.
type Probe struct {
	LookPath func(string) (string, error)
	// Output runs a program detached and returns stdout and exit code; a
	// non-zero exit is not an error (launch.Runner.Output's contract).
	Output     func(name string, args ...string) (string, int, error)
	TmuxSocket string
	Now        func() time.Time
	Alive      func(pid int) (bool, error)
	// Cwd is a process's working directory; errUnsupported where the OS
	// doesn't expose it.
	Cwd func(pid int) (string, error)
}

func (p Probe) withDefaults() Probe {
	if p.LookPath == nil {
		p.LookPath = exec.LookPath
	}
	if p.Output == nil {
		p.Output = execOutput
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Alive == nil {
		p.Alive = processAlive
	}
	if p.Cwd == nil {
		p.Cwd = processCwd
	}
	return p
}

func execOutput(name string, args ...string) (string, int, error) {
	out, err := exec.Command(name, args...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	if err != nil {
		return "", 1, err
	}
	return string(out), 0, nil
}

var errUnsupported = errors.New("not supported on this OS")

// Check reports whether t's CLI is still running.
func (p Probe) Check(t Task) Liveness {
	p = p.withDefaults()
	switch t.Backend {
	case "tmux":
		return p.tmux(t.Session)
	case "zellij":
		return p.zellij(t.Session)
	case "here", "tab":
		return p.pid(t)
	}
	return Liveness{Unknown, fmt.Sprintf("backend %q", t.Backend)}
}

func (p Probe) tmuxArgs(args ...string) []string {
	if p.TmuxSocket != "" {
		return append([]string{"-L", p.TmuxSocket}, args...)
	}
	return args
}

func (p Probe) tmux(session string) Liveness {
	if session == "" {
		return Liveness{Unknown, "no tmux session recorded"}
	}
	bin, err := p.LookPath("tmux")
	if err != nil {
		return Liveness{Unknown, "tmux not on PATH"}
	}
	_, code, err := p.Output(bin, p.tmuxArgs("has-session", "-t", "="+session)...)
	if err != nil {
		return Liveness{Unknown, "tmux: " + err.Error()}
	}
	if code != 0 {
		return Liveness{Finished, "tmux session " + session + " closed"}
	}
	out, code, err := p.Output(bin, p.tmuxArgs("list-windows", "-t", "="+session, "-F", "#{window_activity}")...)
	if err != nil || code != 0 {
		return Liveness{Running, "tmux session " + session}
	}
	var last int64
	for _, f := range strings.Fields(out) {
		if n, err := strconv.ParseInt(f, 10, 64); err == nil && n > last {
			last = n
		}
	}
	if last > 0 {
		if quiet := p.Now().Sub(time.Unix(last, 0)); quiet >= IdleAfter {
			return Liveness{Idle, fmt.Sprintf("tmux session %s, quiet for %s", session, Age(quiet))}
		}
	}
	return Liveness{Running, "tmux session " + session}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func (p Probe) zellij(session string) Liveness {
	if session == "" {
		return Liveness{Unknown, "no zellij session recorded"}
	}
	bin, err := p.LookPath("zellij")
	if err != nil {
		return Liveness{Unknown, "zellij not on PATH"}
	}
	out, code, err := p.Output(bin, "list-sessions", "--short")
	if err != nil {
		return Liveness{Unknown, "zellij: " + err.Error()}
	}
	clean := ansi.ReplaceAllString(out, "")
	if code != 0 {
		if strings.Contains(strings.ToLower(clean), "no active") {
			return Liveness{Finished, "zellij session " + session + " closed"}
		}
		return Liveness{Unknown, "zellij list-sessions failed"}
	}
	for _, line := range strings.Split(clean, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == session {
			// zellij 0.39+ lists resurrectable sessions as "(EXITED ...)".
			if strings.Contains(line, "EXITED") {
				return Liveness{Finished, "zellij session " + session + " exited"}
			}
			return Liveness{Running, "zellij session " + session}
		}
	}
	return Liveness{Finished, "zellij session " + session + " closed"}
}

// ReadPID reads a pid file; ok is false for missing or junk content.
func ReadPID(path string) (int, bool) {
	if path == "" {
		return 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 32 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n <= 1 {
		return 0, false
	}
	return n, true
}

func (p Probe) pid(t Task) Liveness {
	pid, ok := ReadPID(t.PIDFile)
	if !ok {
		return Liveness{Unknown, "no pid recorded"}
	}
	alive, err := p.Alive(pid)
	if err != nil {
		return Liveness{Unknown, fmt.Sprintf("pid %d: %v", pid, err)}
	}
	if !alive {
		return Liveness{Finished, fmt.Sprintf("pid %d exited", pid)}
	}
	// Pids get reused. Where the OS shows a process's cwd (Linux), a
	// process outside the worktree is someone else's: ours is gone.
	if cwd, err := p.Cwd(pid); err == nil && cwd != "" {
		if !sameOrUnder(cwd, t.Worktree) {
			return Liveness{Finished, fmt.Sprintf("pid %d exited (reused by another process)", pid)}
		}
	}
	return Liveness{Running, fmt.Sprintf("pid %d", pid)}
}

func sameOrUnder(path, dir string) bool {
	rd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		rd = dir
	}
	rp, err := filepath.EvalSymlinks(path)
	if err != nil {
		rp = path
	}
	return filepath.Clean(rp) == filepath.Clean(rd) || Under(rp, rd)
}

// Age renders a duration as "45s", "12m", "3h", "2d".
func Age(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
