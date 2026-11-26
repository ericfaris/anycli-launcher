package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericfaris/anycli-launcher/v4/internal/config"
	"github.com/ericfaris/anycli-launcher/v4/internal/launch"
	"github.com/ericfaris/anycli-launcher/v4/internal/tui"
	"github.com/ericfaris/anycli-launcher/v4/internal/worktree"
)

// taskHarness is a harness whose git ignores the machine's config and has
// an identity, plus a git project "api" with one commit.
func taskHarness(t *testing.T) *harness {
	t.Helper()
	if !hasGit() {
		t.Skip("git not installed")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(cfg, []byte("[user]\n\tname = t\n\temail = t@t\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"), 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("AC_TASKS_FILE", "")
	h := newHarness(t)
	h.mkproj("api")
	gitInit(t, h.proj("api"))
	return h
}

func (h *harness) proj(name string) string { return filepath.Join(h.root, name) }

func (h *harness) wtRoot() string { return filepath.Join(h.root, config.WorktreeDirName) }

func (h *harness) registry() *worktree.Registry {
	h.t.Helper()
	r, err := worktree.Load(h.env.TasksPath)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := worktree.ExecGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func TestTaskCreatesWorktreeAndLaunches(t *testing.T) {
	h := taskHarness(t)
	if code := h.run("api", "--task", "Fix the login bug"); code != 0 {
		t.Fatalf("code %d: %s", code, h.err.String())
	}
	wt := filepath.Join(h.wtRoot(), "api", "fix-the-login-bug")
	req := h.launcher.reqs[0]
	want := launch.Request{
		Dir: wt, Name: "claude", Args: []string{"Fix the login bug"}, CDFile: h.cdFile,
		Session: "api/fix-the-login-bug", PIDFile: worktree.PIDPath(h.env.TasksPath, "api/fix-the-login-bug"),
	}
	if !reflect.DeepEqual(req, want) {
		t.Fatalf("got  %+v\nwant %+v", req, want)
	}
	if h.cdTarget() != wt {
		t.Fatal("the shell lands in the worktree")
	}
	if got := gitOut(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "ac/fix-the-login-bug" {
		t.Fatalf("branch %q", got)
	}
	if gitOut(t, wt, "rev-parse", "HEAD") != gitOut(t, h.proj("api"), "rev-parse", "HEAD") {
		t.Fatal("base is the project's HEAD")
	}
	r := h.registry()
	if len(r.Tasks) != 1 {
		t.Fatalf("%+v", r.Tasks)
	}
	tk := r.Tasks[0]
	if tk.ID != "api/fix-the-login-bug" || tk.Project != "api" || tk.Branch != "ac/fix-the-login-bug" || tk.Backend != "here" ||
		tk.Task != "Fix the login bug" || tk.CLI != "claude" || !tk.Started.Equal(h.now) || tk.PIDFile != want.PIDFile || tk.Session != "" || !worktree.ValidSHA(tk.Base) {
		t.Fatalf("%+v", tk)
	}
	if !strings.Contains(h.err.String(), "ac finish api/fix-the-login-bug") {
		t.Fatalf("stderr %q", h.err.String())
	}
	// The worktree never shows up as a project, and launches were recorded.
	h.run("list")
	if strings.TrimSpace(h.out.String()) != "api" {
		t.Fatalf("list: %q", h.out.String())
	}
}

// ac task <project> "..." is the same, words joined; a second identical
// task gets -2, and a branch or folder already in the way bumps the slug.
func TestTaskSubcommandAndCollisions(t *testing.T) {
	h := taskHarness(t)
	if code := h.run("task", "api", "Add", "tests"); code != 0 {
		t.Fatalf("%d %s", code, h.err.String())
	}
	if code := h.run("api", "--task=add tests"); code != 0 {
		t.Fatal(h.err.String())
	}
	gitRun(t, h.proj("api"), "branch", "ac/add-tests-3")
	os.MkdirAll(filepath.Join(h.wtRoot(), "api", "add-tests-4"), 0o755)
	if code := h.run("task", "api", "add tests"); code != 0 {
		t.Fatal(h.err.String())
	}
	var ids []string
	for _, tk := range h.registry().Tasks {
		ids = append(ids, tk.ID)
	}
	if !reflect.DeepEqual(ids, []string{"api/add-tests", "api/add-tests-2", "api/add-tests-5"}) {
		t.Fatalf("ids %v", ids)
	}
	if h.launcher.reqs[0].Args[0] != "Add tests" {
		t.Fatalf("args %q", h.launcher.reqs[0].Args)
	}
}

func TestTaskBase(t *testing.T) {
	h := taskHarness(t)
	p := h.proj("api")
	gitRun(t, p, "checkout", "-q", "-b", "feature")
	gitRun(t, p, "commit", "-q", "--allow-empty", "-m", "feature work")
	feature := gitOut(t, p, "rev-parse", "HEAD")
	gitRun(t, p, "checkout", "-q", "main")
	if code := h.run("api", "--task", "on feature", "--base", "feature"); code != 0 {
		t.Fatal(h.err.String())
	}
	wt := filepath.Join(h.wtRoot(), "api", "on-feature")
	if gitOut(t, wt, "rev-parse", "HEAD") != feature || h.registry().Tasks[0].Base != feature {
		t.Fatal("--base feature")
	}
	for _, bad := range []string{"--upload-pack=touch pwned", "-b", "nope", "main;touch pwned"} {
		h.launcher.reqs = nil
		if code := h.run("api", "--task", "x "+bad, "--base="+bad); code != 1 || len(h.launcher.reqs) != 0 {
			t.Errorf("base %q: code %d %s", bad, code, h.err.String())
		}
	}
	if len(h.registry().Tasks) != 1 {
		t.Fatal("bad bases must not create tasks")
	}
	if _, err := os.Stat(filepath.Join(p, "pwned")); err == nil {
		t.Fatal("injection")
	}
}

func TestTaskRefusals(t *testing.T) {
	h := taskHarness(t)
	h.mkproj("plain")
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"plain", "--task", "x"}, 1, "plain is not a git repository"},
		{[]string{"api", "--task", "--dangerously-skip-permissions"}, 2, "can't start with '-'"},
		{[]string{"api", "--task", "  -x"}, 2, "can't start with '-'"},
		{[]string{"api", "--task", "   "}, 2, "empty"},
		{[]string{"api", "--task", "a\x00b"}, 2, "NUL"},
		{[]string{"api", "--task"}, 2, "--task needs a value"},
		{[]string{"api", "-r", "--task", "x"}, 2, "can't be combined with --resume"},
		{[]string{"task", "api"}, 2, "usage: ac task"},
		{[]string{"api", "--base", "main"}, 2, "--base needs --task"},
		{[]string{"task", "api", "x", "--task", "y"}, 2, "use either"},
		{[]string{"api", "extra", "--task", "x"}, 2, "usage: ac <project> --task"},
		{[]string{"nope", "--task", "x"}, 1, "no project matching"},
	}
	for _, c := range cases {
		if code := h.run(c.args...); code != c.code || !strings.Contains(h.err.String(), c.msg) {
			t.Errorf("%q: code %d stderr %q", c.args, code, h.err.String())
		}
	}
	if len(h.launcher.reqs) != 0 {
		t.Fatal("nothing may launch")
	}
	if _, err := os.Stat(h.env.TasksPath); err == nil {
		t.Fatal("no registry for refused tasks")
	}
	if _, err := os.Stat(h.wtRoot()); err == nil {
		t.Fatal("no worktree dir for refused tasks")
	}
}

// The CLI is checked before anything is created; an invalid worktree_dir
// (a project root) is refused.
func TestTaskPrechecks(t *testing.T) {
	h := taskHarness(t)
	h.onPath["claude"] = false
	if code := h.run("api", "--task", "x"); code != 1 || !strings.Contains(h.err.String(), "claude") {
		t.Fatalf("%d %q", code, h.err.String())
	}
	if _, err := os.Stat(h.wtRoot()); err == nil {
		t.Fatal("worktree made before the CLI check")
	}
	h.onPath["claude"] = true
	h.writeConfig(func(c *config.Config) { c.WorktreeDir = h.root })
	if code := h.run("api", "--task", "x"); code != 1 || !strings.Contains(h.err.String(), "must not be or contain the project root") {
		t.Fatalf("%d %q", code, h.err.String())
	}
	// A custom visible worktree_dir inside the root works and stays out of
	// the project list.
	h.writeConfig(func(c *config.Config) { c.WorktreeDir = filepath.Join(h.root, "trees") })
	if code := h.run("api", "--task", "x"); code != 0 {
		t.Fatal(h.err.String())
	}
	if _, err := os.Stat(filepath.Join(h.root, "trees", "api", "x")); err != nil {
		t.Fatal(err)
	}
	h.run("list")
	if strings.TrimSpace(h.out.String()) != "api" {
		t.Fatalf("list: %q", h.out.String())
	}
	// A planted symlink for the project's folder is refused.
	h.writeConfig(func(c *config.Config) {})
	os.MkdirAll(h.wtRoot(), 0o755)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(h.wtRoot(), "api")); err != nil {
		t.Skip("no symlinks")
	}
	if code := h.run("api", "--task", "y"); code != 1 || !strings.Contains(h.err.String(), "symlink") {
		t.Fatalf("%d %q", code, h.err.String())
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatal("wrote through the symlink")
	}
}

