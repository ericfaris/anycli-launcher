package buildinfo

import (
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	stamp := &debug.BuildInfo{
		Main: debug.Module{Version: "v4.1.2"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-11-20T10:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	devel := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	cases := []struct {
		name       string
		v, c, d    string
		bi         *debug.BuildInfo
		ok         bool
		want       Info
		wantString string
	}{
		{"ldflags win", "4.0.0", "abcdef1234", "2026-11-26T00:00:00Z", stamp, true,
			Info{Version: "4.0.0", Commit: "abcdef1234", Date: "2026-11-26T00:00:00Z"}, "4.0.0 (abcdef1, 2026-11-26)"},
		{"ldflags v prefix stripped", "v4.0.0", "", "", nil, false,
			Info{Version: "4.0.0"}, "4.0.0"},
		{"go install", "", "", "", stamp, true,
			Info{Version: "4.1.2", Commit: "0123456789abcdef", Date: "2026-11-20T10:00:00Z", Dirty: true}, "4.1.2 (0123456-dirty, 2026-11-20)"},
		{"devel build", "", "", "", devel, true,
			Info{Version: DevVersion}, DevVersion},
		{"no build info", "", "", "", nil, false,
			Info{Version: DevVersion}, DevVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolve(tc.v, tc.c, tc.d, tc.bi, tc.ok)
			if got != tc.want {
				t.Fatalf("resolve = %+v, want %+v", got, tc.want)
			}
			if s := got.String(); s != tc.wantString {
				t.Fatalf("String = %q, want %q", s, tc.wantString)
			}
		})
	}
}

func TestGetNeverEmpty(t *testing.T) {
	if v := Get().Version; v == "" || strings.HasPrefix(v, "v") {
		t.Fatalf("Get().Version = %q", v)
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		h    Host
		want Method
	}{
		{"brew formula macOS", Host{Exe: "/opt/homebrew/Cellar/anycli-launcher/4.0.0/bin/ac", GOOS: "darwin", Home: "/Users/e"}, Homebrew},
		{"brew cask", Host{Exe: "/opt/homebrew/Caskroom/anycli-launcher/4.0.0/ac", GOOS: "darwin", Home: "/Users/e"}, Homebrew},
		{"linuxbrew", Host{Exe: "/home/linuxbrew/.linuxbrew/Cellar/anycli-launcher/4.0.0/bin/ac", GOOS: "linux", Home: "/home/e"}, Homebrew},
		{"other brew formula is not us", Host{Exe: "/opt/homebrew/Cellar/acct/1/bin/ac", GOOS: "darwin", Home: "/Users/e"}, Unknown},
		{"scoop default root", Host{Exe: `C:\Users\E\scoop\apps\anycli-launcher\current\ac.exe`, GOOS: "windows", Home: `C:\Users\E`}, Scoop},
		{"scoop custom root", Host{Exe: `D:\tools\apps\anycli-launcher\4.0.0\ac.exe`, GOOS: "windows", Home: `C:\Users\E`,
			Getenv: env(map[string]string{"SCOOP": `D:\Tools`})}, Scoop},
		{"go default GOPATH", Host{Exe: filepath.Join("/home/e", "go", "bin", "ac"), GOOS: "linux", Home: "/home/e"}, GoInstall},
		{"go GOBIN", Host{Exe: "/opt/gobin/ac", GOOS: "linux", Home: "/home/e", Getenv: env(map[string]string{"GOBIN": "/opt/gobin"})}, GoInstall},
		{"go GOPATH list", Host{Exe: "/w/one/bin/ac", GOOS: "linux", Home: "/home/e", Getenv: env(map[string]string{"GOPATH": "/w/one:/w/two"})}, GoInstall},
		{"go windows", Host{Exe: `C:\Users\E\go\bin\ac.exe`, GOOS: "windows", Home: `C:\Users\E`}, GoInstall},
		{"script default", Host{Exe: "/home/e/.local/bin/ac", GOOS: "linux", Home: "/home/e"}, Script},
		{"script custom dir", Host{Exe: "/srv/bin/ac", GOOS: "linux", Home: "/home/e", Getenv: env(map[string]string{"AC_INSTALL_DIR": "/srv/bin/"})}, Script},
		{"ps1 default", Host{Exe: `C:\Users\E\AppData\Local\Programs\anycli-launcher\ac.exe`, GOOS: "windows", Home: `C:\Users\E`,
			Getenv: env(map[string]string{"LOCALAPPDATA": `C:\Users\E\AppData\Local`})}, Script},
		{"windows case-insensitive", Host{Exe: `c:\users\e\GO\BIN\AC.EXE`, GOOS: "windows", Home: `C:\Users\E`}, GoInstall},
		{"usr local bin", Host{Exe: "/usr/local/bin/ac", GOOS: "linux", Home: "/home/e"}, Unknown},
		{"subdir of script dir is not it", Host{Exe: "/home/e/.local/bin/x/ac", GOOS: "linux", Home: "/home/e"}, Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.h.Getenv == nil {
				tc.h.Getenv = env(nil)
			}
			if got := Detect(tc.h); got != tc.want {
				t.Fatalf("Detect = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectRealProcess(t *testing.T) {
	// The test binary lives in a temp dir: whatever it says, it must not panic
	// and must return a known method.
	switch Detect(Host{}) {
	case Homebrew, Scoop, GoInstall, Script, Unknown:
	default:
		t.Fatal("unexpected method")
	}
}

func TestUpdateHint(t *testing.T) {
	for _, tc := range []struct {
		m    Method
		goos string
		want string
	}{
		{Homebrew, "darwin", "brew upgrade anycli-launcher"},
		{Scoop, "windows", "scoop update anycli-launcher"},
		{GoInstall, "linux", "go install github.com/ericfaris/anycli-launcher/v4/cmd/ac@latest"},
		{Script, "linux", "curl -fsSL https://github.com/ericfaris/anycli-launcher/releases/latest/download/install.sh | sh"},
		{Script, "windows", "irm https://github.com/ericfaris/anycli-launcher/releases/latest/download/install.ps1 | iex"},
	} {
		got := UpdateHint(tc.m, tc.goos)
		if len(got) != 1 || !strings.HasSuffix(got[0], tc.want) || !strings.Contains(got[0], string(tc.m)) {
			t.Errorf("%s/%s: %q", tc.m, tc.goos, got)
		}
	}
	unix := strings.Join(UpdateHint(Unknown, "linux"), "\n")
	for _, s := range []string{"brew upgrade", "install.sh", "go install", "releases/latest"} {
		if !strings.Contains(unix, s) {
			t.Errorf("unknown/linux missing %q:\n%s", s, unix)
		}
	}
	if strings.Contains(unix, "scoop") {
		t.Errorf("scoop offered on linux:\n%s", unix)
	}
	win := strings.Join(UpdateHint(Unknown, "windows"), "\n")
	for _, s := range []string{"scoop update", "install.ps1", "go install"} {
		if !strings.Contains(win, s) {
			t.Errorf("unknown/windows missing %q:\n%s", s, win)
		}
	}
	if UpdateCommand(Unknown, "linux") != "" {
		t.Error("unknown has a command")
	}
}
