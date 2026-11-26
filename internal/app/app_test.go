package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/launch"
	"github.com/ericfaris/anycli-launcher/internal/tui"
)

func TestVersionHelpInit(t *testing.T) {
	h := newHarness(t)
	for _, a := range []string{"version", "--version", "-V"} {
		if h.run(a) != 0 || !strings.Contains(h.out.String(), "anycli-launcher "+Version) {
			t.Errorf("%s: %q", a, h.out.String())
		}
	}
	for _, a := range []string{"help", "--help", "-h"} {
		if h.run(a) != 0 || !strings.Contains(h.out.String(), "ac init <bash|zsh|fish|pwsh>") {
			t.Errorf("%s: %q", a, h.out.String())
		}
	}
	if h.run("init", "zsh") != 0 || !strings.Contains(h.out.String(), "command ac --cd-file") {
		t.Fatal("init zsh")
	}
	if h.run("init", "tcsh") != 2 || h.run("init") != 2 {
		t.Fatal("init errors")
	}
	if h.run("--bogus") != 2 || !strings.Contains(h.err.String(), "unknown option") {
		t.Fatal("unknown option")
	}
	if Main([]string{"--cd-file"}, h.env) != 2 {
		t.Fatal("--cd-file without value")
	}
}

func TestLaunchByName(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha", "ml-project", "beta")
	h.writeConfig(func(c *config.Config) {
		c.DefaultFlags = "--model opus"
		c.Projects = map[string]config.Project{
			"ml-project": {Alias: "ML", Flags: config.Str(`--agent "my agent"`), Title: "ML Project", Prompt: "help me with ML"},
			"beta":       {Flags: config.Str("none")},
		}
	})
	if code := h.run("ml"); code != 0 {
		t.Fatalf("code %d: %s", code, h.err.String())
	}
	got := h.launcher.reqs[0]
	want := launch.Request{
		Dir: filepath.Join(h.root, "ml-project"), Name: "claude",
		Args:  []string{"--model", "opus", "--agent", "my agent", "help me with ML"},
		Title: "ML Project", CDFile: h.cdFile,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if h.cdTarget() != want.Dir {
		t.Fatal("cd file")
	}

	h.run("beta", "--resume")
	if r := h.launcher.reqs[1]; !reflect.DeepEqual(r.Args, []string{"--continue"}) {
		t.Fatalf("beta resume: %q", r.Args)
	}
	h.run("pick", "ML")
	if r := h.launcher.reqs[2]; !reflect.DeepEqual(r.Args, []string{"--resume", "--model", "opus", "--agent", "my agent"}) {
		t.Fatalf("pick drops prompt: %q", r.Args)
	}
	h.run("-r", "alpha")
	h.run("resume", "alpha")
	if a, b := h.launcher.reqs[3].Args, h.launcher.reqs[4].Args; !reflect.DeepEqual(a, b) || a[0] != "--continue" {
		t.Fatalf("resume forms differ: %q %q", a, b)
	}
	h.run("--", "alpha")
	if len(h.launcher.reqs) != 6 {
		t.Fatal("-- form")
	}
}

func TestLaunchErrors(t *testing.T) {
	h := newHarness(t)
	h.mkproj("my-app", "my-api")
	if h.run("my") != 1 || !strings.Contains(h.err.String(), "more than one") {
		t.Fatal("ambiguous")
	}
	if h.run("zzz") != 1 || !strings.Contains(h.err.String(), "no project matching") {
		t.Fatal("no match")
	}
	for _, args := range [][]string{{"resume"}, {"pick"}, {"--resume"}, {"--"}} {
		if h.run(args...) != 2 {
			t.Errorf("%v should be a usage error", args)
		}
	}
	if len(h.launcher.reqs) != 0 {
		t.Fatal("nothing should launch")
	}

	h.writeConfig(func(c *config.Config) { c.CLI = "" })
	if h.run("my-app") != 1 || !strings.Contains(h.err.String(), "ac setup") {
		t.Fatal("no cli")
	}

	h.writeConfig(func(c *config.Config) { c.ProjectsDir = filepath.Join(h.home, "missing") })
	if h.run("my-app") != 1 || !strings.Contains(h.err.String(), "projects directory not found") {
		t.Fatal("missing root")
	}
}

func TestLaunchNotFoundOnPath(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha")
	h.env.Launcher = nil // real Here backend
	h.onPath["claude"] = false
	if code := h.run("alpha"); code != 127 || !strings.Contains(h.err.String(), "not found on PATH") {
		t.Fatalf("code %d: %s", code, h.err.String())
	}
	if h.cdTarget() != "" {
		t.Fatal("must not cd when nothing launched")
	}
}

func TestPerProjectCLIAndLaunchOverride(t *testing.T) {
	h := newHarness(t)
	h.mkproj("k", "c")
	h.writeConfig(func(c *config.Config) {
		c.CLI = "kiro-cli"
		c.CLILaunch = "chat --x"
		c.Projects = map[string]config.Project{"c": {CLI: "claude"}}
	})
	os.WriteFile(filepath.Join(h.root, "k", config.OverlayFile), []byte(`prompt = "from repo"`+"\n"), 0o644)
	h.run("k")
	h.run("c")
	if r := h.launcher.reqs[0]; r.Name != "kiro-cli" || !reflect.DeepEqual(r.Args, []string{"chat", "--x", "from repo"}) {
		t.Fatalf("kiro: %+v", r)
	}
	// A project on another CLI must not inherit kiro's launch subcommand.
	if r := h.launcher.reqs[1]; r.Name != "claude" || len(r.Args) != 0 {
		t.Fatalf("claude: %+v", r)
	}
}

func TestProjectNamePrecedence(t *testing.T) {
	h := newHarness(t)
	h.mkproj("doctor", "init")
	if h.run("doctor") != 0 || len(h.launcher.reqs) != 1 {
		t.Fatal("project named doctor must launch")
	}
	// init is never shadowed: the shim depends on it.
	if h.run("init", "bash") != 0 || len(h.launcher.reqs) != 1 {
		t.Fatal("init must stay a subcommand")
	}
}

func TestList(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha", "ml-project")
	h.writeConfig(func(c *config.Config) {
		c.Projects = map[string]config.Project{"ml-project": {Alias: "ML"}}
	})
	if h.run("list") != 0 || h.out.String() != "alpha\nml-project\tML\n" {
		t.Fatalf("%q", h.out.String())
	}
}

func TestAutoMigrateOnFirstLoad(t *testing.T) {
	h := newHarness(t)
	os.Remove(h.cfgPath)
	h.mkproj("public-knowledge-network")
	yaml := "projects_dir: \"" + filepath.ToSlash(h.root) + "\"\ncli_tool: claude\nprojects:\n  public_knowledge_network:\n    alias: PKN\n"
	os.WriteFile(filepath.Join(filepath.Dir(h.cfgPath), "config.yaml"), []byte(yaml), 0o644)
	if h.run("list") != 0 {
		t.Fatal(h.err.String())
	}
	if !strings.Contains(h.err.String(), "imported v3 config") || !strings.Contains(h.out.String(), "\tPKN") {
		t.Fatalf("out=%q err=%q", h.out.String(), h.err.String())
	}
	if c := h.loadConfig(); c.CLI != "claude" || c.Projects["public-knowledge-network"].Alias != "PKN" {
		t.Fatalf("%+v", c)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.cfgPath), "config.yaml")); err != nil {
		t.Fatal("v3 file must be left in place")
	}
}