// Hostile task text reaches the CLI as one verbatim argument, never a
// shell; the branch and path come from the slug.
func TestTaskHostileText(t *testing.T) {
	h := taskHarness(t)
	text := "it's \"quoted\"; $(touch pwned) `touch pwned2` #{session_name} #(touch pwned3)\nline2 ../../x"
	if code := h.run("api", "--task", text); code != 0 {
		t.Fatal(h.err.String())
	}
	req := h.launcher.reqs[0]
	if !reflect.DeepEqual(req.Args, []string{text}) {
		t.Fatalf("args %q", req.Args)
	}
	tk := h.registry().Tasks[0]
	if !worktree.ValidBranch(tk.Branch) || !worktree.Under(tk.Worktree, h.wtRoot()) || tk.Task != text {
		t.Fatalf("%+v", tk)
	}
	for _, f := range []string{"pwned", "pwned2", "pwned3"} {
		for _, d := range []string{h.proj("api"), tk.Worktree, h.root} {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				t.Fatalf("%s in %s", f, d)
			}
		}
	}
	// Control characters never reach the terminal from the registry.
	h.run("running")
	if strings.ContainsAny(h.out.String(), "\x1b\x00") {
		t.Fatalf("raw control chars: %q", h.out.String())
	}
}

func TestTaskBackendRecorded(t *testing.T) {
	h := taskHarness(t)
	h.writeConfig(func(c *config.Config) { c.Backend = "tmux" })
	if code := h.run("api", "--task", "one"); code != 0 {
		t.Fatal(h.err.String())
	}
	tk := h.registry().Tasks[0]
	if tk.Backend != "tmux" || tk.Session != launch.SessionName("api/one") || tk.PIDFile != "" {
		t.Fatalf("%+v", tk)
	}
	// Fallback to here is recorded as here, with a pid file.
	h.unavail["tmux"] = true
	if code := h.run("api", "--task", "two"); code != 0 {
		t.Fatal(h.err.String())
	}
	tk, _ = h.registry().Get("api/two")
	if tk.Backend != "here" || tk.Session != "" || tk.PIDFile == "" {
		t.Fatalf("%+v", tk)
	}
	if !reflect.DeepEqual(h.backends[len(h.backends)-2:], []string{"tmux", "here"}) {
		t.Fatalf("%v", h.backends)
	}
}

