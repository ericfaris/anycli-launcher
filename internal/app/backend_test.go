package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/v4/internal/config"
	"github.com/ericfaris/anycli-launcher/v4/internal/launch"
	"github.com/ericfaris/anycli-launcher/v4/internal/tui"
)

// Regression: with no backend configured and no color/title, a launch is
// exactly phase 2's: the here backend only, the same request (the new
// fields are zero apart from Session, which Here ignores), nothing on
// stderr.
func TestDefaultBackendIsHere(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha")
	if code := h.run("alpha"); code != 0 {
		t.Fatalf("code %d: %s", code, h.err.String())
	}
	if !reflect.DeepEqual(h.backends, []string{"here"}) {
		t.Fatalf("backends %v", h.backends)
	}
	want := launch.Request{Dir: filepath.Join(h.root, "alpha"), Name: "claude", CDFile: h.cdFile, Session: "alpha"}
	if got := h.launcher.reqs[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if h.err.Len() != 0 || h.out.Len() != 0 {
		t.Fatalf("output: %q %q", h.out.String(), h.err.String())
	}
	if h.cdTarget() != want.Dir {
		t.Fatal("cd file")
	}
}

func TestAutoTabFallsBackToHereSilently(t *testing.T) {
	h := newHarness(t)
	h.mkproj("app")
	h.writeConfig(func(c *config.Config) {
		c.Projects = map[string]config.Project{"app": {Color: "#112233", Title: "App"}}
	})
	h.unavail["tab"] = true
	if code := h.run("app"); code != 0 {
		t.Fatalf("code %d: %s", code, h.err.String())
	}
	if !reflect.DeepEqual(h.backends, []string{"tab", "here"}) {
		t.Fatalf("backends %v", h.backends)
	}
	if h.err.Len() != 0 {
		t.Fatalf("auto fallback must be silent (v3): %q", h.err.String())
	}
	// Same request both times, color and title included.
	tabReq, hereReq := h.unavailReqs[0], h.launcher.reqs[0]
	if !reflect.DeepEqual(tabReq, hereReq) || hereReq.Color != "#112233" || hereReq.Title != "App" {
		t.Fatalf("tab %+v here %+v", tabReq, hereReq)
	}
	if h.cdTarget() != filepath.Join(h.root, "app") {
		t.Fatal("here fallback must cd")
	}
}

func TestExplicitBackendFallbackIsAnnounced(t *testing.T) {
	h := newHarness(t)
	h.mkproj("app")
	h.writeConfig(func(c *config.Config) { c.Backend = "tmux" })
	h.unavail["tmux"] = true
	if code := h.run("app"); code != 0 {
		t.Fatal(code)
	}
	if !reflect.DeepEqual(h.backends, []string{"tmux", "here"}) {
		t.Fatalf("backends %v", h.backends)
	}
	if !strings.Contains(h.err.String(), "tmux backend unavailable (test says no); launching here") {
		t.Fatalf("stderr %q", h.err.String())
	}
}

func TestBackendPrecedence(t *testing.T) {
	h := newHarness(t)
	h.mkproj("a", "b", "c")
	h.writeConfig(func(c *config.Config) {
		c.Backend = "tab"
		c.Projects = map[string]config.Project{"a": {Backend: "zellij"}}
	})
	os.WriteFile(filepath.Join(h.root, "b", config.OverlayFile), []byte(`backend = "tmux"`+"\n"), 0o644)
	h.run("a")
	h.run("b")
	h.run("c")
	h.run("--backend", "here", "a")
	h.run("--backend=tmux", "c")
	h.run("c", "--backend", "here")
	want := []string{"zellij", "tmux", "tab", "here", "tmux", "here"}
	if !reflect.DeepEqual(h.backends, want) {
		t.Fatalf("got %v want %v", h.backends, want)
	}
}

func TestBackendFlagErrors(t *testing.T) {
	h := newHarness(t)
	h.mkproj("a")
	if h.run("--backend", "screen", "a") != 2 || !strings.Contains(h.err.String(), `unknown backend "screen"`) {
		t.Fatalf("%q", h.err.String())
	}
	if h.run("--backend") != 2 {
		t.Fatal("missing value")
	}
	if len(h.backends) != 0 {
		t.Fatal("nothing should launch")
	}
}

func TestBadBackendInConfigAndOverlay(t *testing.T) {
	h := newHarness(t)
	h.mkproj("a", "b")
	h.writeConfig(func(c *config.Config) { c.Backend = "tmx" })
	h.run("a")
	if !reflect.DeepEqual(h.backends, []string{"here"}) || !strings.Contains(h.err.String(), `unknown backend "tmx" in config; using auto`) {
		t.Fatalf("%v %q", h.backends, h.err.String())
	}
	// A repo's .ac.toml naming a command is rejected as a whole.
	h.writeConfig(func(c *config.Config) {})
	os.WriteFile(filepath.Join(h.root, "b", config.OverlayFile), []byte("backend = \"sh -c 'touch pwned'\"\ntitle = \"T\"\n"), 0o644)
	h.backends = nil
	h.run("b")
	if !reflect.DeepEqual(h.backends, []string{"here"}) || !strings.Contains(h.err.String(), "unknown backend") {
		t.Fatalf("%v %q", h.backends, h.err.String())
	}
}

func TestRequestFieldsForBackends(t *testing.T) {
	h := newHarness(t)
	h.mkproj("work/api", "work/web") // scan_depth 2: "work" is a group
	h.writeConfig(func(c *config.Config) {
		c.ScanDepth = 2
		c.Backend = "tmux"
		c.Projects = map[string]config.Project{"api": {Color: "not-a-color", Title: "API"}}
	})
	if code := h.run("api", "--resume"); code != 0 {
		t.Fatalf("%d %s", code, h.err.String())
	}
	r := h.launcher.reqs[0]
	if r.Session != "work/api" || !r.Resume || r.Color != "" || r.Title != "API" {
		t.Fatalf("%+v", r)
	}
	h.run("web")
	if r := h.launcher.reqs[1]; r.Resume || r.Session != "work/web" {
		t.Fatalf("%+v", r)
	}
}

func TestTerminalConfigReachesDeps(t *testing.T) {
	h := newHarness(t)
	h.mkproj("a")
	h.writeConfig(func(c *config.Config) { c.Terminal = "kitty" })
	h.run("a")
	if h.deps.Terminal != "kitty" {
		t.Fatalf("%+v", h.deps)
	}
	h.writeConfig(func(c *config.Config) { c.Terminal = "sh -c x" })
	h.run("a")
	if h.deps.Terminal != "" {
		t.Fatalf("invalid terminal must not reach the backend: %q", h.deps.Terminal)
	}
}

func TestMenuHeaderShowsBackend(t *testing.T) {
	cases := []struct {
		name   string
		cfg    func(*config.Config)
		args   []string
		vars   map[string]string
		onPath []string
		want   string
	}{
		{"default, no terminal", func(*config.Config) {}, nil, nil, nil, "auto: here"},
		{"default in Windows Terminal", func(*config.Config) {}, nil, map[string]string{"WT_SESSION": "1"}, []string{"wt.exe"}, "auto: wt"},
		{"WT_SESSION but no wt.exe", func(*config.Config) {}, nil, map[string]string{"WT_SESSION": "1"}, nil, "auto: here"},
		{"tab in kitty", func(c *config.Config) { c.Backend = "tab" }, nil, map[string]string{"KITTY_WINDOW_ID": "1"}, []string{"kitty"}, "tab: kitty"},
		{"tab, nothing", func(c *config.Config) { c.Backend = "tab" }, nil, nil, nil, "tab: none"},
		{"tmux", func(c *config.Config) { c.Backend = "tmux" }, nil, nil, nil, "tmux"},
		{"flag wins", func(c *config.Config) { c.Backend = "tmux" }, []string{"--backend", "here"}, nil, nil, "here"},
		{"typo shows auto", func(c *config.Config) { c.Backend = "tmx" }, nil, nil, nil, "auto: here"},
	}
	for _, c := range cases {
		h := newHarness(t)
		h.mkproj("a")
		h.writeConfig(c.cfg)
		for k, v := range c.vars {
			h.vars[k] = v
		}
		for _, p := range c.onPath {
			h.onPath[p] = true
		}
		h.menu = []tui.Result{{Action: tui.ActQuit}}
		h.run(c.args...)
		if len(h.menuOpts) != 1 || h.menuOpts[0].Backend != c.want {
			t.Errorf("%s: got %+v", c.name, h.menuOpts)
		}
	}
}

func TestTabExecDispatchSkipsConfig(t *testing.T) {
	h := newHarness(t)
	os.WriteFile(h.cfgPath, []byte("not = [valid toml"), 0o644)
	if code := Main([]string{launch.TabExecCommand}, h.env); code != 2 || !strings.Contains(h.err.String(), "usage: ac __tab-exec") {
		t.Fatalf("code %d %q", code, h.err.String())
	}
}

func TestDoctorBackends(t *testing.T) {
	h := newHarness(t)
	h.mkproj("a")
	h.run("doctor")
	out := h.out.String()
	for _, s := range []string{"-- Launch backend --", "backend: auto (default", "tab terminal: none detected", "tmux: not installed", "not yet checked on a real terminal: gnome-terminal, iterm, kitty, terminal-app, wezterm, wt, zellij", "Healthy"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q:\n%s", s, out)
		}
	}

	h.writeConfig(func(c *config.Config) {
		c.Backend = "tmx"
		c.Terminal = "xterm"
		c.Projects = map[string]config.Project{"a": {Backend: "screen", Color: "red"}}
	})
	h.vars["KITTY_WINDOW_ID"] = "1"
	h.onPath["kitty"] = true
	h.run("doctor")
	out = h.out.String()
	for _, s := range []string{`unknown backend "tmx"`, `unknown terminal "xterm"`, `a: unknown backend "screen"`, `a: color "red" is not #rrggbb`,
		"tab terminal: kitty (KITTY_WINDOW_ID) -> /fake/bin/kitty", "! unverified: kitty", "allow_remote_control", "Healthy"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q:\n%s", s, out)
		}
	}

	// backend = tmux without tmux: a warning, still healthy. With tmux:
	// version shown, and inside tmux noted.
	h.writeConfig(func(c *config.Config) { c.Backend = "tmux" })
	h.run("doctor")
	if out := h.out.String(); !strings.Contains(out, "! tmux not on PATH (backend = tmux falls back to here)") || !strings.Contains(out, "Healthy") {
		t.Errorf("%s", out)
	}
	h.onPath["tmux"] = true
	h.vars["TMUX"] = "/tmp/x,1,0"
	h.env.Run = func(dir, name string, args ...string) (string, error) {
		if name == "/fake/bin/tmux" && args[0] == "-V" {
			return "tmux 3.4", nil
		}
		return "", nil
	}
	h.run("doctor")
	if out := h.out.String(); !strings.Contains(out, "✓ tmux -> /fake/bin/tmux (tmux 3.4)") || !strings.Contains(out, "inside tmux") {
		t.Errorf("%s", out)
	}
	h.run("--backend", "zellij", "doctor")
	if out := h.out.String(); !strings.Contains(out, "backend: zellij (--backend)") || !strings.Contains(out, "! zellij not on PATH") {
		t.Errorf("%s", out)
	}
}

func TestBackendNamesInSync(t *testing.T) {
	if !reflect.DeepEqual(config.Backends, launch.Backends) {
		t.Fatalf("config %v launch %v", config.Backends, launch.Backends)
	}
}
