package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shRun is one install.sh invocation in a sandbox: a temp HOME, a fake
// uname (so every OS/arch branch runs on any host) and the fake release.
type shRun struct {
	t       *testing.T
	home    string
	bin     string // fake tools dir, first on PATH
	rel     *release
	env     []string
	sysPATH string
}

func newShRun(t *testing.T, rel *release) *shRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for Unix; install.ps1 has its own test")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		if _, err := exec.LookPath("wget"); err != nil {
			t.Skip("no curl or wget")
		}
	}
	r := &shRun{t: t, home: t.TempDir(), bin: t.TempDir(), rel: rel, sysPATH: os.Getenv("PATH")}
	r.uname("Linux", "x86_64")
	return r
}

// uname installs a fake uname that answers -s and -m.
func (r *shRun) uname(s, m string) {
	script := "#!/bin/sh\ncase \"$1\" in -s) echo '" + s + "' ;; -m) echo '" + m + "' ;; *) echo '" + s + "' ;; esac\n"
	if err := os.WriteFile(filepath.Join(r.bin, "uname"), []byte(script), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

func (r *shRun) run(path string, args ...string) (string, int) {
	r.t.Helper()
	cmd := exec.Command("sh", append([]string{repoFile(r.t, "install.sh")}, args...)...)
	if path == "" {
		path = r.bin + string(os.PathListSeparator) + r.sysPATH
	}
	cmd.Env = append([]string{
		"HOME=" + r.home,
		"PATH=" + path,
		"SHELL=/bin/zsh",
		"AC_RELEASES_URL=" + r.rel.URL,
		"TMPDIR=" + r.t.TempDir(),
	}, r.env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	return string(out), code
}

func (r *shRun) installed() string { return filepath.Join(r.home, ".local", "bin", "ac") }

func TestInstallShHappyPath(t *testing.T) {
	r := newShRun(t, newRelease(t, nil))
	out, code := r.run("")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	b, err := os.ReadFile(r.installed())
	if err != nil || string(b) != fakeBinary {
		t.Fatalf("installed binary: %q %v", b, err)
	}
	if st, _ := os.Stat(r.installed()); st.Mode()&0o111 == 0 {
		t.Fatalf("not executable: %v", st.Mode())
	}
	for _, want := range []string{
		"Checksum OK",
		`eval "$(ac init zsh)"`,
		filepath.Join(r.home, ".zshrc"),
		// ~/.local/bin is not on the sandbox PATH: say how to add it.
		`export PATH="` + filepath.Join(r.home, ".local", "bin") + `:$PATH"`,
		"anycli-launcher 9.9.9 (fixture)", // it ran the installed binary
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Never touches rc files without --setup.
	entries, _ := os.ReadDir(r.home)
	for _, e := range entries {
		if e.Name() != ".local" {
			t.Errorf("unexpected file in HOME: %s", e.Name())
		}
	}
	if got := r.rel.requested(); len(got) != 2 || got[0] != "/latest/download/anycli-launcher_linux_amd64.tar.gz" || got[1] != "/latest/download/checksums.txt" {
		t.Errorf("requests: %v", got)
	}
	// No temp files left next to the binary.
	if left, _ := filepath.Glob(filepath.Join(r.home, ".local", "bin", ".ac.tmp.*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestInstallShPlatforms(t *testing.T) {
	for _, tc := range []struct{ s, m, asset string }{
		{"Linux", "x86_64", "anycli-launcher_linux_amd64.tar.gz"},
		{"Linux", "aarch64", "anycli-launcher_linux_arm64.tar.gz"},
		{"Darwin", "arm64", "anycli-launcher_darwin_arm64.tar.gz"},
	} {
		t.Run(tc.s+"_"+tc.m, func(t *testing.T) {
			r := newShRun(t, newRelease(t, nil))
			r.uname(tc.s, tc.m)
			out, code := r.run("")
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if got := r.rel.requested(); len(got) == 0 || got[0] != "/latest/download/"+tc.asset {
				t.Fatalf("requests %v, want %s", got, tc.asset)
			}
		})
	}
	for _, tc := range []struct{ s, m, msg string }{
		{"Linux", "i686", "unsupported architecture: i686"},
		{"SunOS", "x86_64", "unsupported OS: SunOS"},
	} {
		t.Run("reject_"+tc.s+"_"+tc.m, func(t *testing.T) {
			r := newShRun(t, newRelease(t, nil))
			r.uname(tc.s, tc.m)
			out, code := r.run("")
			if code == 0 || !strings.Contains(out, tc.msg) {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if len(r.rel.requested()) != 0 {
				t.Fatal("downloaded on an unsupported platform")
			}
		})
	}
}

func TestInstallShVersionAndDir(t *testing.T) {
	r := newShRun(t, newRelease(t, nil))
	dir := filepath.Join(t.TempDir(), "my bin")
	r.env = append(r.env, "AC_INSTALL_DIR="+dir)
	out, code := r.run("", "--version", "9.9.9")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ac")); err != nil {
		t.Fatal(err)
	}
	if got := r.rel.requested(); got[0] != "/download/v9.9.9/anycli-launcher_linux_amd64.tar.gz" {
		t.Fatalf("requests %v", got)
	}
	// --dir beats AC_INSTALL_DIR, and a v-prefixed tag is used as is.
	other := t.TempDir()
	if out, code := r.run("", "--dir", other, "--version=v9.9.9"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(other, "ac")); err != nil {
		t.Fatal(err)
	}
}

// failClosed runs the installer against a bad release and checks nothing
// was installed.
func failClosed(t *testing.T, mutate func(map[string][]byte, map[string]string), msg string) {
	t.Helper()
	r := newShRun(t, newRelease(t, mutate))
	out, code := r.run("")
	if code == 0 {
		t.Fatalf("installed despite %s:\n%s", msg, out)
	}
	if !strings.Contains(out, msg) {
		t.Errorf("output missing %q:\n%s", msg, out)
	}
	if _, err := os.Stat(r.installed()); err == nil {
		t.Fatal("binary installed")
	}
}

func TestInstallShFailsClosed(t *testing.T) {
	const asset = "anycli-launcher_linux_amd64.tar.gz"
	t.Run("tampered archive", func(t *testing.T) {
		failClosed(t, func(f map[string][]byte, _ map[string]string) {
			f[asset] = append(append([]byte(nil), f[asset]...), 0)
		}, "checksum mismatch")
	})
	t.Run("not in checksums", func(t *testing.T) {
		failClosed(t, func(_ map[string][]byte, s map[string]string) { delete(s, asset) }, "not listed in checksums.txt")
	})
	t.Run("no checksums file", func(t *testing.T) {
		failClosed(t, func(f map[string][]byte, _ map[string]string) { f["checksums.txt"] = nil }, "download failed")
	})
	t.Run("no archive", func(t *testing.T) {
		failClosed(t, func(f map[string][]byte, _ map[string]string) { f[asset] = nil }, "download failed")
	})
	t.Run("prefix-only checksum line", func(t *testing.T) {
		// A line for another asset whose name ends the same must not match.
		failClosed(t, func(f map[string][]byte, s map[string]string) {
			f["checksums.txt"] = []byte(s[asset] + "  evil-" + asset + "\n")
		}, "not listed in checksums.txt")
	})
}

func TestInstallShNoHashTool(t *testing.T) {
	r := newShRun(t, newRelease(t, nil))
	// A PATH with every tool the script needs except the sha256 ones.
	tools := t.TempDir()
	for _, n := range []string{"sh", "curl", "wget", "tar", "gzip", "mktemp", "rm", "mkdir", "cp", "chmod", "mv", "awk", "grep", "basename", "dirname", "head", "touch", "cat", "sysctl"} {
		p, err := exec.LookPath(n)
		if err != nil {
			continue
		}
		if err := os.Symlink(p, filepath.Join(tools, n)); err != nil {
			t.Fatal(err)
		}
	}
	os.Rename(filepath.Join(r.bin, "uname"), filepath.Join(tools, "uname"))
	out, code := r.run(tools)
	if code == 0 || !strings.Contains(out, "need sha256sum, shasum or openssl") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(r.installed()); err == nil {
		t.Fatal("installed without verifying")
	}
}

func TestInstallShShellLines(t *testing.T) {
	for _, tc := range []struct{ shell, rc, line string }{
		{"bash", ".bashrc", `eval "$(ac init bash)"`},
		{"zsh", ".zshrc", `eval "$(ac init zsh)"`},
		{"fish", ".config/fish/config.fish", "ac init fish | source"},
		{"pwsh", ".config/powershell/Microsoft.PowerShell_profile.ps1",
			"Invoke-Expression (& (Get-Command ac -CommandType Application | Select-Object -First 1).Source init pwsh | Out-String)"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			r := newShRun(t, newRelease(t, nil))
			r.env = append(r.env, "SHELL=/usr/bin/"+tc.shell)
			out, code := r.run("")
			if code != 0 || !strings.Contains(out, "  "+tc.line+"\n") || !strings.Contains(out, filepath.Join(r.home, tc.rc)) {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if _, err := os.Stat(filepath.Join(r.home, tc.rc)); err == nil {
				t.Fatal("rc file written without --setup")
			}

			// --setup writes it once, plus PATH, and is idempotent.
			for i := 0; i < 2; i++ {
				if out, code := r.run("", "--setup"); code != 0 {
					t.Fatalf("setup %d: exit %d:\n%s", i, code, out)
				}
			}
			b, err := os.ReadFile(filepath.Join(r.home, tc.rc))
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(b), tc.line); n != 1 {
				t.Fatalf("line written %d times:\n%s", n, b)
			}
			if !strings.Contains(string(b), filepath.Join(r.home, ".local", "bin")) {
				t.Fatalf("PATH line missing:\n%s", b)
			}
		})
	}
	t.Run("on PATH already", func(t *testing.T) {
		r := newShRun(t, newRelease(t, nil))
		dir := filepath.Join(r.home, ".local", "bin")
		path := r.bin + ":" + dir + ":" + r.sysPATH
		if out, code := r.run(path, "--setup"); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		b, _ := os.ReadFile(filepath.Join(r.home, ".zshrc"))
		if strings.Contains(string(b), "PATH") || !strings.Contains(string(b), `eval "$(ac init zsh)"`) {
			t.Fatalf("rc:\n%s", b)
		}
	})
	t.Run("unknown shell", func(t *testing.T) {
		r := newShRun(t, newRelease(t, nil))
		r.env = append(r.env, "SHELL=/bin/tcsh")
		out, code := r.run("", "--setup")
		if code != 0 || !strings.Contains(out, "ac init bash") || !strings.Contains(out, "pass --shell") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		entries, _ := os.ReadDir(r.home)
		if len(entries) != 1 {
			t.Fatalf("wrote into HOME: %v", entries)
		}
	})
	t.Run("--shell overrides SHELL", func(t *testing.T) {
		r := newShRun(t, newRelease(t, nil))
		out, code := r.run("", "--shell", "fish")
		if code != 0 || !strings.Contains(out, "ac init fish | source") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
}

func TestInstallShOptions(t *testing.T) {
	r := newShRun(t, newRelease(t, nil))
	if out, code := r.run("", "--help"); code != 0 || !strings.Contains(out, "Usage: install.sh") {
		t.Fatalf("help: %d\n%s", code, out)
	}
	if out, code := r.run("", "--bogus"); code == 0 || !strings.Contains(out, "unknown option") {
		t.Fatalf("bogus: %d\n%s", code, out)
	}
	if out, code := r.run("", "--version"); code == 0 || !strings.Contains(out, "--version needs a tag") {
		t.Fatalf("version: %d\n%s", code, out)
	}
	if len(r.rel.requested()) != 0 {
		t.Fatal("downloaded on a usage error")
	}
}