// Phase 1-3 regression: a project named like a new subcommand still wins.
func TestTaskWordCommandsYieldToProjects(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"task", "running", "finish", "prune", "ls-tasks"} {
		h.mkproj(name)
		h.launcher.reqs = nil
		if code := h.run(name); code != 0 || len(h.launcher.reqs) != 1 || h.launcher.reqs[0].Dir != h.proj(name) {
			t.Fatalf("%s: %d %+v %s", name, code, h.launcher.reqs, h.err.String())
		}
	}
}

func TestRunningOutput(t *testing.T) {
	h := taskHarness(t)
	h.run("running")
	if !strings.Contains(h.out.String(), "no task sessions") {
		t.Fatal(h.out.String())
	}
	h.run("api", "--task", "fix it")
	wt := h.registry().Tasks[0].Worktree
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644)
	gitRun(t, wt, "add", "new.txt")
	gitRun(t, wt, "commit", "-q", "-m", "w")
	os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x"), 0o644)
	// Fake a live CLI for the pid check.
	launch.WritePIDFile(worktree.PIDPath(h.env.TasksPath, "api/fix-it"), 4242)
	h.env.Probe = worktree.Probe{Alive: func(pid int) (bool, error) { return pid == 4242, nil }, Cwd: func(int) (string, error) { return wt, nil }}
	h.now = h.now.Add(3 * time.Hour)
	for _, cmd := range []string{"running", "ls-tasks"} {
		h.run(cmd)
		lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "TASK") {
			t.Fatalf("%q", h.out.String())
		}
		f := strings.Fields(lines[1])
		want := []string{"api/fix-it", "ac/fix-it", "3h", "running", "dirty", "↑1", "(1", "unpushed)", "here"}
		if !reflect.DeepEqual(f, want) {
			t.Fatalf("%s: %q", cmd, f)
		}
	}
	os.RemoveAll(wt)
	h.run("running")
	if !strings.Contains(h.out.String(), "worktree missing") {
		t.Fatal(h.out.String())
	}
	// A corrupt registry: reported, empty list, exit 0.
	os.WriteFile(h.env.TasksPath, []byte("{"), 0o600)
	if code := h.run("running"); code != 0 || !strings.Contains(h.err.String(), "unreadable") || !strings.Contains(h.out.String(), "no task sessions") {
		t.Fatalf("%d %q %q", code, h.out.String(), h.err.String())
	}
}

