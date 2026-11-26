package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/v4/internal/buildinfo"
)

func TestVersionAndDoctorUpdateHints(t *testing.T) {
	for _, tc := range []struct {
		name string
		host func(home string) buildinfo.Host
		cmd  string // the update command both version and doctor print
		want []string
		not  []string
	}{
		{"homebrew", func(string) buildinfo.Host {
			return buildinfo.Host{Exe: "/opt/homebrew/Cellar/anycli-launcher/4.0.0/bin/ac", GOOS: "darwin"}
		}, "brew upgrade anycli-launcher", []string{"installed via: homebrew"}, []string{"scoop", "go install"}},
		{"scoop", func(string) buildinfo.Host {
			return buildinfo.Host{Exe: `C:\Users\E\scoop\apps\anycli-launcher\current\ac.exe`, GOOS: "windows"}
		}, "scoop update anycli-launcher", []string{"installed via: scoop"}, []string{"brew"}},
		{"go install", func(home string) buildinfo.Host {
			return buildinfo.Host{Exe: filepath.Join(home, "go", "bin", "ac"), GOOS: "linux", Home: home}
		}, "go install github.com/ericfaris/anycli-launcher/v4/cmd/ac@latest", []string{"installed via: go install"}, []string{"brew"}},
		{"unknown", func(string) buildinfo.Host {
			return buildinfo.Host{Exe: "/usr/local/bin/ac", GOOS: "linux", Home: "/nowhere"}
		}, "update with whatever installed it", []string{"installed via: unknown", "brew upgrade", "install.sh", "go install", "releases/latest"}, []string{"scoop"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			host := tc.host(h.home)
			host.Getenv = func(string) string { return "" }
			h.env.Install = host

			h.run("doctor")
			out := h.out.String()
			i := strings.Index(out, "-- Version --")
			if i < 0 || !strings.Contains(out[i:], "version: "+Version) {
				t.Fatalf("doctor has no version section:\n%s", out)
			}
			ver := out[i:]
			for _, s := range append([]string{tc.cmd}, tc.want...) {
				if !strings.Contains(ver, s) {
					t.Errorf("doctor missing %q:\n%s", s, ver)
				}
			}
			for _, s := range tc.not {
				if strings.Contains(ver, s) {
					t.Errorf("doctor version section mentions %q:\n%s", s, ver)
				}
			}

			for _, arg := range []string{"version", "--version", "-V"} {
				h.run(arg)
				first, rest, _ := strings.Cut(h.out.String(), "\n")
				if !strings.HasPrefix(first, "anycli-launcher "+Version) {
					t.Errorf("%s first line %q", arg, first)
				}
				if !strings.Contains(rest, tc.cmd) {
					t.Errorf("%s has no update hint %q:\n%s", arg, tc.cmd, rest)
				}
			}
		})
	}
}
