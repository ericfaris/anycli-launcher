package launch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// interceptAttach runs everything for real except the final attach /
// switch-client, which needs a terminal.
type interceptAttach struct {
	ExecRunner
	attaches [][]string
}

func (r *interceptAttach) Run(c Command) (int, error) {
	r.attaches = append(r.attaches, c.Args)
	return 0, nil
}

type rec struct {
	Cwd  string   `json:"cwd"`
	Args []string `json:"args"`
}

func waitRecords(t *testing.T, path string, n int) []rec {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var out []rec
		if f, err := os.Open(path); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				var r rec
				if json.Unmarshal(sc.Bytes(), &r) == nil {
					out = append(out, r)
				}
			}
			f.Close()
		}
		if len(out) >= n || time.Now().After(deadline) {
			if len(out) < n {
				t.Fatalf("want %d records, got %d: %+v", n, len(out), out)
			}
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestTmuxIntegration drives a real tmux on a private socket (skipped when
// tmux is absent): hostile dirs, titles and args must reach the CLI
// verbatim, a second fresh launch opens a second window, resume re-attaches
// without starting anything.
func TestTmuxIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no tmux on Windows")
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	base := t.TempDir()
	// Private server: own socket dir, no user config.
	t.Setenv("TMUX_TMPDIR", base)
	t.Setenv("TMUX", "")
	t.Setenv("HOME", base)
	t.Setenv("XDG_CONFIG_HOME", base)
	out := filepath.Join(base, "records.jsonl")
	t.Setenv("LAUNCH_HELPER", "1")
	t.Setenv("LAUNCH_HELPER_OUT", out)
	sock := fmt.Sprintf("ac-test-%d", os.Getpid())
	t.Cleanup(func() { exec.Command(tmuxBin, "-L", sock, "kill-server").Run() })

	// The fake CLI lives in a directory a shell would trip over.
	binDir := filepath.Join(base, "bin dir'x")
	os.MkdirAll(binDir, 0o755)
	cli := filepath.Join(binDir, "fake cli")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, data, 0o755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(base, "proj #{session_name} #(touch pwned) $(touch pwned2)")
	os.MkdirAll(proj, 0o755)

	r := &interceptAttach{}
	d := Deps{
		LookPath: func(n string) (string, error) {
			if n == "fake" {
				return cli, nil
			}
			return exec.LookPath(n)
		},
		Runner: r, TmuxSocket: sock,
	}
	tm := &Tmux{Deps: d}
	args := []string{"it's", `"q"`, "a;b", "$(touch pwned3)", "#{session_name}", "line1\nline2", ""}
	req := Request{Dir: proj, Name: "fake", Args: args, Title: "T #{host} #(touch pwned4)", Session: "my.proj"}
	if code, err := tm.Launch(req); code != 0 || err != nil {
		t.Fatalf("launch: %d %v", code, err)
	}
	recs := waitRecords(t, out, 1)
	if recs[0].Cwd != proj && recs[0].Cwd != mustEval(t, proj) {
		t.Fatalf("cwd %q want %q", recs[0].Cwd, proj)
	}
	if !reflect.DeepEqual(recs[0].Args, args) {
		t.Fatalf("args %q want %q", recs[0].Args, args)
	}
	sess := SessionName("my.proj")
	wantAttach := []string{"-L", sock, "attach-session", "-t", "=" + sess}
	if len(r.attaches) != 1 || !reflect.DeepEqual(r.attaches[0], wantAttach) {
		t.Fatalf("attach %q", r.attaches)
	}

	// Fresh again: the session exists, so a second window; argv of one
	// element goes through sh -c and must survive the odd path.
	req2 := req
	req2.Args, req2.Title = nil, ""
	if code, err := tm.Launch(req2); code != 0 || err != nil {
		t.Fatalf("launch 2: %d %v", code, err)
	}
	recs = waitRecords(t, out, 2)
	if len(recs[1].Args) != 0 {
		t.Fatalf("args %q", recs[1].Args)
	}

	// Resume: re-attach only.
	req3 := req
	req3.Resume = true
	if code, err := tm.Launch(req3); code != 0 || err != nil {
		t.Fatalf("resume: %d %v", code, err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(waitRecords(t, out, 2)); n != 2 {
		t.Fatalf("resume started a CLI (%d records)", n)
	}
	if len(r.attaches) != 3 {
		t.Fatalf("attaches %q", r.attaches)
	}

	lw, err := exec.Command(tmuxBin, "-L", sock, "list-windows", "-t", "="+sess, "-F", "#{window_name}").Output()
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Split(strings.TrimSpace(string(lw)), "\n")
	if len(names) != 2 || names[0] != "T #{host} #(touch pwned4)" {
		t.Fatalf("windows %q", names)
	}
	for _, p := range []string{"pwned", "pwned2", "pwned3", "pwned4"} {
		for _, dir := range []string{proj, base} {
			if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
				t.Fatalf("%s was created in %s: injection", p, dir)
			}
		}
	}
}