func TestFinishClean(t *testing.T) {
	h := taskHarness(t)
	h.run("api", "--task", "fix it")
	wt := h.registry().Tasks[0].Worktree
	h.input("n\n")
	if code := h.run("finish", "fix-it"); code != 1 || !strings.Contains(h.out.String(), "Cancelled") {
		t.Fatalf("%d %s", code, h.out.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("cancel removed the worktree")
	}
	h.input("y\n")
	if code := h.run("finish", "api/fix-it"); code != 0 {
		t.Fatalf("%d %s %s", code, h.out.String(), h.err.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree kept")
	}
	if _, err := os.Stat(filepath.Dir(wt)); !os.IsNotExist(err) {
		t.Fatal("empty project folder kept")
	}
	if gitOut(t, h.proj("api"), "branch", "--list", "ac/fix-it") == "" {
		t.Fatal("branch must be kept by default")
	}
	if len(h.registry().Tasks) != 0 {
		t.Fatal("registry entry kept")
	}
	if !strings.Contains(h.out.String(), "no new commits") || !strings.Contains(h.out.String(), "branch -d") {
		t.Fatalf("hint: %s", h.out.String())
	}
	if code := h.run("finish", "fix-it"); code != 1 || !strings.Contains(h.err.String(), "no task matching") {
		t.Fatal("finished twice")
	}
}

// Uncommitted changes or unpushed commits need the branch name typed.
func TestFinishNeedsBranchNameForWork(t *testing.T) {
	h := taskHarness(t)
	h.run("api", "--task", "dirty")
	h.run("api", "--task", "commits")
	r := h.registry()
	dirty, _ := r.Get("api/dirty")
	commits, _ := r.Get("api/commits")
	os.WriteFile(filepath.Join(dirty.Worktree, "a.txt"), []byte("x"), 0o644)
	gitRun(t, commits.Worktree, "commit", "-q", "--allow-empty", "-m", "work")

	for _, ans := range []string{"y\n", "yes\n", "ac/dirt\n", ""} {
		h.input(ans)
		if code := h.run("finish", "api/dirty"); code != 1 {
			t.Fatalf("answer %q accepted", ans)
		}
		if _, err := os.Stat(dirty.Worktree); err != nil {
			t.Fatal("removed without the branch name")
		}
	}
	if !strings.Contains(h.out.String(), "uncommitted changes") || !strings.Contains(h.out.String(), "Type the branch name (ac/dirty)") {
		t.Fatal(h.out.String())
	}
	h.input("ac/dirty\n")
	if code := h.run("finish", "api/dirty"); code != 0 {
		t.Fatal(h.out.String(), h.err.String())
	}
	if _, err := os.Stat(dirty.Worktree); !os.IsNotExist(err) {
		t.Fatal("dirty worktree kept after confirmation")
	}

	h.input("y\n")
	if code := h.run("finish", "commits", "--delete-branch"); code != 1 || !strings.Contains(h.out.String(), "1 commit not pushed") || !strings.Contains(h.out.String(), "deleting the branch loses them") {
		t.Fatalf("%d %s", code, h.out.String())
	}
	h.input("ac/commits\n")
	if code := h.run("finish", "--delete-branch", "commits"); code != 0 {
		t.Fatal(h.out.String(), h.err.String())
	}
	if gitOut(t, h.proj("api"), "branch", "--list", "ac/commits") != "" {
		t.Fatal("--delete-branch kept the branch")
	}
	if gitOut(t, h.proj("api"), "branch", "--list", "main") == "" {
		t.Fatal("main must survive")
	}
	if code := h.run("finish", "x", "--force"); code != 2 {
		t.Fatal("unknown option")
	}
	if code := h.run("finish"); code != 2 {
		t.Fatal("no task")
	}
}

// A hand-edited (or stale) registry can't steer finish outside
// worktree_dir, through a symlink, or at a folder that isn't a worktree.
func TestFinishGuards(t *testing.T) {
	h := taskHarness(t)
	h.run("api", "--task", "real")
	real, _ := h.registry().Get("api/real")

	outside := filepath.Join(t.TempDir(), "important")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "keep"), nil, 0o644)
	notWT := filepath.Join(h.wtRoot(), "api", "plain")
	os.MkdirAll(notWT, 0o755)
	os.WriteFile(filepath.Join(notWT, "keep"), nil, 0o644)

	tamper := func(id, path string) {
		worktree.Update(h.env.TasksPath, h.now, func(r *worktree.Registry) error {
			t := real
			t.ID, t.Worktree, t.Dir, t.Branch = id, path, path, "ac/"+id[strings.IndexByte(id, '/')+1:]
			r.Put(t)
			return nil
		})
	}
	tamper("api/outside", outside)
	tamper("api/escape", filepath.Join(h.wtRoot(), "api", "..", "..", "..", filepath.Base(filepath.Dir(outside)), "important"))
	tamper("api/plain", notWT)
	tamper("api/root", h.wtRoot())
	tamper("api/repo", h.proj("api"))
	cases := map[string]string{
		"api/outside": "not inside the worktree dir",
		"api/escape":  "not inside the worktree dir",
		"api/plain":   "is not a git worktree",
		"api/root":    "not inside the worktree dir",
		"api/repo":    "not inside the worktree dir",
	}
	if err := os.Symlink(outside, filepath.Join(h.wtRoot(), "api", "link")); err == nil {
		tamper("api/link", filepath.Join(h.wtRoot(), "api", "link"))
		cases["api/link"] = "symlink"
	}
	for id, msg := range cases {
		h.input("y\nac/" + id[4:] + "\n")
		if code := h.run("finish", id); code != 1 || !strings.Contains(h.err.String(), msg) {
			t.Errorf("%s: %d %q", id, code, h.err.String())
		}
	}
	for _, f := range []string{filepath.Join(outside, "keep"), filepath.Join(notWT, "keep"), real.Worktree} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s was touched", f)
		}
	}
	if len(h.registry().Tasks) != len(cases)+1 {
		t.Fatal("refused finishes must keep their entries")
	}
}

