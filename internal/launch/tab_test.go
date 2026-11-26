package launch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeRunner records commands. out answers Output calls (nil: "", 0).
type fakeRunner struct {
	outputs []Command
	runs    []Command
	out     func(Command) (string, int, error)
	runCode int
}

func (f *fakeRunner) Output(c Command) (string, int, error) {
	f.outputs = append(f.outputs, c)
	if f.out != nil {
		return f.out(c)
	}
	return "", 0, nil
}

func (f *fakeRunner) Run(c Command) (int, error) {
	f.runs = append(f.runs, c)
	return f.runCode, nil
}

const (
	exe  = "/home/u/go/bin/ac"
	spec = "/tmp/ac-tab-1.json"
)

func TestBuildTab(t *testing.T) {
	tramp := []string{exe, "__tab-exec", spec}
	cases := []struct {
		name string
		inv  TabInvocation
		want []string
	}{
		{
			"wt from WSL (v3 shape, trampoline instead of bash script)",
			TabInvocation{Terminal: TermWT, Platform: PlatformWSL, Dir: "/home/u/projects/app", Title: "App", Color: "#ff8800", Distro: "Debian"},
			append([]string{"-w", "0", "new-tab", "--title", "App", "--tabColor", "#ff8800", "wsl.exe", "-d", "Debian", "--cd", "/home/u/projects/app", "--exec"}, tramp...),
		},
		{
			"wt from WSL, no title/color, default distro, profile kept",
			TabInvocation{Terminal: TermWT, Platform: PlatformWSL, Dir: "/p", ProfileID: "{61c54bbd}"},
			append([]string{"-w", "0", "new-tab", "--profile", "{61c54bbd}", "wsl.exe", "-d", "Ubuntu", "--cd", "/p", "--exec"}, tramp...),
		},
		{
			"wt native windows pins -d and separates with --",
			TabInvocation{Terminal: TermWT, Platform: PlatformWindows, Exe: `C:\Users\Jo Smith\bin\ac.exe`, Spec: `C:\Users\Jo Smith\AppData\Local\Temp\ac-tab-1.json`, Dir: `C:\src\a;b`, Title: "T"},
			[]string{"-w", "0", "new-tab", "--title", "T", "-d", `C:\src\a\;b`, "--", `C:\Users\Jo Smith\bin\ac.exe`, "__tab-exec", `C:\Users\Jo Smith\AppData\Local\Temp\ac-tab-1.json`},
		},
		{
			"kitty",
			TabInvocation{Terminal: TermKitty, Platform: PlatformLinux, Dir: "/p/x", Title: "X"},
			append([]string{"@", "launch", "--type=tab", "--cwd", "/p/x", "--tab-title", "X"}, tramp...),
		},
		{
			"kitty without title",
			TabInvocation{Terminal: TermKitty, Platform: PlatformMac, Dir: "/p/x"},
			append([]string{"@", "launch", "--type=tab", "--cwd", "/p/x"}, tramp...),
		},
		{
			"wezterm",
			TabInvocation{Terminal: TermWezTerm, Platform: PlatformLinux, Dir: "/p/x", Title: "ignored (OSC from trampoline)"},
			append([]string{"cli", "spawn", "--cwd", "/p/x", "--"}, tramp...),
		},
		{
			"gnome-terminal",
			TabInvocation{Terminal: TermGnome, Platform: PlatformLinux, Dir: "/p/x y"},
			append([]string{"--tab", "--working-directory=/p/x y", "--"}, tramp...),
		},
	}
	for _, c := range cases {
		if c.inv.Exe == "" {
			c.inv.Exe, c.inv.Spec = exe, spec
		}
		c.inv.Program = "PROG"
		got, err := BuildTab(c.inv)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got.Path != "PROG" || !reflect.DeepEqual(got.Args, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got.Args, c.want)
		}
	}
}

