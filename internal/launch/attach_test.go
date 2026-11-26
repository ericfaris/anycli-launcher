package launch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestPlanAttach(t *testing.T) {
	cases := []struct {
		backend string
		inside  bool
		want    []string
		unavail bool
	}{
		{BackendTmux, false, []string{"attach-session", "-t", "=s-1"}, false},
		{BackendTmux, true, []string{"switch-client", "-t", "=s-1"}, false},
		{BackendZellij, false, []string{"attach", "s-1"}, false},
		{BackendZellij, true, nil, true},
		{BackendHere, false, nil, true},
		{BackendTab, false, nil, true},
	}
	for _, c := range cases {
		got, err := PlanAttach(c.backend, "s-1", c.inside)
		if c.unavail != errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s inside=%v: %q %v", c.backend, c.inside, got, err)
		}
	}
	if _, err := PlanAttach(BackendZellij, "s-1", true); !strings.Contains(err.Error(), "zellij attach s-1") {
		t.Fatalf("inside zellij must say how: %v", err)
	}
}

func TestAttach(t *testing.T) {
	path := map[string]string{"tmux": "/usr/bin/tmux", "zellij": "/usr/bin/zellij"}
	for _, inside := range []bool{false, true} {
		r := &fakeRunner{runCode: 5}
		vars := map[string]string{}
		if inside {
			vars["TMUX"] = "/tmp/tmux-1000/default,1,0"
		}
		cd := filepath.Join(t.TempDir(), "cd")
		code, err := Attach(BackendTmux, "app-x", "/wt/app/x", cd, tmuxDeps(vars, path, r))
		if err != nil || code != 5 {
			t.Fatalf("inside=%v: %d %v", inside, code, err)
		}
		verb := "attach-session"
		if inside {
			verb = "switch-client"
		}
		if want := []string{"-L", "test", verb, "-t", "=app-x"}; len(r.runs) != 1 || !reflect.DeepEqual(r.runs[0].Args, want) || len(r.outputs) != 0 {
			t.Fatalf("runs %v outputs %v", r.runs, r.outputs)
		}
		if _, err := os.Stat(cd); inside == (err == nil) {
			t.Fatalf("inside=%v: cd-file written=%v", inside, err == nil)
		}
	}
	// zellij: no -L socket, plain attach.
	r := &fakeRunner{}
	if _, err := Attach(BackendZellij, "app-x", "/wt", "", tmuxDeps(nil, path, r)); err != nil || !reflect.DeepEqual(r.runs[0].Args, []string{"attach", "app-x"}) {
		t.Fatalf("%v %v", err, r.runs)
	}
	// Missing multiplexer, inside zellij: unavailable, nothing runs.
	r = &fakeRunner{}
	if _, err := Attach(BackendTmux, "s", "/wt", "", tmuxDeps(nil, map[string]string{}, r)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := Attach(BackendZellij, "s", "/wt", "", tmuxDeps(map[string]string{"ZELLIJ": "0"}, path, r)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if len(r.runs) != 0 {
		t.Fatal("nothing should run")
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWritePIDFile(t *testing.T) {
	if err := WritePIDFile("", 1); err != nil {
		t.Fatal("empty path is a no-op")
	}
	p := filepath.Join(t.TempDir(), "state", "tasks", "x.pid")
	if err := WritePIDFile(p, 42); err != nil || readPID(t, p) != 42 {
		t.Fatal(err)
	}
	if err := WritePIDFile(p, 43); err != nil || readPID(t, p) != 43 {
		t.Fatal("replace")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".pid-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

// Here records the CLI child's pid (not ac's) when asked to; without a
// PIDFile nothing is written (phase 3 behavior).
func TestHereWritesPIDFile(t *testing.T) {
	h := helper(t)
	var out bytes.Buffer
	h.Stdout, h.Stderr = &out, &out
	pidf := filepath.Join(t.TempDir(), "x.pid")
	if code, err := h.Launch(Request{Dir: t.TempDir(), Name: "fake", PIDFile: pidf}); err != nil || code != 0 {
		t.Fatalf("%d %v %s", code, err, out.String())
	}
	if pid := readPID(t, pidf); pid <= 0 || pid == os.Getpid() {
		t.Fatalf("pid %d (ac is %d)", pid, os.Getpid())
	}
}

func TestTabExecWritesPIDFileOnlyInFinalStage(t *testing.T) {
	stubTabExec(t, "linux")
	pidf := filepath.Join(t.TempDir(), "t.pid")
	path, _ := WriteSpec(t.TempDir(), TabSpec{Dir: t.TempDir(), Argv: []string{"/bin/claude"}, Login: true, PIDFile: pidf})
	TabExec([]string{path}, &bytes.Buffer{}, &bytes.Buffer{})
	if _, err := os.Stat(pidf); err == nil {
		t.Fatal("login stage must not write the pid (the final exec has it)")
	}
	TabExec([]string{"--login-done", path}, &bytes.Buffer{}, &bytes.Buffer{})
	if readPID(t, pidf) != os.Getpid() {
		t.Fatal("final stage records its own pid (exec keeps it)")
	}
}

func TestTabLaunchPassesPIDFileInSpec(t *testing.T) {
	d, r, _ := tabDeps(t, map[string]string{"KITTY_WINDOW_ID": "1"}, map[string]string{"kitty": "/usr/bin/kitty", "claude": "/usr/bin/claude"})
	if _, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p", Name: "claude", PIDFile: "/state/t.pid"}); err != nil {
		t.Fatal(err)
	}
	specPath := r.outputs[0].Args[len(r.outputs[0].Args)-1]
	s, err := ReadSpec(specPath)
	if err != nil || s.PIDFile != "/state/t.pid" {
		t.Fatalf("%+v %v", s, err)
	}
}