func TestFinishGoneAndPrune(t *testing.T) {
	h := taskHarness(t)
	h.run("api", "--task", "one")
	h.run("api", "--task", "two")
	h.run("api", "--task", "three")
	r := h.registry()
	one, _ := r.Get("api/one")
	two, _ := r.Get("api/two")
	os.RemoveAll(one.Worktree)
	os.RemoveAll(two.Worktree)

	if code := h.run("finish", "one"); code != 0 || !strings.Contains(h.out.String(), "already gone") {
		t.Fatalf("%d %s %s", code, h.out.String(), h.err.String())
	}
	if code := h.run("prune"); code != 0 || !strings.Contains(h.out.String(), "pruned api/two") || strings.Contains(h.out.String(), "three") {
		t.Fatalf("%d %s", code, h.out.String())
	}
	ids := []string{}
	for _, tk := range h.registry().Tasks {
		ids = append(ids, tk.ID)
	}
	if !reflect.DeepEqual(ids, []string{"api/three"}) {
		t.Fatal(ids)
	}
	if strings.Contains(gitOut(t, h.proj("api"), "worktree", "list"), "two") {
		t.Fatal("git still lists the pruned worktree")
	}
	if gitOut(t, h.proj("api"), "branch", "--list", "ac/two") == "" {
		t.Fatal("prune must keep branches")
	}
	h.run("prune")
	if !strings.Contains(h.out.String(), "nothing to prune") {
		t.Fatal(h.out.String())
	}
	// Prune sets a corrupt registry aside.
	os.WriteFile(h.env.TasksPath, []byte("nope"), 0o600)
	if code := h.run("prune"); code != 0 || !strings.Contains(h.err.String(), "setting it aside") {
		t.Fatal(h.err.String())
	}
	if m, _ := filepath.Glob(h.env.TasksPath + ".corrupt-*"); len(m) != 1 {
		t.Fatalf("backup %v", m)
	}
}

