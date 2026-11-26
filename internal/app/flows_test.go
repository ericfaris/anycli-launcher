package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/internal/config"
)

func TestValidateNewName(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "taken"), 0o755)
	for name, ok := range map[string]bool{
		"app": true, "my-app_2.0": true, "9lives": true,
		"": false, "a/b": false, `a\b`: false, "-x": false, ".hidden": false, "sp ace": false, "taken": false,
	} {
		if err := ValidateNewName(root, name); (err == nil) != ok {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestNewProjectCreatesGitInitsAndSaves(t *testing.T) {
	h := newHarness(t)
	if !hasGit() {
		t.Skip("git not installed")
	}
	// prompt for name: invalid, then good; settings: alias, bad color, good color, title, prompt
	h.input("bad/name\nfresh\ny\nFresh App\nnotacolor\n#00cc66\nFresh\nhelp me\n")
	if h.run("new") != 0 {
		t.Fatal(h.out.String(), h.err.String())
	}
	dir := filepath.Join(h.root, "fresh")
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatal("git init missing")
	}
	out := h.out.String()
	if !strings.Contains(out, "path separator") || !strings.Contains(out, "6-digit hex") {
		t.Fatal(out)
	}
	p := h.loadConfig().Projects["fresh"]
	if p.Alias != "Fresh App" || p.Color != "#00cc66" || p.Title != "Fresh" || p.Prompt != "help me" {
		t.Fatalf("%+v", p)
	}
	if h.cdTarget() != dir {
		t.Fatal("ac new should cd into the new project")
	}
}

func TestNewProjectByArgNoGitNoSettings(t *testing.T) {
	h := newHarness(t)
	h.onPath["git"] = false
	h.input("n\n")
	if h.run("new", "quick") != 0 || !strings.Contains(h.out.String(), "git not found") {
		t.Fatal(h.out.String())
	}
	if _, ok := h.loadConfig().Projects["quick"]; ok {
		t.Fatal("no settings should be saved")
	}
	if h.run("new", "quick") != 1 || !strings.Contains(h.err.String(), "already exists") {
		t.Fatal("duplicate")
	}
	h.input("\n")
	if h.run("new") != 1 {
		t.Fatal("blank name cancels")
	}
}

func TestSetupKeepsAndChanges(t *testing.T) {
	h := newHarness(t)
	h.onPath["codex"] = true
	h.writeConfig(func(c *config.Config) {
		c.CLI, c.CLILaunch, c.DefaultFlags, c.Theme = "kiro-cli", "chat", "--trust-all-tools", "mono"
		c.Projects = map[string]config.Project{"app": {Alias: "A"}}
	})
	// keep root, keep CLI, keep flags
	h.input("\n\n\n")
	if h.run("setup") != 0 {
		t.Fatal(h.err.String())
	}
	c := h.loadConfig()
	if c.CLI != "kiro-cli" || c.CLILaunch != "chat" || c.DefaultFlags != "--trust-all-tools" || c.Theme != "mono" || c.Projects["app"].Alias != "A" {
		t.Fatalf("keep: %+v", c)
	}
	// new root (created), switch CLI by number (claude is #1), clear flags
	newRoot := filepath.Join(h.home, "code")
	h.input(newRoot + "\ny\n1\n-\n")
	if h.run("setup") != 0 {
		t.Fatal(h.err.String())
	}
	c = h.loadConfig()
	if c.ProjectsDir != config.Tildify(newRoot) || c.CLI != "claude" || c.CLILaunch != "" || c.DefaultFlags != "" || c.Projects["app"].Alias != "A" {
		t.Fatalf("change: %+v", c)
	}
	if _, err := os.Stat(newRoot); err != nil {
		t.Fatal("root not created")
	}
	h.input("")
	if h.run("setup") != 1 {
		t.Fatal("EOF should cancel")
	}
}

func TestWizardPicksCandidate(t *testing.T) {
	h := newHarness(t)
	os.Remove(h.cfgPath)
	h.env.IsTTY = true
	h.mkproj("one")
	os.MkdirAll(filepath.Join(h.home, "code", "x"), 0o755)
	t.Setenv("HOME", h.home)
	t.Setenv("USERPROFILE", h.home)
	h.input("1\n2\n--fast\n")
	if h.run() != 0 {
		t.Fatal(h.out.String(), h.err.String())
	}
	out := h.out.String()
	if !strings.Contains(out, "~/projects  (recommended)") || !strings.Contains(out, "~/code") {
		t.Fatal(out)
	}
	c := h.loadConfig()
	if c.ProjectsDir != "~/projects" || c.CLI != "claude" || c.DefaultFlags != "--fast" {
		t.Fatalf("%+v", c)
	}
	if len(h.menuOpts) != 1 {
		t.Fatal("menu should open right after setup")
	}
}

func TestWizardFreshMachine(t *testing.T) {
	h := newHarness(t)
	os.Remove(h.cfgPath)
	os.Remove(h.root)
	h.env.IsTTY = true
	t.Setenv("HOME", h.home)
	t.Setenv("USERPROFILE", h.home)
	h.onPath = map[string]bool{}
	// Enter = ~/projects, create it, type a CLI, no flags
	h.input("\ny\nmycli\n\n")
	if h.run() != 0 {
		t.Fatal(h.out.String(), h.err.String())
	}
	c := h.loadConfig()
	if c.CLI != "mycli" || c.ProjectsDir != "~/projects" {
		t.Fatalf("%+v", c)
	}
	if _, err := os.Stat(h.root); err != nil {
		t.Fatal("~/projects not created")
	}
}

func TestWizardCancel(t *testing.T) {
	for _, in := range []string{"", "\nn\n", "\ny\n\n"} {
		h := newHarness(t)
		os.Remove(h.cfgPath)
		os.Remove(h.root)
		h.env.IsTTY = true
		t.Setenv("HOME", h.home)
		t.Setenv("USERPROFILE", h.home)
		h.onPath = map[string]bool{}
		h.input(in)
		if h.run() != 1 || config.Exists(h.cfgPath) {
			t.Errorf("input %q should cancel without writing config", in)
		}
	}
}

func TestDoctor(t *testing.T) {
	h := newHarness(t)
	h.mkproj("a", "bad")
	os.WriteFile(filepath.Join(h.root, "bad", config.OverlayFile), []byte("cli = \"./x\"\n"), 0o644)
	code := h.run("doctor")
	out := h.out.String()
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, s := range []string{"config file present", "2 projects", "claude on PATH", "profile verified", "resume: --continue", "shell integration active", "Healthy"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in\n%s", s, out)
		}
	}
	if !strings.Contains(out, "bad: ") {
		t.Errorf("overlay error not reported\n%s", out)
	}

	h.writeConfig(func(c *config.Config) { c.CLI = "aider"; c.Theme = "neon"; c.ExtraDirs = []string{"/no/such"} })
	h.onPath["gh"] = false
	code = Main([]string{"doctor"}, h.env)
	out = h.out.String()
	if code != 1 {
		t.Fatalf("missing cli must fail\n%s", out)
	}
	for _, s := range []string{"aider not on PATH", "not verified", "unknown theme", "extra dir not found", "shell integration not active", "gh not on PATH", "Problems found"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in\n%s", s, out)
		}
	}

	h.writeConfig(func(c *config.Config) { c.ProjectsDir = filepath.Join(h.home, "gone") })
	if h.run("doctor") != 1 || !strings.Contains(h.out.String(), "projects directory not found") {
		t.Fatal(h.out.String())
	}
}
