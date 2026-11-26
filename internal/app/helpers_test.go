package app

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/launch"
	"github.com/ericfaris/anycli-launcher/internal/tui"
)

// fakeLauncher records launches instead of running anything.
type fakeLauncher struct {
	reqs []launch.Request
	code int
	err  error
}

func (f *fakeLauncher) Launch(r launch.Request) (int, error) {
	f.reqs = append(f.reqs, r)
	if f.err == nil && r.CDFile != "" {
		launch.WriteCDFile(r.CDFile, r.Dir)
	}
	return f.code, f.err
}

type harness struct {
	t        *testing.T
	env      *Env
	out, err *bytes.Buffer
	home     string
	root     string
	cfgPath  string
	cdFile   string
	launcher *fakeLauncher
	onPath   map[string]bool
	ghCalls  [][]string
	ghAuthOK bool
	ghFail   bool
	menu     []tui.Result
	menuOpts []tui.Options
	now      time.Time
}

// newHarness creates a home with a projects root and a config with cli=claude.
func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("AC_PROJECTS_DIR", "")
	t.Setenv("AC_CONFIG", "")
	home := t.TempDir()
	h := &harness{
		t: t, home: home, root: filepath.Join(home, "projects"),
		cfgPath:  filepath.Join(home, ".config", "anycli-launcher", "config.toml"),
		cdFile:   filepath.Join(t.TempDir(), "cd"),
		out:      &bytes.Buffer{},
		err:      &bytes.Buffer{},
		launcher: &fakeLauncher{},
		onPath:   map[string]bool{"claude": true, "git": hasGit(), "gh": true},
		ghAuthOK: true,
		now:      time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
	}
	os.MkdirAll(h.root, 0o755)
	h.env = &Env{
		Stdin:      strings.NewReader(""),
		Stdout:     h.out,
		Stderr:     h.err,
		ConfigPath: h.cfgPath,
		Home:       home,
		LookPath: func(n string) (string, error) {
			if h.onPath[n] {
				if n == "git" {
					return exec.LookPath("git")
				}
				return "/fake/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		Run: func(dir, name string, args ...string) (string, error) {
			if name == "gh" {
				h.ghCalls = append(h.ghCalls, args)
				if args[0] == "auth" && !h.ghAuthOK {
					return "", errors.New("not logged in")
				}
				if args[0] == "repo" && h.ghFail {
					return "", errors.New("forbidden")
				}
				return "", nil
			}
			return DefaultRun(dir, name, args...)
		},
		Launcher:     h.launcher,
		FrecencyPath: filepath.Join(home, ".local", "state", "anycli-launcher", "frecency.json"),
		Now:          func() time.Time { return h.now },
		RunMenu: func(o tui.Options) (tui.Result, error) {
			h.menuOpts = append(h.menuOpts, o)
			if len(h.menu) == 0 {
				return tui.Result{Action: tui.ActQuit}, nil
			}
			r := h.menu[0]
			h.menu = h.menu[1:]
			return r, nil
		},
	}
	h.writeConfig(func(c *config.Config) {})
	return h
}

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func (h *harness) writeConfig(mut func(*config.Config)) {
	c := config.Default()
	c.ProjectsDir = h.root
	c.CLI = "claude"
	mut(&c)
	if err := config.Save(h.cfgPath, c); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) loadConfig() config.Config {
	c, _, err := config.Load(h.cfgPath)
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

func (h *harness) mkproj(rel ...string) {
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(h.root, r), 0o755); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) input(s string) { h.env.Stdin = strings.NewReader(s) }

// run invokes Main with the shim's --cd-file and returns the exit code.
func (h *harness) run(args ...string) int {
	h.out.Reset()
	h.err.Reset()
	return Main(append([]string{"--cd-file", h.cdFile}, args...), h.env)
}

func (h *harness) cdTarget() string {
	b, err := os.ReadFile(h.cdFile)
	if err != nil {
		return ""
	}
	return string(b)
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if !hasGit() {
		t.Skip("git not installed")
	}
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
