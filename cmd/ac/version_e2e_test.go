package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestReleaseLdflags builds ac the way .goreleaser.yaml does and checks that
// the -X symbols land. A typo in the symbol path makes -X a silent no-op, so
// only a real build catches it.
func TestReleaseLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	exe := filepath.Join(t.TempDir(), "ac-release")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	const pkg = "github.com/ericfaris/anycli-launcher/v4/internal/buildinfo"
	ld := "-s -w -X " + pkg + ".version=9.8.7 -X " + pkg + ".commit=feedfacecafe -X " + pkg + ".date=2030-01-02T03:04:05Z"
	build := exec.Command(goBin, "build", "-trimpath", "-ldflags", ld, "-o", exe, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Pin the update hint to the go-install location so the output is stable.
	home := t.TempDir()
	cmd := exec.Command(exe, "--version")
	cmd.Env = append(os.Environ(), "AC_CONFIG="+filepath.Join(home, "none.toml"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	first, rest, _ := strings.Cut(string(out), "\n")
	if want := "anycli-launcher 9.8.7 (feedfac, 2030-01-02)"; strings.TrimSpace(first) != want {
		t.Fatalf("first line %q, want %q", first, want)
	}
	if !strings.Contains(rest, "update") {
		t.Fatalf("no update hint:\n%s", out)
	}
}