func TestBuildTabAppleScript(t *testing.T) {
	for _, term := range []Terminal{TermITerm, TermTerminalApp} {
		c, err := BuildTab(TabInvocation{Terminal: term, Program: "osascript", Platform: PlatformMac, Exe: exe, Spec: spec, Dir: `/p/"x"`, Title: `"; do shell script "touch /tmp/pwned`})
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Args) != 2 || c.Args[0] != "-e" {
			t.Fatalf("%s: %q", term, c.Args)
		}
		script := c.Args[1]
		want := `"exec '/home/u/go/bin/ac' __tab-exec '/tmp/ac-tab-1.json'"`
		if !strings.Contains(script, "write text "+want) && !strings.Contains(script, "do script "+want) {
			t.Fatalf("%s: script %s", term, script)
		}
		// User data never reaches AppleScript: it travels in the spec file.
		if strings.Contains(script, "pwned") || strings.Contains(script, `/p/`) {
			t.Fatalf("%s: user data leaked into the script:\n%s", term, script)
		}
	}
}

// Hostile project dirs and titles: the trampoline paths stay exact, nothing
// can add a wt subcommand (';'), and user data appears only as whole argv
// elements that were sanitized for their route.
func TestBuildTabHostileValues(t *testing.T) {
	for _, h := range hostile {
		for _, term := range []Terminal{TermWT, TermKitty, TermWezTerm, TermGnome} {
			for _, plat := range []string{PlatformWSL, PlatformWindows, PlatformLinux} {
				if term == TermWT && plat == PlatformLinux {
					continue
				}
				inv := TabInvocation{Terminal: term, Program: "P", Exe: exe, Spec: spec, Platform: plat, Dir: "/p/" + h, Title: h, Color: "#000000", ProfileID: h, Distro: h}
				c, err := BuildTab(inv)
				if err != nil {
					t.Fatalf("%s/%s %q: %v", term, plat, h, err)
				}
				n := len(c.Args)
				if !reflect.DeepEqual(c.Args[n-3:], []string{exe, "__tab-exec", spec}) {
					t.Fatalf("%s/%s %q: trampoline not last: %q", term, plat, h, c.Args)
				}
				for _, a := range c.Args {
					// The directory is a real path and passes through as is
					// (argv element); every other value must be clean.
					isDir := strings.Contains(a, "/p/"+h)
					if !isDir && strings.ContainsAny(a, "\n\r\x1b") {
						t.Fatalf("%s/%s %q: control char in %q", term, plat, h, a)
					}
					if term == TermWT && strings.Contains(strings.ReplaceAll(a, `\;`, ""), ";") {
						t.Fatalf("%s/%s %q: unescaped ; in %q", term, plat, h, a)
					}
				}
				if term == TermWT && plat == PlatformWSL {
					cd := c.Args[indexOf(c.Args, "--cd")+1]
					if cd != WSLCdArg("/p/"+h) {
						t.Fatalf("cd %q", cd)
					}
					if d := c.Args[indexOf(c.Args, "wsl.exe")+2]; !SafePath(d) {
						t.Fatalf("distro %q", d)
					}
				}
			}
		}
	}
}

func indexOf(a []string, s string) int {
	for i, x := range a {
		if x == s {
			return i
		}
	}
	return -1
}

