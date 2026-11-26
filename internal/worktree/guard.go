package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrGone means the worktree directory no longer exists.
var ErrGone = errors.New("worktree directory is gone")

// CheckPath reports why path must not be removed as a task worktree, or
// nil when it is a real directory strictly inside root. Both the path as
// written and its symlink-resolved form must be inside root, and path
// itself must not be a symlink: a registry entry (or a planted link) can't
// steer finish at anything outside worktree_dir. Callers then also require
// git to list it as one of the repository's worktrees; deletion itself is
// always `git worktree remove`, never a recursive delete.
func CheckPath(path, root string) error {
	if path == "" || root == "" {
		return errors.New("empty path")
	}
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return fmt.Errorf("%s: worktree paths must be absolute", path)
	}
	clean, cleanRoot := filepath.Clean(path), filepath.Clean(root)
	if !Under(clean, cleanRoot) {
		return fmt.Errorf("%s is not inside the worktree dir %s", path, root)
	}
	st, err := os.Lstat(clean)
	if errors.Is(err, os.ErrNotExist) {
		return ErrGone
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, not a worktree", path)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return fmt.Errorf("worktree dir %s: %w", root, err)
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return err
	}
	if !Under(real, realRoot) {
		return fmt.Errorf("%s resolves to %s, outside the worktree dir %s", path, real, realRoot)
	}
	return nil
}

// Under reports whether child is strictly inside parent (lexically).
func Under(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// CheckRoot reports why root can't hold task worktrees: it must not be, or
// contain, a project root (discovery would hide projects, and finish could
// reach them).
func CheckRoot(root string, projectRoots []string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("worktree_dir %q must be an absolute path", root)
	}
	for _, r := range projectRoots {
		r = filepath.Clean(r)
		if filepath.Clean(root) == r || Under(r, root) {
			return fmt.Errorf("worktree_dir %s must not be or contain the project root %s", root, r)
		}
	}
	return nil
}
