package main

// End-to-end task session through the real binary and real git: `ac
// <project> --task "..."` makes the worktree, runs the CLI there with the
// task as its argv (here and, through the fake kitty, tab), records the
// CLI's pid, `ac running` reports it finished once it exited, and `ac
// finish` removes the worktree but keeps the branch.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/internal/config"
)

func TestTaskE2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "projects")
	proj := filepath.Join(root, "api")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(proj, 0o755)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	for _, n := range []string{"ac", "fakecli", "kitty"} {
		copyExe(t, filepath.Join(bin, n+exe))
	}
	gitcfg := filepath.Join(tmp, "gitconfig")
	os.WriteFile(gitcfg, []byte("[user]\n\tname = t\n\temail = t@t\n[commit]\n\tgpgsign = false\n"), 0o600)
	env := append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AC_PROJECTS_DIR=", "HOME="+home, "USERPROFILE="+home,
		"GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1",
		"AC_FRECENCY_FILE="+filepath.Join(tmp, "state", "frecency.json"), "AC_TASKS_FILE=",
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "TMUX=", "ZELLIJ=",
	)
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir, c.Env = dir, env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(proj, "init", "-q", "-b", "main")
	git(proj, "commit", "-q", "--allow-empty", "-m", "init")

	cfgPath := filepath.Join(tmp, "config.toml")
	writeCfg := func(backend string) {
		cfg := config.Default()
		cfg.ProjectsDir, cfg.CLI, cfg.Backend, cfg.Terminal = root, "fakecli", backend, "kitty"
		if err := config.Save(cfgPath, cfg); err != nil {
			t.Fatal(err)
		}
	}
	cliLog := filepath.Join(tmp, "cli.json")
	ac := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command(filepath.Join(bin, "ac"+exe), args...)
		cmd.Dir = tmp
		cmd.Env = append(env, "AC_CONFIG="+cfgPath, "FAKECLI_JSON="+cliLog)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ac %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	type rec struct {
		Cwd  string   `json:"cwd"`
		Args []string `json:"args"`
		Pid  int      `json:"pid"`
	}
	readRec := func() rec {
		t.Helper()
		var r rec
		b, err := os.ReadFile(cliLog)
		if err != nil || json.Unmarshal(b, &r) != nil {
			t.Fatalf("CLI never ran: %v", err)
		}
		os.Remove(cliLog)
		return r
	}
	same := func(a, b string) bool {
		sa, err1 := os.Stat(a)
		sb, err2 := os.Stat(b)
		return err1 == nil && err2 == nil && os.SameFile(sa, sb)
	}
	pidOf := func(id string) int {
		b, _ := os.ReadFile(filepath.Join(tmp, "state", "tasks", filepath.FromSlash(id)+".pid"))
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return n
	}

	task := "Fix \"login\"; $(touch pwned) `id`"
	writeCfg("here")
	cdFile := filepath.Join(tmp, "cd")
	ac("", "--cd-file", cdFile, "api", "--task", task)
	wt := filepath.Join(root, ".ac-worktrees", "api", "fix-login-touch-pwned-id")
	r := readRec()
	if !reflect.DeepEqual(r.Args, []string{task}) || !same(r.Cwd, wt) {
		t.Fatalf("CLI %+v want cwd %s", r, wt)
	}
	if got := pidOf("api/fix-login-touch-pwned-id"); got != r.Pid {
		t.Fatalf("pid file %d, CLI pid %d", got, r.Pid)
	}
	if b, _ := os.ReadFile(cdFile); !same(string(b), wt) {
		t.Fatalf("cd file %q", b)
	}

	// Tab through the fake kitty (Unix: the trampoline execs, so the pid
	// file holds the CLI's own pid).
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("bash"); err == nil {
			writeCfg("tab")
			ac("", "api", "--task", "second one")
			r2 := readRec()
			if !reflect.DeepEqual(r2.Args, []string{"second one"}) || !same(r2.Cwd, filepath.Join(root, ".ac-worktrees", "api", "second-one")) {
				t.Fatalf("tab CLI %+v", r2)
			}
			if got := pidOf("api/second-one"); got != r2.Pid {
				t.Fatalf("tab pid file %d, CLI pid %d", got, r2.Pid)
			}
		}
	}

	out := ac("", "running")
	if !strings.Contains(out, "api/fix-login-touch-pwned-id") || !strings.Contains(out, "finished") {
		t.Fatalf("running:\n%s", out)
	}
	if list := ac("", "list"); strings.TrimSpace(list) != "api" {
		t.Fatalf("worktrees listed as projects: %q", list)
	}

	out = ac("y\n", "finish", "fix-login-touch-pwned-id")
	if !strings.Contains(out, "Removed worktree") {
		t.Fatalf("finish:\n%s", out)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree kept")
	}
	if git(proj, "branch", "--list", "ac/fix-login-touch-pwned-id") == "" {
		t.Fatal("branch must be kept")
	}
	for _, d := range []string{proj, tmp, wt} {
		if _, err := os.Stat(filepath.Join(d, "pwned")); err == nil {
			t.Fatalf("injection in %s", d)
		}
	}
	if out := ac("", "doctor"); !strings.Contains(out, "-- Task worktrees --") || !strings.Contains(out, "worktree support ok") {
		t.Fatalf("doctor:\n%s", out)
	}
}
