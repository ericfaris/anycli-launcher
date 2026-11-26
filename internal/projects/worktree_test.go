package projects

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ericfaris/anycli-launcher/v4/internal/config"
)

// Task worktrees never show up as projects: the default .ac-worktrees is
// hidden, and a visible worktree_dir inside a root (or a group) is skipped
// too. A worktree_dir that is a root itself hides nothing.
func TestDiscoverSkipsWorktreeDir(t *testing.T) {
	t.Setenv("AC_PROJECTS_DIR", "")
	root := t.TempDir()
	mkdirs(t, root, "alpha", filepath.Join(config.WorktreeDirName, "alpha", "fix-bug"),
		filepath.Join("wt", "alpha", "x"), filepath.Join("work", "api"), filepath.Join("work", "trees", "a"))
	cfg := config.Default()
	cfg.ProjectsDir = root
	if got := labels(Discover(cfg)); !reflect.DeepEqual(got, []string{"alpha", "work", "wt"}) {
		t.Fatalf("default: %v", got)
	}
	cfg.WorktreeDir = filepath.Join(root, "wt")
	if got := labels(Discover(cfg)); !reflect.DeepEqual(got, []string{"alpha", "work"}) {
		t.Fatalf("visible worktree_dir: %v", got)
	}
	cfg.ScanDepth = 2
	cfg.WorktreeDir = filepath.Join(root, "work", "trees")
	if got := labels(Discover(cfg)); !reflect.DeepEqual(got, []string{"alpha", "work/api", "wt/alpha"}) {
		t.Fatalf("depth 2: %v", got)
	}
	cfg.ScanDepth = 1
	for _, bad := range []string{root, filepath.Dir(root)} {
		cfg.WorktreeDir = bad
		if got := labels(Discover(cfg)); !reflect.DeepEqual(got, []string{"alpha", "work", "wt"}) {
			t.Fatalf("worktree_dir %s (a root or above) must hide nothing: %v", bad, got)
		}
	}
}

func TestInWorktreeRoot(t *testing.T) {
	wt := filepath.Join("a", "wt")
	for dir, want := range map[string]bool{
		wt:                             true,
		filepath.Join(wt, "x"):         true,
		filepath.Join("a", "wtx"):      false,
		filepath.Join("a"):             false,
		filepath.Join("a", "wt", ".."): false,
		filepath.Join("b", "wt"):       false,
	} {
		if got := inWorktreeRoot(dir, wt); got != want {
			t.Errorf("%s: got %v", dir, got)
		}
	}
	if inWorktreeRoot(wt, "") {
		t.Fatal("empty root matches nothing")
	}
}
