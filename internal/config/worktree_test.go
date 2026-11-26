package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeRoot(t *testing.T) {
	t.Setenv("AC_PROJECTS_DIR", "")
	c := Default()
	c.ProjectsDir = filepath.Join("base", "projects")
	if got, want := c.WorktreeRoot(), filepath.Join("base", "projects", ".ac-worktrees"); got != want {
		t.Fatalf("default %q want %q", got, want)
	}
	env := t.TempDir()
	t.Setenv("AC_PROJECTS_DIR", env)
	if got := c.WorktreeRoot(); got != filepath.Join(env, WorktreeDirName) {
		t.Fatalf("AC_PROJECTS_DIR: %q", got)
	}
	c.WorktreeDir = filepath.Join("x", "..", "trees") + string(filepath.Separator)
	if got := c.WorktreeRoot(); got != "trees" {
		t.Fatalf("custom: %q", got)
	}
	home, _ := os.UserHomeDir()
	c.WorktreeDir = "~/wt"
	if got := c.WorktreeRoot(); got != filepath.Join(home, "wt") {
		t.Fatalf("tilde: %q", got)
	}
	// Round trip through Save/Load, with no unknown-key warning.
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, warns, err := Load(path)
	if err != nil || len(warns) != 0 || got.WorktreeDir != "~/wt" {
		t.Fatalf("%+v %v %v", got, warns, err)
	}
	// Unset stays out of the file (phase 1-3 configs are byte-stable).
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "worktree_dir") {
		t.Fatalf("unset worktree_dir written:\n%s", data)
	}
}
