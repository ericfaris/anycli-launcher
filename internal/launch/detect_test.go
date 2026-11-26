package launch

import (
	"errors"
	"os"
	"testing"
)

func envOf(goos string, vars map[string]string, proc string) Environ {
	return Environ{
		GOOS:        goos,
		Getenv:      func(k string) string { return vars[k] },
		ProcVersion: func() string { return proc },
		Glob:        func(string) ([]string, error) { return nil, nil },
		Stat:        func(string) (os.FileInfo, error) { return nil, errors.New("no") },
	}
}

func TestPlatform(t *testing.T) {
	cases := []struct {
		goos string
		vars map[string]string
		proc string
		want string
	}{
		{"linux", nil, "Linux version 6.1 (gcc)", PlatformLinux},
		{"linux", nil, "Linux version 6.6.87.2-microsoft-standard-WSL2", PlatformWSL},
		{"linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, "", PlatformWSL},
		{"linux", map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}, "", PlatformWSL},
		{"windows", map[string]string{"MSYSTEM": "MINGW64"}, "", PlatformWindows},
		{"darwin", nil, "", PlatformMac},
		{"freebsd", nil, "", PlatformLinux},
	}
	for _, c := range cases {
		if got := envOf(c.goos, c.vars, c.proc).Platform(); got != c.want {
			t.Errorf("%s %v %q: got %s want %s", c.goos, c.vars, c.proc, got, c.want)
		}
	}
}

func TestDetectTerminal(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		vars    map[string]string
		forced  string
		wtFound bool
		want    Terminal
	}{
		{"nothing", "linux", nil, "", false, TermNone},
		{"wt session", "linux", map[string]string{"WT_SESSION": "x", "WSL_DISTRO_NAME": "U"}, "", false, TermWT},
		{"wt profile only", "windows", map[string]string{"WT_PROFILE_ID": "{g}"}, "", false, TermWT},
		{"wsl with wt.exe (v3 parity)", "linux", map[string]string{"WSL_DISTRO_NAME": "U"}, "", true, TermWT},
		{"windows with wt.exe", "windows", nil, "", true, TermWT},
		{"plain linux ignores wtFound", "linux", nil, "", true, TermNone},
		{"iterm", "darwin", map[string]string{"TERM_PROGRAM": "iTerm.app"}, "", false, TermITerm},
		{"terminal.app", "darwin", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, "", false, TermTerminalApp},
		{"kitty", "linux", map[string]string{"KITTY_WINDOW_ID": "1"}, "", false, TermKitty},
		{"wezterm pane", "linux", map[string]string{"WEZTERM_PANE": "3"}, "", false, TermWezTerm},
		{"wezterm term_program", "darwin", map[string]string{"TERM_PROGRAM": "WezTerm"}, "", false, TermWezTerm},
		{"gnome", "linux", map[string]string{"GNOME_TERMINAL_SCREEN": "/org/x"}, "", false, TermGnome},
		{"gnome service", "linux", map[string]string{"GNOME_TERMINAL_SERVICE": ":1.2"}, "", false, TermGnome},
		{"wezterm inside wt: innermost wins", "windows", map[string]string{"WT_SESSION": "x", "WEZTERM_PANE": "1"}, "", true, TermWezTerm},
		{"kitty launched from gnome", "linux", map[string]string{"GNOME_TERMINAL_SCREEN": "x", "KITTY_WINDOW_ID": "2"}, "", false, TermKitty},
		{"forced", "linux", map[string]string{"KITTY_WINDOW_ID": "2"}, "wezterm", false, TermWezTerm},
		{"forced invalid is ignored", "linux", map[string]string{"KITTY_WINDOW_ID": "2"}, "xterm", false, TermKitty},
	}
	for _, c := range cases {
		got := DetectTerminal(envOf(c.goos, c.vars, ""), c.forced, c.wtFound)
		if got.Terminal != c.want || got.Reason == "" {
			t.Errorf("%s: got %+v want %s", c.name, got, c.want)
		}
	}
}

func TestFindWT(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("no") }
	e := envOf("linux", map[string]string{"WSL_DISTRO_NAME": "U"}, "")
	e.Glob = func(p string) ([]string, error) {
		if p != "/mnt/c/Users/*/AppData/Local/Microsoft/WindowsApps/wt.exe" {
			t.Fatalf("glob %q", p)
		}
		return []string{"/mnt/c/Users/me/AppData/Local/Microsoft/WindowsApps/wt.exe"}, nil
	}
	if got := FindWT(e, none); got != "/mnt/c/Users/me/AppData/Local/Microsoft/WindowsApps/wt.exe" {
		t.Fatalf("wsl glob: %q", got)
	}
	onPath := func(n string) (string, error) { return "/usr/bin/" + n, nil }
	if got := FindWT(e, onPath); got != "/usr/bin/wt.exe" {
		t.Fatalf("PATH first: %q", got)
	}
	w := envOf("windows", map[string]string{"LOCALAPPDATA": `C:\Users\me\AppData\Local`}, "")
	var statted string
	w.Stat = func(p string) (os.FileInfo, error) { statted = p; return nil, nil }
	if got := FindWT(w, none); got == "" || got != statted {
		t.Fatalf("windows LOCALAPPDATA: %q (stat %q)", got, statted)
	}
	if FindWT(envOf("linux", nil, ""), none) != "" {
		t.Fatal("plain linux has no wt")
	}
}

func TestValidTerminalAndBackend(t *testing.T) {
	for _, s := range []string{"", "wt", "iterm", "terminal-app", "kitty", "wezterm", "gnome-terminal"} {
		if !ValidTerminal(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"xterm", "WT", "sh -c x"} {
		if ValidTerminal(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
	for _, s := range []string{"", "auto", "here", "tab", "tmux", "zellij"} {
		if !ValidBackend(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"screen", "Tab", "/bin/sh", "tmux;rm"} {
		if ValidBackend(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestChoose(t *testing.T) {
	cases := []struct {
		setting string
		styled  bool
		want    string
	}{
		{"", false, "here"},
		{"", true, "tab"},
		{"auto", false, "here"},
		{"auto", true, "tab"},
		{"here", true, "here"},
		{"tab", false, "tab"},
		{"tmux", true, "tmux"},
		{"zellij", false, "zellij"},
		{"bogus", true, "tab"},
		{"bogus", false, "here"},
	}
	for _, c := range cases {
		if got := Choose(c.setting, c.styled); got != c.want {
			t.Errorf("Choose(%q,%v)=%s want %s", c.setting, c.styled, got, c.want)
		}
	}
}
