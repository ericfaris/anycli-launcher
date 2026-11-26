package main

// End-to-end shim test: eval the real `ac init <shell>` output in each
// installed shell and check that `ac <name>` launches the CLI in the project
// and leaves the parent shell cd'd there.
//
// The test binary plays both parts: copied to bin/ac it runs main(), copied
// to bin/fakecli it records its cwd and argv. Set AC_REQUIRE_SHELLS=bash,zsh
// (as CI does) to fail instead of skip when a shell is missing.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/internal/config"
)

func TestMain(m *testing.M) {
	switch strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") {
	case "ac":
		main()
		return
	case "fakecli":
		wd, _ := os.Getwd()
		if log := os.Getenv("FAKECLI_LOG"); log != "" {
			os.WriteFile(log, []byte(fmt.Sprintf("cwd=%s\nargs=%s\n", wd, strings.Join(os.Args[1:], "|"))), 0o644)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func copyExe(t *testing.T, dst string) {
	t.Helper()
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, src); err != nil {
		t.Fatal(err)
	}
	out.Close()
}

type shellCase struct {
	argv   []string
	script string
}

var shellCases = map[string]shellCase{
	"bash": {[]string{"bash", "--norc", "--noprofile", "-c"}, posixScript("bash")},
	"zsh":  {[]string{"zsh", "-f", "-c"}, posixScript("zsh")},
	"fish": {[]string{"fish", "--no-config", "-c"}, `ac init fish | source
ac alpha; echo "PWD=$PWD"
ac zzz; echo "RC=$status"; echo "PWD2=$PWD"
ac new beta </dev/null >/dev/null; echo "PWD3=$PWD"`},
	"pwsh": {[]string{"pwsh", "-NoProfile", "-NonInteractive", "-Command"}, `Invoke-Expression (& ac init pwsh | Out-String)
ac alpha; "PWD=$((Get-Location).Path)"
ac zzz 2>$null; "RC=$LASTEXITCODE"; "PWD2=$((Get-Location).Path)"
$null | ac new beta | Out-Null; "PWD3=$((Get-Location).Path)"`},
}

func posixScript(sh string) string {
	return `eval "$(ac init ` + sh + `)"
ac alpha; echo "PWD=$PWD"
ac zzz; echo "RC=$?"; echo "PWD2=$PWD"
ac new beta </dev/null >/dev/null; echo "PWD3=$PWD"`
}

func TestShimE2E(t *testing.T) {
	required := map[string]bool{}
	for _, s := range strings.Split(os.Getenv("AC_REQUIRE_SHELLS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			required[s] = true
		}
	}
	for name, sc := range shellCases {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(sc.argv[0]); err != nil {
				if required[name] {
					t.Fatalf("%s required but not installed", name)
				}
				t.Skipf("%s not installed", name)
			}
			if runtime.GOOS == "windows" && name != "pwsh" && !required[name] {
				t.Skip("POSIX shells on Windows (Git Bash) are covered by pwsh here")
			}
			runShim(t, sc)
		})
	}
}

func runShim(t *testing.T, sc shellCase) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "projects")
	alpha := filepath.Join(root, "alpha")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(alpha, 0o755)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	copyExe(t, filepath.Join(bin, "ac"+exe))
	copyExe(t, filepath.Join(bin, "fakecli"+exe))

	cfgPath := filepath.Join(tmp, "config.toml")
	cfg := config.Default()
	cfg.ProjectsDir = root
	cfg.CLI = "fakecli"
	cfg.DefaultFlags = "--x"
	cfg.Projects = map[string]config.Project{"alpha": {Prompt: "hi there"}}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(tmp, "fakecli.log")

	cmd := exec.Command(sc.argv[0], append(sc.argv[1:], sc.script)...)
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AC_CONFIG="+cfgPath, "AC_PROJECTS_DIR=", "HOME="+home, "FAKECLI_LOG="+log, "TMPDIR="+tmp,
	)
	outB, err := cmd.CombinedOutput()
	out := string(outB)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	vals := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			vals[k] = v
		}
	}
	same := func(a, b string) bool {
		ra, _ := filepath.EvalSymlinks(a)
		rb, _ := filepath.EvalSymlinks(b)
		return ra != "" && strings.EqualFold(ra, rb)
	}
	if !same(vals["PWD"], alpha) {
		t.Errorf("shell did not cd into the project: PWD=%q want %q\n%s", vals["PWD"], alpha, out)
	}
	if vals["RC"] != "1" || !same(vals["PWD2"], alpha) {
		t.Errorf("failed lookup: RC=%q PWD2=%q\n%s", vals["RC"], vals["PWD2"], out)
	}
	if !same(vals["PWD3"], filepath.Join(root, "beta")) {
		t.Errorf("ac new should cd into the new project: %q\n%s", vals["PWD3"], out)
	}
	logB, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("fake CLI never ran\n%s", out)
	}
	if !strings.Contains(string(logB), "args=--x|hi there") {
		t.Errorf("CLI argv: %s", logB)
	}
	if !strings.Contains(string(logB), "cwd=") || !same(strings.TrimPrefix(strings.Split(string(logB), "\n")[0], "cwd="), alpha) {
		t.Errorf("CLI cwd: %s", logB)
	}
	// The shim cleans up its temp files.
	left, _ := filepath.Glob(filepath.Join(tmp, "ac-cd.*"))
	if len(left) > 0 {
		t.Errorf("cd temp files left behind: %v", left)
	}
}
