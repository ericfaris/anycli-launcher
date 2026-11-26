package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"
)

type call struct {
	name string
	args []string
}

// fakeProbe answers tmux/zellij calls from a script.
func fakeProbe(onPath map[string]bool, answer func(c call) (string, int, error)) (Probe, *[]call) {
	var calls []call
	return Probe{
		LookPath: func(n string) (string, error) {
			if onPath[n] {
				return "/bin/" + n, nil
			}
			return "", errors.New("no")
		},
		Output: func(name string, args ...string) (string, int, error) {
			c := call{name, args}
			calls = append(calls, c)
			return answer(c)
		},
		Now: func() time.Time { return t0 },
	}, &calls
}

func TestCheckTmux(t *testing.T) {
	recent := strconv.FormatInt(t0.Add(-time.Minute).Unix(), 10)
	quiet := strconv.FormatInt(t0.Add(-30*time.Minute).Unix(), 10)
	cases := []struct {
		name     string
		onPath   bool
		has      int
		hasErr   error
		activity string
		want     string
	}{
		{"running", true, 0, nil, recent + "\n" + quiet, Running},
		{"idle", true, 0, nil, quiet, Idle},
		{"no activity info", true, 0, nil, "", Running},
		{"closed", true, 1, nil, "", Finished},
		{"no tmux", false, 0, nil, "", Unknown},
		{"tmux broken", true, 0, errors.New("exec format error"), "", Unknown},
	}
	for _, c := range cases {
		p, calls := fakeProbe(map[string]bool{"tmux": c.onPath}, func(cl call) (string, int, error) {
			if cl.args[2] == "has-session" {
				return "", c.has, c.hasErr
			}
			return c.activity, 0, nil
		})
		p.TmuxSocket = "sock"
		got := p.Check(Task{Backend: "tmux", Session: "api-fix"})
		if got.State != c.want {
			t.Errorf("%s: %+v", c.name, got)
		}
		if c.onPath && c.hasErr == nil {
			if want := []string{"-L", "sock", "has-session", "-t", "=api-fix"}; !reflect.DeepEqual((*calls)[0].args, want) {
				t.Errorf("%s: %q", c.name, (*calls)[0].args)
			}
		}
	}
	p, _ := fakeProbe(nil, nil)
	if got := p.Check(Task{Backend: "tmux"}); got.State != Unknown {
		t.Fatal("no session recorded")
	}
}

func TestCheckZellij(t *testing.T) {
	cases := []struct {
		name string
		out  string
		code int
		want string
	}{
		{"running", "other\n\x1b[32;1mapi-fix\x1b[m [Created 1h ago]\n", 0, Running},
		{"exited", "api-fix [Created 2h ago] (EXITED - attach to resurrect)\n", 0, Finished},
		{"absent", "other\n", 0, Finished},
		{"prefix only", "api-fix-2\n", 0, Finished},
		{"no sessions", "No active zellij sessions found.", 1, Finished},
		{"failed", "boom", 2, Unknown},
	}
	for _, c := range cases {
		p, _ := fakeProbe(map[string]bool{"zellij": true}, func(call) (string, int, error) { return c.out, c.code, nil })
		if got := p.Check(Task{Backend: "zellij", Session: "api-fix"}); got.State != c.want {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	p, _ := fakeProbe(nil, nil)
	if p.Check(Task{Backend: "zellij", Session: "x"}).State != Unknown {
		t.Fatal("no zellij")
	}
}

func TestCheckPIDFake(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, "wt")
	os.MkdirAll(filepath.Join(wt, "sub"), 0o755)
	pidf := filepath.Join(dir, "x.pid")
	base := Probe{
		Alive: func(pid int) (bool, error) { return pid == 4242, nil },
		Cwd:   func(int) (string, error) { return "", errUnsupported },
	}
	tk := Task{Backend: "here", Worktree: wt, PIDFile: pidf}
	check := func(p Probe) string { return p.Check(tk).State }

	if check(base) != Unknown {
		t.Fatal("no pid file -> unknown")
	}
	for _, junk := range []string{"", "abc", "1", "0", "-5", "99999999999999999999999999999999999"} {
		os.WriteFile(pidf, []byte(junk), 0o600)
		if check(base) != Unknown {
			t.Errorf("pid %q must be unknown", junk)
		}
	}
	os.WriteFile(pidf, []byte("4242\n"), 0o600)
	if check(base) != Running {
		t.Fatal("alive, cwd unsupported -> running")
	}
	withCwd := base
	withCwd.Cwd = func(int) (string, error) { return filepath.Join(wt, "sub"), nil }
	if check(withCwd) != Running {
		t.Fatal("cwd inside the worktree -> running")
	}
	withCwd.Cwd = func(int) (string, error) { return dir, nil }
	if check(withCwd) != Finished {
		t.Fatal("pid reused by a process elsewhere -> finished")
	}
	os.WriteFile(pidf, []byte("4243"), 0o600)
	if check(base) != Finished {
		t.Fatal("dead -> finished")
	}
	broken := base
	broken.Alive = func(int) (bool, error) { return false, errors.New("EINVAL") }
	if check(broken) != Unknown {
		t.Fatal("check error -> unknown")
	}
	tk.Backend = "tab"
	os.WriteFile(pidf, []byte("4242"), 0o600)
	if check(base) != Running {
		t.Fatal("tab uses the pid too")
	}
	tk.Backend = "screen"
	if check(base) != Unknown {
		t.Fatal("unknown backend")
	}
}

// The real process checks: this test process is alive (and on Linux its
// cwd is visible); a child that exited is not.
func TestProcessAliveReal(t *testing.T) {
	if alive, err := processAlive(os.Getpid()); err != nil || !alive {
		t.Fatalf("self: %v %v", alive, err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if alive, _ := processAlive(cmd.Process.Pid); alive {
		t.Skip("pid already reused (rare)")
	}
	cwd, err := processCwd(os.Getpid())
	if runtime.GOOS == "linux" {
		wd, _ := os.Getwd()
		if err != nil || !sameOrUnder(cwd, wd) {
			t.Fatalf("cwd %q %v want %q", cwd, err, wd)
		}
	} else if !errors.Is(err, errUnsupported) {
		t.Fatalf("cwd on %s: %q %v", runtime.GOOS, cwd, err)
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second: "0s", 45 * time.Second: "45s", 12 * time.Minute: "12m",
		3 * time.Hour: "3h", 47 * time.Hour: "47h", 72 * time.Hour: "3d",
	} {
		if got := Age(d); got != want {
			t.Errorf("%v: %q want %q", d, got, want)
		}
	}
}
