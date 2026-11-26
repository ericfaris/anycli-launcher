package main

// End-to-end tab launch without a GUI: `ac <name>` with backend = "tab"
// and terminal = "kitty" runs a fake `kitty` (this test binary), which
// records the argv it got and then does what a terminal would: runs the
// command after the options (`ac __tab-exec <spec>`) in the requested cwd.
// The trampoline then goes through a real `bash -l` (Unix) and execs the
// fake CLI, which records its cwd and argv. So everything but the window
// itself is real: spec file, login re-exec, cd, exec, cleanup.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/v4/internal/config"
)

// fakeTerminal plays `kitty @ launch --type=tab --cwd D [--tab-title T] cmd...`.
func fakeTerminal() {
	args := os.Args[1:]
	if log := os.Getenv("FAKETERM_LOG"); log != "" {
		b, _ := json.Marshal(args)
		os.WriteFile(log, b, 0o644)
	}
	cwd := ""
	i := 0
	for i < len(args) {
		switch a := args[i]; {
		case a == "@" || a == "launch" || strings.HasPrefix(a, "--type="):
			i++
		case a == "--cwd" || a == "--tab-title":
			if a == "--cwd" {
				cwd = args[i+1]
			}
			i += 2
		default:
			goto run
		}
	}
run:
	cmd := exec.Command(args[i], args[i+1:]...)
	cmd.Dir = cwd
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestTabBackendE2E(t *testing.T) {
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("bash not installed (needed for the login re-exec)")
		}
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "projects")
	// A project directory full of shell metacharacters.
	proj := filepath.Join(root, `my "app"; $(touch pwned) it's`)
	if runtime.GOOS == "windows" {
		proj = filepath.Join(root, "my app; it's")
	}
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(proj, 0o755)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	copyExe(t, filepath.Join(bin, "ac"+exe))
	copyExe(t, filepath.Join(bin, "fakecli"+exe))
	copyExe(t, filepath.Join(bin, "kitty"+exe))

	prompt := "fix \"this\"; $(touch pwned2) `id`\nsecond line"
	cfgPath := filepath.Join(tmp, "config.toml")
	cfg := config.Default()
	cfg.ProjectsDir = root
	cfg.CLI = "fakecli"
	cfg.Backend = "tab"
	cfg.Terminal = "kitty"
	cfg.Projects = map[string]config.Project{filepath.Base(proj): {Prompt: prompt, Title: "T; $(x)", Color: "#336699"}}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	cliLog := filepath.Join(tmp, "cli.json")
	termLog := filepath.Join(tmp, "term.json")
	cdFile := filepath.Join(tmp, "cd")
	tmpDir := filepath.Join(tmp, "t")
	os.MkdirAll(tmpDir, 0o700)

	cmd := exec.Command(filepath.Join(bin, "ac"+exe), "--cd-file", cdFile, "my")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AC_CONFIG="+cfgPath, "AC_PROJECTS_DIR=", "HOME="+home, "USERPROFILE="+home,
		"FAKECLI_JSON="+cliLog, "FAKETERM_LOG="+termLog,
		"TMPDIR="+tmpDir, "TMP="+tmpDir, "TEMP="+tmpDir,
		"AC_FRECENCY_FILE="+filepath.Join(tmp, "frecency.json"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var term []string
	if b, err := os.ReadFile(termLog); err != nil || json.Unmarshal(b, &term) != nil {
		t.Fatalf("terminal never ran: %v\n%s", err, out)
	}
	if len(term) < 3 || term[len(term)-2] != "__tab-exec" || !reflect.DeepEqual(term[:5], []string{"@", "launch", "--type=tab", "--cwd", proj}) {
		t.Fatalf("terminal argv %q", term)
	}
	var rec struct {
		Cwd     string   `json:"cwd"`
		Args    []string `json:"args"`
		Spawned string   `json:"spawned"`
	}
	b, err := os.ReadFile(cliLog)
	if err != nil || json.Unmarshal(b, &rec) != nil {
		t.Fatalf("CLI never ran: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(rec.Args, []string{prompt}) || rec.Spawned != "1" {
		t.Fatalf("CLI %+v", rec)
	}
	ra, _ := filepath.EvalSymlinks(rec.Cwd)
	rb, _ := filepath.EvalSymlinks(proj)
	if !strings.EqualFold(ra, rb) {
		t.Fatalf("CLI cwd %q want %q", rec.Cwd, proj)
	}
	if _, err := os.Stat(cdFile); err == nil {
		t.Fatal("a tab launch must not cd the calling shell")
	}
	if left, _ := filepath.Glob(filepath.Join(tmpDir, "ac-tab-*")); len(left) != 0 {
		t.Fatalf("spec files left: %v", left)
	}
	for _, p := range []string{"pwned", "pwned2"} {
		for _, d := range []string{proj, tmp, home} {
			if _, err := os.Stat(filepath.Join(d, p)); err == nil {
				t.Fatalf("injection: %s in %s", p, d)
			}
		}
	}
}
