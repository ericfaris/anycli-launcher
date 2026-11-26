package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// powershell finds pwsh (or Windows PowerShell 5.1) on Windows. install.ps1
// refuses to run elsewhere, so the test is Windows-only (CI windows-latest).
func powershell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 is Windows-only")
	}
	for _, n := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skip("no PowerShell")
	return ""
}

func runPs1(t *testing.T, ps string, rel *release, extraEnv []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(ps, append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", repoFile(t, "install.ps1")}, args...)...)
	// Drop inherited values instead of setting them empty: Windows env
	// blocks don't carry empty variables reliably.
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(strings.ToUpper(kv), "=")
		if !strings.HasPrefix(k, "AC_") && !strings.HasPrefix(k, "PROCESSOR_ARCHITE") {
			env = append(env, kv)
		}
	}
	arch := "AMD64" // pinned, so arm64 runners take the same path
	for _, kv := range extraEnv {
		if v, ok := strings.CutPrefix(kv, "PROCESSOR_ARCHITECTURE="); ok {
			arch = v
		} else {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "AC_RELEASES_URL="+rel.URL, "PROCESSOR_ARCHITECTURE="+arch, "PROCESSOR_ARCHITEW6432="+arch)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

const ps1Line = "Invoke-Expression (& (Get-Command ac -CommandType Application | Select-Object -First 1).Source init pwsh | Out-String)"

func TestInstallPs1HappyPath(t *testing.T) {
	ps := powershell(t)
	rel := newRelease(t, nil)
	dir := filepath.Join(t.TempDir(), "my bin")
	out, code := runPs1(t, ps, rel, nil, "-Dir", dir)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "ac.exe"))
	if err != nil || string(b) != fakeBinary {
		t.Fatalf("installed: %q %v", b, err)
	}
	for _, want := range []string{"Checksum OK", ps1Line, "Add " + dir + " to your PATH"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if got := rel.requested(); len(got) != 2 || got[0] != "/latest/download/anycli-launcher_windows_amd64.zip" {
		t.Errorf("requests %v", got)
	}

	// A pinned version uses the tagged download path; ARM64 picks its zip.
	out, code = runPs1(t, ps, rel, []string{"PROCESSOR_ARCHITECTURE=ARM64"}, "-Dir", dir, "-Version", "9.9.9")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := rel.requested(); got[len(got)-2] != "/download/v9.9.9/anycli-launcher_windows_arm64.zip" {
		t.Errorf("requests %v", got)
	}
}

func TestInstallPs1FailsClosed(t *testing.T) {
	ps := powershell(t)
	const asset = "anycli-launcher_windows_amd64.zip"
	for _, tc := range []struct {
		name   string
		mutate func(map[string][]byte, map[string]string)
		msg    string
	}{
		{"tampered", func(f map[string][]byte, _ map[string]string) { f[asset] = append(append([]byte(nil), f[asset]...), 0) }, "checksum mismatch"},
		{"not listed", func(_ map[string][]byte, s map[string]string) { delete(s, asset) }, "not listed in checksums.txt"},
		{"no checksums", func(f map[string][]byte, _ map[string]string) { f["checksums.txt"] = nil }, "download failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			out, code := runPs1(t, ps, newRelease(t, tc.mutate), nil, "-Dir", dir)
			if code == 0 || !strings.Contains(out, tc.msg) {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if _, err := os.Stat(filepath.Join(dir, "ac.exe")); err == nil {
				t.Fatal("installed")
			}
		})
	}
	t.Run("unsupported arch", func(t *testing.T) {
		rel := newRelease(t, nil)
		out, code := runPs1(t, ps, rel, []string{"PROCESSOR_ARCHITECTURE=x86"}, "-Dir", t.TempDir())
		if code == 0 || !strings.Contains(out, "unsupported architecture") || len(rel.requested()) != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
}

func TestInstallPs1Setup(t *testing.T) {
	ps := powershell(t)
	rel := newRelease(t, nil)
	dir := t.TempDir()
	prof := filepath.Join(t.TempDir(), "sub", "profile.ps1")
	// dir already on PATH: -Setup must not touch the user PATH (registry).
	env := []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	for i := 0; i < 2; i++ {
		out, code := runPs1(t, ps, rel, env, "-Dir", dir, "-Setup", "-ProfilePath", prof)
		if code != 0 || strings.Contains(out, "user PATH") {
			t.Fatalf("run %d: exit %d:\n%s", i, code, out)
		}
	}
	b, err := os.ReadFile(prof)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), ps1Line); n != 1 {
		t.Fatalf("line written %d times:\n%s", n, b)
	}
}