func TestBuildTabRejectsUnsafeTrampolinePaths(t *testing.T) {
	for _, p := range []string{"/home/Jo Smith/ac", "/x/it's/ac", "/x/$(y)/ac"} {
		_, err := BuildTab(TabInvocation{Terminal: TermWT, Program: "wt", Platform: PlatformWSL, Exe: p, Spec: spec, Dir: "/p"})
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%q: %v", p, err)
		}
		_, err = BuildTab(TabInvocation{Terminal: TermITerm, Program: "osascript", Platform: PlatformMac, Exe: exe, Spec: p, Dir: "/p"})
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("spec %q: %v", p, err)
		}
	}
	if _, err := BuildTab(TabInvocation{Terminal: "xterm", Exe: exe, Spec: spec}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestSpecFor(t *testing.T) {
	argv := []string{"/bin/claude"}
	if s := SpecFor(TermWT, PlatformWSL, "/p", "T", "#123456", argv); !s.Login || s.ColorSeq != "" {
		t.Fatalf("wt/wsl: %+v", s)
	}
	if s := SpecFor(TermWT, PlatformWindows, "/p", "", "", argv); s.Login {
		t.Fatalf("windows has no login shell: %+v", s)
	}
	if s := SpecFor(TermITerm, PlatformMac, "/p", "", "#123456", argv); s.Login || s.ColorSeq != "iterm" {
		t.Fatalf("iterm: %+v", s)
	}
	if s := SpecFor(TermTerminalApp, PlatformMac, "/p", "", "", argv); s.Login {
		t.Fatalf("terminal-app: %+v", s)
	}
	for _, term := range []Terminal{TermKitty, TermWezTerm, TermGnome} {
		if s := SpecFor(term, PlatformLinux, "/p", "", "", argv); !s.Login {
			t.Fatalf("%s: %+v", term, s)
		}
	}
}

func tabDeps(t *testing.T, vars map[string]string, onPath map[string]string) (Deps, *fakeRunner, string) {
	t.Helper()
	tmp := t.TempDir()
	if !SafePath(tmp) {
		// Windows temp paths (C:\...) never pass SafePath; these tests
		// simulate Unix platforms. TestTabLaunchWindowsNative covers Windows.
		t.Skip("temp dir is not a POSIX-safe path here")
	}
	r := &fakeRunner{}
	return Deps{
		LookPath: func(n string) (string, error) {
			if p, ok := onPath[n]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Runner:  r,
		Env:     envOf("linux", vars, ""),
		Exe:     exe,
		TempDir: tmp,
	}, r, tmp
}

func TestTabLaunchWritesSpecAndRunsTerminal(t *testing.T) {
	d, r, tmp := tabDeps(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WT_SESSION": "s"},
		map[string]string{"wt.exe": "/mnt/c/wt.exe", "claude": "/usr/bin/claude"})
	cd := filepath.Join(t.TempDir(), "cd")
	prompt := "it's \"quoted\"; $(touch pwned)\nline2"
	code, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p/app", Name: "claude", Args: []string{"--model", "x", prompt}, Title: "App", Color: "#00ff00", CDFile: cd})
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	if len(r.outputs) != 1 || len(r.runs) != 0 {
		t.Fatalf("outputs %v runs %v", r.outputs, r.runs)
	}
	c := r.outputs[0]
	if c.Path != "/mnt/c/wt.exe" {
		t.Fatalf("path %s", c.Path)
	}
	specPath := c.Args[len(c.Args)-1]
	if filepath.Dir(specPath) != tmp || !strings.HasPrefix(filepath.Base(specPath), "ac-tab-") {
		t.Fatalf("spec %s", specPath)
	}
	s, err := ReadSpec(specPath)
	if err != nil {
		t.Fatal(err)
	}
	want := TabSpec{Dir: "/p/app", Argv: []string{"/usr/bin/claude", "--model", "x", prompt}, Title: "App", Color: "#00ff00", Login: true}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("spec %+v\nwant %+v", s, want)
	}
	if _, err := os.Stat(cd); err == nil {
		t.Fatal("tab launch must not cd the current shell")
	}
}

func TestTabLaunchUnavailable(t *testing.T) {
	// Plain Linux, no terminal variables: nothing detected, nothing written.
	d, r, tmp := tabDeps(t, nil, map[string]string{"claude": "/usr/bin/claude"})
	_, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p", Name: "claude"})
	if !errors.Is(err, ErrUnavailable) || len(r.outputs) != 0 {
		t.Fatalf("err %v outputs %v", err, r.outputs)
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "*")); len(m) != 0 {
		t.Fatalf("files written: %v", m)
	}
	// Kitty detected but its program is missing.
	d, _, _ = tabDeps(t, map[string]string{"KITTY_WINDOW_ID": "1"}, map[string]string{"claude": "/usr/bin/claude"})
	if _, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p", Name: "claude"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	// Unsafe ac path: unavailable, and no spec left behind.
	d, _, tmp = tabDeps(t, map[string]string{"KITTY_WINDOW_ID": "1"}, map[string]string{"claude": "/usr/bin/claude", "kitty": "/usr/bin/kitty"})
	d.Exe = "/home/Jo Smith/ac"
	if _, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p", Name: "claude"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "*")); len(m) != 0 {
		t.Fatalf("files written: %v", m)
	}
}

