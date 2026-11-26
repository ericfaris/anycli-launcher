package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/launch"
	"github.com/ericfaris/anycli-launcher/internal/tui"
	"github.com/ericfaris/anycli-launcher/internal/worktree"
)

// taskInfo is one task with its live status and git state.
type taskInfo struct {
	worktree.Task
	Live    worktree.Liveness
	Git     worktree.State
	Missing bool // the worktree directory is gone
}

// clean strips control characters (registry text is shown in a terminal;
// a task description must not be able to emit escape sequences).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return ' '
		}
		return r
	}, s)
}

// loadTasks reads the registry. A corrupt one is reported once and reads
// as empty.
func (a *app) loadTasks() []worktree.Task {
	r, err := worktree.Load(a.env.TasksPath)
	if err != nil {
		a.eprintf("ac: %v\n", err)
	}
	return r.Tasks
}

func (a *app) inspect(t worktree.Task) taskInfo {
	info := taskInfo{Task: t, Live: a.probe().Check(t)}
	if _, err := os.Lstat(t.Worktree); errors.Is(err, os.ErrNotExist) {
		info.Missing = true
		return info
	}
	info.Git = a.git().Status(t.Worktree, t.Base)
	return info
}

func (i taskInfo) gitLabel() string {
	if i.Missing {
		return "worktree missing"
	}
	if i.Git.Err != nil {
		return "git state unknown"
	}
	var parts []string
	if i.Git.Dirty {
		parts = append(parts, "dirty")
	}
	if i.Git.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", i.Git.Ahead))
	}
	if i.Git.Unpushed > 0 {
		parts = append(parts, fmt.Sprintf("(%d unpushed)", i.Git.Unpushed))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, " ")
}

func (a *app) age(t worktree.Task) string {
	if t.Started.IsZero() {
		return "?"
	}
	return worktree.Age(a.env.Now().Sub(t.Started))
}

