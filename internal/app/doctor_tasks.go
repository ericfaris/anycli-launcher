package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericfaris/anycli-launcher/v4/internal/config"
	"github.com/ericfaris/anycli-launcher/v4/internal/worktree"
)

// doctorTasks reports the worktree dir, git's worktree support and the
// task registry. Only an invalid worktree_dir fails doctor; the rest only
// matters to --task, so it warns.
func (a *app) doctorTasks(r *report) {
	root := a.cfg.WorktreeRoot()
	if err := worktree.CheckRoot(root, a.cfg.Roots()); err != nil {
		r.fail(err.Error(), "set worktree_dir to a folder outside the project roots, or leave it unset")
	} else {
		src := "default"
		if a.cfg.WorktreeDir != "" {
			src = "worktree_dir"
		}
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			r.pass(fmt.Sprintf("worktree dir: %s (%s)", root, src))
		} else {
			r.pass(fmt.Sprintf("worktree dir: %s (%s; created by the first --task)", root, src))
		}
		for _, pr := range a.cfg.Roots() {
			if worktree.Under(root, pr) && !strings.HasPrefix(filepath.Base(root), ".") {
				r.info("inside the project root " + pr + "; discovery skips it")
			}
		}
	}

	if _, err := a.env.LookPath("git"); err != nil {
		r.warn("git not on PATH", "--task needs git")
	} else if out, err := a.env.Run(a.env.Home, "git", "version"); err != nil {
		r.warn("git version unknown: "+err.Error(), "--task needs git 2.17+")
	} else if ma, mi, ok := worktree.Version(out); !ok {
		r.warn(fmt.Sprintf("can't parse %q", out), "--task needs git 2.17+")
	} else if !worktree.VersionOK(ma, mi) {
		r.warn(fmt.Sprintf("git %d.%d is too old for task worktrees", ma, mi), fmt.Sprintf("--task needs git %d.%d+ (git worktree remove)", worktree.MinGit[0], worktree.MinGit[1]))
	} else {
		r.pass(fmt.Sprintf("git %d.%d: worktree support ok", ma, mi))
	}

	path := a.env.TasksPath
	reg, err := worktree.Load(path)
	switch {
	case errors.Is(err, worktree.ErrCorrupt):
		r.warn("task registry is corrupt ("+path+")", "ignored; the next task or 'ac prune' sets it aside and starts fresh")
		return
	case err != nil:
		r.warn("task registry unreadable: "+err.Error(), "")
		return
	case !config.Exists(path):
		r.pass("no task sessions recorded yet (" + path + ")")
	default:
		r.pass(fmt.Sprintf("%d task %s in %s", len(reg.Tasks), plural(len(reg.Tasks), "session", "sessions"), path))
	}
	if err := dirWritable(filepath.Dir(path)); err != nil {
		r.warn("task registry not writable: "+err.Error(), "--task can't record sessions")
	}
	gone := 0
	for _, t := range reg.Tasks {
		if _, err := os.Lstat(t.Worktree); errors.Is(err, os.ErrNotExist) {
			gone++
		}
	}
	if gone > 0 {
		r.warn(fmt.Sprintf("%d %s whose worktree is gone", gone, plural(gone, "task", "tasks")), "run 'ac prune'")
	}
	if _, err := os.Stat(path + ".lock"); err == nil {
		r.warn("registry lock file present ("+path+".lock)", "another ac is writing, or one crashed; it expires after 10s")
	}
}