func TestMigrateCommand(t *testing.T) {
	h := newHarness(t)
	src, _ := filepath.Abs("../../tests/fixtures/config.yaml")
	if h.run("migrate", src) != 1 || !strings.Contains(h.err.String(), "already exists") {
		t.Fatal("must refuse to overwrite")
	}
	if h.run("migrate", "--force", src) != 0 {
		t.Fatal(h.err.String())
	}
	if c := h.loadConfig(); c.CLI != "kiro-cli" || c.Projects["ml-project"].Alias != "ML" {
		t.Fatalf("%+v", c)
	}
	if h.run("migrate", "--force", filepath.Join(h.home, "nope.yaml")) != 1 || !strings.Contains(h.err.String(), "no v3 config") {
		t.Fatal(h.err.String())
	}
}

func TestMenuFlow(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha", "beta")
	os.MkdirAll(filepath.Join(h.home, ".claude"), 0o755)
	h.writeConfig(func(c *config.Config) {
		c.CLIConfigDir = filepath.Join(h.home, ".claude")
		c.ShowGitStatus = true
		c.Theme = "dusk"
	})
	h.menu = []tui.Result{{Action: tui.ActSelect, Entry: tui.Entry{Kind: tui.KindProject, Dir: filepath.Join(h.root, "beta")}, Mode: 1}}
	if h.run() != 0 {
		t.Fatal(h.err.String())
	}
	o := h.menuOpts[0]
	var labels []string
	for _, e := range o.Entries {
		labels = append(labels, e.Label)
	}
	if !reflect.DeepEqual(labels, []string{"terminal", "projects", ".claude", "alpha", "beta"}) {
		t.Fatalf("entries %v", labels)
	}
	if !o.ShowGit || !o.CanResume || !o.CanPick || o.Theme.Name != "dusk" || o.CLI != "claude" {
		t.Fatalf("opts %+v", o)
	}
	if r := h.launcher.reqs[0]; r.Dir != filepath.Join(h.root, "beta") || r.Args[0] != "--continue" {
		t.Fatalf("%+v", r)
	}
}

