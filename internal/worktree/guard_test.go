package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
}

func TestCheckPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "wt")
	ok := filepath.Join(root, "api", "fix")
	outside := filepath.Join(base, "important")
	for _, d := range []string{ok, outside} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(root, "api", "file"), nil, 0o644)

	if err := CheckPath(ok, root); err != nil {
		t.Fatalf("valid worktree refused: %v", err)
	}
	refuse := map[string]string{
		"the root itself":    root,
		"outside":            outside,
		"dot-dot escape":     filepath.Join(root, "api", "..", "..", "important"),
		"prefix sibling":     root + "x",
		"relative":           filepath.Join("wt", "api", "fix"),
		"empty":              "",
		"a file":             filepath.Join(root, "api", "file"),
		"filesystem root":    filepath.VolumeName(base) + string(filepath.Separator),
		"parent of the root": base,
	}
	for name, p := range refuse {
		if err := CheckPath(p, root); err == nil {
			t.Errorf("%s (%s) accepted", name, p)
		}
	}
	if err := CheckPath(filepath.Join(root, "api", "gone"), root); !errors.Is(err, ErrGone) {
		t.Fatalf("missing: %v", err)
	}
	if err := CheckPath(ok, ""); err == nil {
		t.Fatal("empty root")
	}
}

// Symlink tricks: a worktree path that is a link, or sits under a linked
// directory that points out of worktree_dir, is refused.
func TestCheckPathSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "wt")
	outside := filepath.Join(base, "important")
	os.MkdirAll(filepath.Join(root, "api"), 0o755)
	os.MkdirAll(filepath.Join(outside, "fix"), 0o755)

	link := filepath.Join(root, "api", "fix")
	symlinkOrSkip(t, outside, link)
	if err := CheckPath(link, root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("link as worktree: %v", err)
	}
	// A linked project folder: wt/evil -> important, so wt/evil/fix
	// resolves outside.
	symlinkOrSkip(t, outside, filepath.Join(root, "evil"))
	if err := CheckPath(filepath.Join(root, "evil", "fix"), root); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("linked parent: %v", err)
	}
	// A worktree_dir that is itself a symlink (e.g. to another disk) is
	// fine as long as the worktree really is inside its target.
	realRoot := filepath.Join(base, "disk")
	os.MkdirAll(filepath.Join(realRoot, "api", "fix"), 0o755)
	linkedRoot := filepath.Join(base, "wtlink")
	symlinkOrSkip(t, realRoot, linkedRoot)
	if err := CheckPath(filepath.Join(linkedRoot, "api", "fix"), linkedRoot); err != nil {
		t.Fatalf("linked root: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("nothing may be touched")
	}
}

func TestCheckRoot(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "projects")
	if err := CheckRoot(filepath.Join(proj, ".ac-worktrees"), []string{proj}); err != nil {
		t.Fatal(err)
	}
	if err := CheckRoot(filepath.Join(base, "elsewhere"), []string{proj}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{proj, base, "relative"} {
		if err := CheckRoot(bad, []string{proj}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
