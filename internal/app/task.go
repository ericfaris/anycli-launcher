package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericfaris/anycli-launcher/internal/clis"
	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/launch"
	"github.com/ericfaris/anycli-launcher/internal/projects"
	"github.com/ericfaris/anycli-launcher/internal/worktree"
)

// Task sessions: `ac <project> --task "..."` creates a git worktree on a
// new ac/<slug> branch under worktree_dir, records it in the registry and
// launches the CLI there (any backend) with the task as its prompt.

func (a *app) git() worktree.Git {
	return worktree.Git{Run: func(dir string, args ...string) (string, error) {
		return a.env.Run(dir, "git", args...)
	}}
}

func (a *app) probe() worktree.Probe {
	p := a.env.Probe
	if p.LookPath == nil {
		p.LookPath = a.env.LookPath
	}
	if p.Now == nil {
		p.Now = a.env.Now
	}
	return p
}

// checkTaskText rejects descriptions that can't be a prompt argument: empty,
// a leading '-' (the CLI would parse it as an option) or NUL bytes.
func checkTaskText(text string) (string, error) {
	t := strings.TrimSpace(text)
	switch {
	case t == "":
		return "", errors.New("the task description is empty")
	case strings.HasPrefix(t, "-"):
		return "", fmt.Errorf("a task description can't start with '-' (the CLI would read %q as an option)", firstWord(t))
	case strings.ContainsRune(t, 0):
		return "", errors.New("the task description contains a NUL byte")
	}
	return t, nil
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

// resolveOne resolves a project name like launchByName (exact, prefix,
// fuzzy, frecency), reporting none/ambiguous itself.
func (a *app) resolveOne(query string) (projects.Project, bool) {
	matches := projects.ResolveRanked(projects.Discover(a.cfg), query, a.scores())
	switch len(matches) {
	case 0:
		a.eprintf("ac: no project matching '%s'\n    run 'ac list' to see what is available\n", query)
		return projects.Project{}, false
	case 1:
		return matches[0], true
	}
	a.eprintf("ac: '%s' matches more than one project:\n", query)
	for _, m := range matches {
		a.eprintf("      %s\n", m.Label())
	}
	return projects.Project{}, false
}

// worktreeRoot is worktree_dir, validated: absolute, not a project root and
// not containing one.
func (a *app) worktreeRoot() (string, error) {
	root := a.cfg.WorktreeRoot()
	if err := worktree.CheckRoot(root, a.cfg.Roots()); err != nil {
		return "", err
	}
	return root, nil
}

func (a *app) startTask(query, text, base string) int {
	text, err := checkTaskText(text)
	if err != nil {
		return a.fail(2, err)
	}
	if err := a.requireRoot(); err != nil {
		return a.fail(1, err)
	}
	p, ok := a.resolveOne(query)
	if !ok {
		return 1
	}
	if p.OverlayErr != nil {
		a.eprintf("ac: ignoring %s: %v\n", config.OverlayFile, p.OverlayErr)
	}
	// Check everything that can fail before a worktree exists.
	cli, _ := a.cliFor(p.Settings)
	if cli == "" {
		return a.fail(1, errors.New("no AI CLI configured yet; run 'ac setup'"))
	}
	if _, err := a.env.LookPath(cli); err != nil {
		return a.fail(1, fmt.Errorf("%s: %w (configured cli); run 'ac doctor'", cli, launch.ErrNotFound))
	}
	if _, err := a.env.LookPath("git"); err != nil {
		return a.fail(1, errors.New("--task needs git, which is not on PATH"))
	}
	g := a.git()
	top, err := g.TopLevel(p.Dir)
	if err != nil {
		return a.fail(1, fmt.Errorf("%s is not a git repository; --task needs one (git init, commit, then retry)", p.Label()))
	}
	sub, err := subdirOf(p.Dir, top)
	if err != nil {
		return a.fail(1, err)
	}
	sha, err := g.ResolveBase(top, base)
	if err != nil {
		return a.fail(1, err)
	}
	root, err := a.worktreeRoot()
	if err != nil {
		return a.fail(1, err)
	}
	key := worktree.SafeComponent(p.Label())
	parent := filepath.Join(root, key)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return a.fail(1, err)
	}
	// A planted symlink (worktree_dir/<project> -> elsewhere) is refused.
	if err := worktree.CheckPath(parent, root); err != nil {
		return a.fail(1, err)
	}

	slug := worktree.Slug(text)
	var t worktree.Task
	err = worktree.Update(a.env.TasksPath, a.env.Now(), func(r *worktree.Registry) error {
		s, err := worktree.Unique(slug, func(s string) (bool, error) {
			if _, ok := r.Get(key + "/" + s); ok {
				return true, nil
			}
			if g.BranchExists(top, worktree.BranchPrefix+s) {
				return true, nil
			}
			_, err := os.Lstat(filepath.Join(parent, s))
			return err == nil, nil
		})
		if err != nil {
			return err
		}
		id := key + "/" + s
		path := filepath.Join(parent, s)
		branch := worktree.BranchPrefix + s
		if err := g.Add(top, branch, path, sha); err != nil {
			return fmt.Errorf("git worktree add failed: %w", err)
		}
		t = worktree.Task{
			ID: id, Project: p.Label(), Repo: top, Worktree: path, Dir: filepath.Join(path, sub),
			Branch: branch, Base: sha, Task: text, CLI: cli, Started: a.env.Now(),
		}
		r.Put(t)
		return nil
	})
	if err != nil {
		return a.fail(1, err)
	}
	a.eprintf("ac: task %s on branch %s\n    %s\n    finish with: ac finish %s\n", t.ID, t.Branch, config.Tildify(t.Worktree), t.ID)

	s := p.Settings
	s.Prompt = text
	rawSession := p.Label() + "/" + t.Slug()
	hook := launchHook{
		pidFile: worktree.PIDPath(a.env.TasksPath, t.ID),
		onBackend: func(name string) {
			t.Backend, t.Session, t.PIDFile = name, "", ""
			switch name {
			case launch.BackendTmux, launch.BackendZellij:
				t.Session = launch.SessionName(rawSession)
			default:
				t.PIDFile = worktree.PIDPath(a.env.TasksPath, t.ID)
			}
			if err := worktree.Update(a.env.TasksPath, a.env.Now(), func(r *worktree.Registry) error {
				r.Put(t)
				return nil
			}); err != nil {
				a.eprintf("ac: could not record the task's backend: %v\n", err)
			}
		},
	}
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return a.fail(1, err)
	}
	return a.launchDirWith(t.Dir, rawSession, s, clis.Fresh, hook)
}

// subdirOf is dir relative to the repository top level ("." when equal),
// so a project inside a bigger repo runs in the same subfolder of the
// worktree. It walks up from dir comparing with os.SameFile, because git
// prints the top level in its own spelling (symlinks resolved, long names
// instead of Windows 8.3 ones, forward slashes).
func subdirOf(dir, top string) (string, error) {
	tst, err := os.Stat(top)
	if err != nil {
		return "", err
	}
	var parts []string
	for d := filepath.Clean(dir); ; {
		if st, err := os.Stat(d); err == nil && os.SameFile(st, tst) {
			if len(parts) == 0 {
				return ".", nil
			}
			for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
				parts[i], parts[j] = parts[j], parts[i]
			}
			return filepath.Join(parts...), nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("%s is not inside its repository %s", dir, top)
		}
		parts = append(parts, filepath.Base(d))
		d = parent
	}
}