// The menu's running view: w lists tasks, Enter opens one (cd into the
// worktree for a here task; attach failures fall back to cd), f finishes.
func TestMenuRunningActions(t *testing.T) {
	h := taskHarness(t)
	h.run("api", "--task", "one")
	h.writeConfig(func(c *config.Config) { c.Backend = "tmux" })
	h.run("api", "--task", "two")
	one, _ := h.registry().Get("api/one")
	two, _ := h.registry().Get("api/two")

	os.Remove(h.cdFile)
	h.menu = []tui.Result{{Action: tui.ActTaskOpen, Task: "api/one"}}
	if code := h.run(); code != 0 {
		t.Fatal(h.err.String())
	}
	o := h.menuOpts[len(h.menuOpts)-1]
	if o.TaskCount != 2 || o.LoadTasks == nil {
		t.Fatalf("%+v", o)
	}
	rows := o.LoadTasks()
	if len(rows) != 2 || rows[0].ID != "api/one" || rows[0].Status != "unknown" || rows[0].Git != "clean" {
		t.Fatalf("%+v", rows)
	}
	if h.cdTarget() != one.Dir {
		t.Fatalf("cd %q", h.cdTarget())
	}

	// tmux task that looks alive: attach is tried (fails here: the fake
	// tmux path doesn't exist), then ac cds into the worktree instead.
	h.onPath["tmux"] = true
	h.env.Probe = worktree.Probe{Output: func(string, ...string) (string, int, error) { return "", 0, nil }}
	os.Remove(h.cdFile)
	h.menu = []tui.Result{{Action: tui.ActTaskOpen, Task: "api/two"}}
	h.run()
	if h.cdTarget() != two.Dir || !strings.Contains(h.err.String(), "can't attach") {
		t.Fatalf("cd %q err %q", h.cdTarget(), h.err.String())
	}

	// Finish from the menu asks on stdin, then the menu comes back.
	h.env.Probe = worktree.Probe{}
	h.input("y\n")
	h.menu = []tui.Result{{Action: tui.ActTaskFinish, Task: "api/one"}, {Action: tui.ActQuit}}
	n := len(h.menuOpts)
	if code := h.run(); code != 0 {
		t.Fatal(h.err.String())
	}
	if _, err := os.Stat(one.Worktree); !os.IsNotExist(err) {
		t.Fatal("finish from the menu")
	}
	if len(h.menuOpts) != n+2 || h.menuOpts[n+1].TaskCount != 1 {
		t.Fatal("menu must come back with one task left")
	}
}

// Without any task, the menu options are the phase 3 ones plus a zero
// TaskCount (the footer and goldens are unchanged).
func TestMenuWithoutTasksUnchanged(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha")
	h.run()
	if o := h.menuOpts[0]; o.TaskCount != 0 {
		t.Fatalf("%+v", o)
	}
	if _, err := os.Stat(h.env.TasksPath); err == nil {
		t.Fatal("menu must not create the registry")
	}
}

func TestDoctorTasks(t *testing.T) {
	h := taskHarness(t)
	h.run("doctor")
	out := h.out.String()
	for _, want := range []string{"-- Task worktrees --", "worktree dir: " + h.wtRoot() + " (default; created by the first --task)", "worktree support ok", "no task sessions recorded yet"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	h.run("api", "--task", "x")
	h.run("api", "--task", "y")
	y, _ := h.registry().Get("api/y")
	os.RemoveAll(y.Worktree)
	h.run("doctor")
	out = h.out.String()
	if !strings.Contains(out, "2 task sessions in") || !strings.Contains(out, "1 task whose worktree is gone (run 'ac prune')") {
		t.Fatal(out)
	}
	os.WriteFile(h.env.TasksPath, []byte("{"), 0o600)
	h.run("doctor")
	if !strings.Contains(h.out.String(), "task registry is corrupt") {
		t.Fatal(h.out.String())
	}
	h.writeConfig(func(c *config.Config) { c.WorktreeDir = h.root })
	if code := h.run("doctor"); code != 1 || !strings.Contains(h.out.String(), "must not be or contain the project root") {
		t.Fatalf("%d %s", code, h.out.String())
	}
}

var _ = exec.LookPath