func TestMenuTerminalRootAndQuit(t *testing.T) {
	h := newHarness(t)
	h.menu = []tui.Result{{Action: tui.ActSelect, Entry: tui.Entry{Kind: tui.KindTerminal, Dir: h.root}}}
	if h.run() != 0 || h.cdTarget() != h.root || len(h.launcher.reqs) != 0 {
		t.Fatal("terminal must cd without launching")
	}
	h.menu = []tui.Result{{Action: tui.ActSelect, Entry: tui.Entry{Kind: tui.KindRoot, Dir: h.root}}}
	if h.run() != 0 || len(h.launcher.reqs) != 1 || h.launcher.reqs[0].Dir != h.root {
		t.Fatal("root must launch in root")
	}
	if h.run() != 0 {
		t.Fatal("quit")
	}
	// Without the shim, terminal explains how to install it.
	h.menu = []tui.Result{{Action: tui.ActSelect, Entry: tui.Entry{Kind: tui.KindTerminal, Dir: h.root}}}
	h.out.Reset()
	h.err.Reset()
	if Main(nil, h.env) != 0 || !strings.Contains(h.err.String(), "ac init") {
		t.Fatal(h.err.String())
	}
}

func TestMenuNewRemoveConfigLoop(t *testing.T) {
	h := newHarness(t)
	h.onPath["git"] = false
	h.mkproj("old")
	var edited string
	h.env.Editor = func(p string) error { edited = p; return nil }
	h.menu = []tui.Result{
		{Action: tui.ActNew},
		{Action: tui.ActRemove, Entry: tui.Entry{Kind: tui.KindProject, Dir: filepath.Join(h.root, "old")}},
		{Action: tui.ActConfig},
	}
	h.input("fresh\nn\ny\n")
	if h.run() != 0 {
		t.Fatal(h.err.String())
	}
	if _, err := os.Stat(filepath.Join(h.root, "fresh")); err != nil {
		t.Fatal("new project not created")
	}
	if _, err := os.Stat(filepath.Join(h.root, "old")); !os.IsNotExist(err) {
		t.Fatal("old not removed")
	}
	if edited != h.cfgPath {
		t.Fatal("editor not opened")
	}
	if len(h.menuOpts) != 4 {
		t.Fatalf("menu should reopen after each action, got %d", len(h.menuOpts))
	}
	last := h.menuOpts[3].Entries
	if last[len(last)-1].Label != "fresh" {
		t.Fatalf("menu not refreshed: %+v", last)
	}
	if h.cdTarget() != "" {
		t.Fatal("menu new must not cd")
	}
}

func TestEditConfigWithoutEditor(t *testing.T) {
	h := newHarness(t)
	os.Remove(h.cfgPath)
	h.env.IsTTY = false
	if h.run("config") != 0 || !strings.Contains(h.out.String(), h.cfgPath) || !config.Exists(h.cfgPath) {
		t.Fatal(h.out.String())
	}
}

func TestNonTTYWithoutConfigErrors(t *testing.T) {
	h := newHarness(t)
	os.Remove(h.cfgPath)
	h.env.IsTTY = false
	// Default projects_dir ~/projects resolves against the real home, not the
	// harness one; point it somewhere missing to get the v3 error path.
	t.Setenv("AC_PROJECTS_DIR", filepath.Join(h.home, "nowhere"))
	if h.run() != 1 || !strings.Contains(h.err.String(), "projects directory not found") {
		t.Fatal(h.err.String())
	}
	if len(h.menuOpts) != 0 {
		t.Fatal("menu must not open")
	}
}