func TestTabLaunchCLIMissingAndSpawnFailure(t *testing.T) {
	d, _, _ := tabDeps(t, map[string]string{"KITTY_WINDOW_ID": "1"}, map[string]string{"kitty": "/usr/bin/kitty"})
	if code, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p", Name: "claude"}); !errors.Is(err, ErrNotFound) || code != 127 {
		t.Fatalf("code %d err %v", code, err)
	}
	d, r, tmp := tabDeps(t, map[string]string{"KITTY_WINDOW_ID": "1"}, map[string]string{"kitty": "/usr/bin/kitty", "claude": "/c"})
	r.out = func(Command) (string, int, error) { return "Error: remote control is disabled", 1, nil }
	_, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p", Name: "claude"})
	if err == nil || !strings.Contains(err.Error(), "remote control is disabled") {
		t.Fatalf("err %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "ac-tab-*")); len(m) != 0 {
		t.Fatalf("spec not cleaned up: %v", m)
	}
}

func TestTabLaunchForcedTerminal(t *testing.T) {
	d, r, _ := tabDeps(t, map[string]string{"KITTY_WINDOW_ID": "1"}, map[string]string{"wezterm": "/usr/bin/wezterm", "claude": "/c"})
	d.Terminal = "wezterm"
	if _, err := (&Tab{Deps: d}).Launch(Request{Dir: "/p", Name: "claude"}); err != nil {
		t.Fatal(err)
	}
	if r.outputs[0].Path != "/usr/bin/wezterm" {
		t.Fatalf("%v", r.outputs)
	}
}

func TestTabLaunchWindowsNative(t *testing.T) {
	tmp := t.TempDir()
	r := &fakeRunner{}
	e := envOf("windows", map[string]string{"WT_SESSION": "x", "LOCALAPPDATA": `C:\L`}, "")
	e.Stat = func(string) (os.FileInfo, error) { return nil, nil }
	d := Deps{
		LookPath: func(n string) (string, error) {
			if n == "claude" {
				return `C:\npm\claude.cmd`, nil
			}
			return "", errors.New("no")
		},
		Runner: r, Env: e, Exe: `C:\Program Files\ac\ac.exe`, TempDir: tmp,
	}
	if _, err := (&Tab{Deps: d}).Launch(Request{Dir: `C:\src\my app`, Name: "claude", Args: []string{"a;b"}, Title: "T;x"}); err != nil {
		t.Fatal(err)
	}
	c := r.outputs[0]
	if c.Path != filepath.Join(`C:\L`, "Microsoft", "WindowsApps", "wt.exe") {
		t.Fatalf("path %q", c.Path)
	}
	n := len(c.Args)
	wantHead := []string{"-w", "0", "new-tab", "--title", `T\;x`, "-d", `C:\src\my app`, "--", `C:\Program Files\ac\ac.exe`, "__tab-exec"}
	if !reflect.DeepEqual(c.Args[:n-1], wantHead) {
		t.Fatalf("args %q", c.Args)
	}
	s, err := ReadSpec(strings.ReplaceAll(c.Args[n-1], `\;`, ";"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Login || !reflect.DeepEqual(s.Argv, []string{`C:\npm\claude.cmd`, "a;b"}) {
		t.Fatalf("spec %+v", s)
	}
}

func TestSweepSpecs(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "ac-tab-old.json")
	fresh := filepath.Join(dir, "ac-tab-new.json")
	other := filepath.Join(dir, "other.json")
	for _, p := range []string{old, fresh, other} {
		os.WriteFile(p, []byte("{}"), 0o600)
	}
	now := time.Now()
	os.Chtimes(old, now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	os.Chtimes(other, now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	SweepSpecs(dir, now, 24*time.Hour)
	if _, err := os.Stat(old); err == nil {
		t.Fatal("old spec kept")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s removed", p)
		}
	}
}