// cmdRunning is `ac running` / `ac ls-tasks`.
func (a *app) cmdRunning() int {
	tasks := a.loadTasks()
	if len(tasks) == 0 {
		a.printf("no task sessions (start one: ac <project> --task \"...\")\n")
		return 0
	}
	w := tabwriter.NewWriter(a.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TASK\tBRANCH\tAGE\tSTATUS\tGIT\tBACKEND")
	for _, t := range tasks {
		i := a.inspect(t)
		backend := t.Backend
		if t.Session != "" {
			backend += " " + t.Session
		}
		if backend == "" {
			backend = "?"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", clean(t.ID), clean(t.Branch), a.age(t), i.Live.State, i.gitLabel(), clean(backend))
	}
	w.Flush()
	return 0
}

// taskCount is the number of registered tasks (no liveness checks), for
// the menu footer. A corrupt registry counts as none, silently: doctor and
// the running view report it.
func (a *app) taskCount() int {
	r, _ := worktree.Load(a.env.TasksPath)
	return len(r.Tasks)
}

// taskRows feeds the TUI's running view.
func (a *app) taskRows() []tui.TaskRow {
	var rows []tui.TaskRow
	for _, t := range a.loadTasks() {
		i := a.inspect(t)
		rows = append(rows, tui.TaskRow{
			ID: clean(t.ID), Branch: clean(t.Branch), Task: clean(t.Task), Age: a.age(t),
			Status: i.Live.State, Git: i.gitLabel(), Backend: clean(t.Backend),
		})
	}
	return rows
}

// findTask resolves a task argument, reporting none/ambiguous itself.
func (a *app) findTask(q string) (worktree.Task, bool) {
	r, err := worktree.Load(a.env.TasksPath)
	if err != nil {
		a.eprintf("ac: %v\n", err)
	}
	ms := r.Find(q)
	switch len(ms) {
	case 0:
		a.eprintf("ac: no task matching '%s' (see 'ac running')\n", clean(q))
		return worktree.Task{}, false
	case 1:
		return ms[0], true
	}
	a.eprintf("ac: '%s' matches more than one task:\n", clean(q))
	for _, t := range ms {
		a.eprintf("      %s\n", clean(t.ID))
	}
	return worktree.Task{}, false
}

// openTask attaches to a live tmux/zellij task session, else cds into the
// worktree (a here/tab CLI runs in another terminal; ac can't attach to it).
func (a *app) openTask(id string) int {
	t, ok := a.findTask(id)
	if !ok {
		return 1
	}
	if _, err := os.Stat(t.Dir); err != nil {
		return a.fail(1, fmt.Errorf("task %s: worktree %s is gone (run 'ac prune')", clean(t.ID), t.Dir))
	}
	live := a.probe().Check(t)
	if (t.Backend == launch.BackendTmux || t.Backend == launch.BackendZellij) && (live.State == worktree.Running || live.State == worktree.Idle) {
		code, err := launch.Attach(t.Backend, t.Session, t.Dir, a.cdFile, a.launchDeps())
		if err == nil {
			return code
		}
		a.eprintf("ac: can't attach: %s\n", unwrapUnavailable(err))
	}
	return a.cdOnly(t.Dir)
}

// cmdFinish: ac finish <task> [--delete-branch].
func (a *app) cmdFinish(args []string) int {
	del, q := false, ""
	for _, x := range args {
		switch {
		case x == "--delete-branch":
			del = true
		case strings.HasPrefix(x, "-"):
			return a.fail(2, fmt.Errorf("finish: unknown option %s", x))
		case q != "":
			return a.fail(2, errors.New("usage: ac finish <task> [--delete-branch]"))
		default:
			q = x
		}
	}
	if q == "" {
		return a.fail(2, errors.New("usage: ac finish <task> [--delete-branch] (see 'ac running')"))
	}
	t, ok := a.findTask(q)
	if !ok {
		return 1
	}
	return a.finishTask(t, del)
}

// finishTask: guard -> show state -> confirm (the branch name when work
// could be lost) -> git worktree remove -> optional branch delete ->
// registry. Nothing outside worktree_dir, and nothing git doesn't list as
// a worktree of the task's repository, is ever removed.
func (a *app) finishTask(t worktree.Task, deleteBranch bool) int {
	root, err := a.worktreeRoot()
	if err != nil {
		return a.fail(1, err)
	}
	g := a.git()
	err = worktree.CheckPath(t.Worktree, root)
	if errors.Is(err, worktree.ErrGone) {
		a.printf("\n  Task %s: the worktree %s is already gone.\n", clean(t.ID), config.Tildify(t.Worktree))
		if st, err := os.Stat(t.Repo); err == nil && st.IsDir() {
			_ = g.Prune(t.Repo)
		}
		if code := a.forget(t); code != 0 {
			return code
		}
		a.printf("  Forgot it. Branch %s was kept.\n", clean(t.Branch))
		return 0
	}
	if err != nil {
		return a.fail(1, fmt.Errorf("refusing to finish %s: %v", clean(t.ID), err))
	}
	e, found, err := g.Find(t.Repo, t.Worktree)
	switch {
	case err != nil:
		return a.fail(1, fmt.Errorf("refusing to finish %s: can't list worktrees of %s: %v", clean(t.ID), t.Repo, err))
	case !found:
		return a.fail(1, fmt.Errorf("refusing to finish %s: %s is not a git worktree of %s", clean(t.ID), t.Worktree, t.Repo))
	case e.Main:
		return a.fail(1, fmt.Errorf("refusing to finish %s: %s is the repository itself", clean(t.ID), t.Worktree))
	}

	st := g.Status(t.Worktree, t.Base)
	live := a.probe().Check(t)
	a.printf("\n  Finish task %s\n  %s\n", clean(t.ID), config.Tildify(t.Worktree))
	a.printf("  branch: %s\n", clean(st.String()))
	a.printf("  status: %s (%s)\n", live.State, clean(live.Detail))
	if t.Task != "" {
		a.printf("  task:   %s\n", clean(t.Task))
	}
	if e.Branch != "" && e.Branch != t.Branch {
		a.printf("  ! the worktree now has %s checked out, not %s\n", clean(e.Branch), clean(t.Branch))
	}
	a.printf("\n")

	var risks []string
	if st.Err != nil {
		risks = append(risks, "a git state that could not be checked")
	}
	if st.Dirty {
		risks = append(risks, "uncommitted changes (they will be deleted)")
	}
	if st.Unpushed > 0 {
		r := fmt.Sprintf("%d %s not pushed to any remote", st.Unpushed, plural(st.Unpushed, "commit", "commits"))
		if deleteBranch {
			r += " (deleting the branch loses them)"
		}
		risks = append(risks, r)
	}
	if live.State == worktree.Running || live.State == worktree.Idle {
		risks = append(risks, "the CLI still running in it")
	}
	var ok bool
	if len(risks) > 0 {
		a.printf("  ! This worktree has %s.\n", strings.Join(risks, ", "))
		ans, _ := a.ask(fmt.Sprintf("  Type the branch name (%s) to confirm: ", clean(t.Branch)))
		ok = ans == t.Branch
	} else {
		q := "  Remove this worktree? (y/N): "
		if deleteBranch {
			q = "  Remove this worktree and delete its branch? (y/N): "
		}
		ok = a.yes(q, false)
	}
	if !ok {
		a.printf("\n  Cancelled; nothing removed.\n")
		return 1
	}
	if err := g.Remove(t.Repo, t.Worktree, st.Dirty || st.Err != nil); err != nil {
		return a.fail(1, fmt.Errorf("git worktree remove failed: %v", err))
	}
	a.printf("\n  Removed worktree %s\n", config.Tildify(t.Worktree))
	code := 0
	branchKept := true
	if deleteBranch {
		if err := g.DeleteBranch(t.Repo, t.Branch); err != nil {
			a.printf("  x Could not delete branch %s: %v\n", clean(t.Branch), err)
			code = 1
		} else {
			branchKept = false
			a.printf("  Deleted branch %s\n", clean(t.Branch))
		}
	}
	if c := a.forget(t); c != 0 {
		return c
	}
	_ = os.Remove(filepath.Dir(t.Worktree)) // only succeeds when empty
	if live.State == worktree.Running || live.State == worktree.Idle {
		switch t.Backend {
		case launch.BackendTmux:
			a.printf("  Its tmux session is still open: tmux kill-session -t %s\n", launch.ShQuote("="+t.Session))
		case launch.BackendZellij:
			a.printf("  Its zellij session is still open: zellij kill-session %s\n", launch.ShQuote(t.Session))
		default:
			a.printf("  Its CLI is still running in another terminal; quit it there.\n")
		}
	}
	if branchKept {
		repo := launch.ShQuote(t.Repo)
		b := launch.ShQuote(t.Branch)
		if st.Ahead == 0 && st.Err == nil && !st.Dirty {
			a.printf("  Branch %s has no new commits; delete it with: git -C %s branch -d %s\n", clean(t.Branch), repo, b)
		} else {
			a.printf("  Branch %s kept. To merge it:\n      git -C %s merge %s\n  or push it for review:\n      git -C %s push -u origin %s\n", clean(t.Branch), repo, b, repo, b)
		}
	}
	return code
}

// forget drops t from the registry and deletes its pid file.
func (a *app) forget(t worktree.Task) int {
	err := worktree.Update(a.env.TasksPath, a.env.Now(), func(r *worktree.Registry) error {
		r.Delete(t.ID)
		return nil
	})
	if err != nil {
		return a.fail(1, err)
	}
	os.Remove(worktree.PIDPath(a.env.TasksPath, t.ID))
	return 0
}

// cmdPrune drops registry entries whose worktree directory is gone (and
// lets git forget them too).
func (a *app) cmdPrune() int {
	if _, err := worktree.Load(a.env.TasksPath); errors.Is(err, worktree.ErrCorrupt) {
		a.eprintf("ac: %v\n    setting it aside as %s.corrupt-<time> and starting a fresh registry\n", err, a.env.TasksPath)
	}
	var pruned []worktree.Task
	g := a.git()
	err := worktree.Update(a.env.TasksPath, a.env.Now(), func(r *worktree.Registry) error {
		repos := map[string]bool{}
		kept := r.Tasks[:0]
		for _, t := range r.Tasks {
			if _, err := os.Lstat(t.Worktree); errors.Is(err, os.ErrNotExist) {
				pruned = append(pruned, t)
				if st, err := os.Stat(t.Repo); err == nil && st.IsDir() && !repos[t.Repo] {
					repos[t.Repo] = true
					_ = g.Prune(t.Repo)
				}
				continue
			}
			kept = append(kept, t)
		}
		r.Tasks = kept
		return nil
	})
	if err != nil {
		return a.fail(1, err)
	}
	if len(pruned) == 0 {
		a.printf("nothing to prune\n")
		return 0
	}
	for _, t := range pruned {
		os.Remove(worktree.PIDPath(a.env.TasksPath, t.ID))
		a.printf("pruned %s (worktree gone; branch %s kept)\n", clean(t.ID), clean(t.Branch))
	}
	return 0
}
