package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/projects"
)

// SafeToRemove reports why dir must not be deleted, or nil when it is a
// project folder strictly beneath one of roots. Ported from v3
// _ac_safe_to_remove_dir (CHANGELOG 3.3.1) plus one extra guard: a folder
// that contains a configured root is refused too.
func SafeToRemove(dir string, roots []string, home string) error {
	if dir == "" {
		return errors.New("empty path")
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not an existing directory", dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	real, _ = filepath.Abs(real)
	if filepath.Dir(real) == real {
		return fmt.Errorf("refusing to remove filesystem root %s", real)
	}
	if home != "" {
		if h, err := filepath.EvalSymlinks(home); err == nil && sameDir(h, real) {
			return fmt.Errorf("refusing to remove your home directory %s", real)
		}
	}
	type rootPair struct{ raw, real string }
	var rs []rootPair
	for _, r := range roots {
		if r == "" {
			continue
		}
		rr, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		rr, _ = filepath.Abs(rr)
		if sameDir(rr, real) {
			return fmt.Errorf("refusing to remove project root %s", r)
		}
		if under(rr, real) {
			return fmt.Errorf("refusing to remove %s: it contains project root %s", dir, r)
		}
		rs = append(rs, rootPair{filepath.Clean(r), rr})
	}
	clean, _ := filepath.Abs(filepath.Clean(dir))
	for _, r := range rs {
		rawAbs, _ := filepath.Abs(r.raw)
		// The unresolved form counts too: a project that is a symlink is
		// removed by unlinking it, never by deleting its target.
		if under(real, r.real) || under(clean, rawAbs) {
			return nil
		}
	}
	return fmt.Errorf("%s is not inside a configured project root", dir)
}

// under reports whether child is strictly inside parent.
func under(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

// GitHubSlug extracts owner/repo from a GitHub remote URL; "" for anything
// else (GitLab, self-hosted, empty), which is then never touched.
func GitHubSlug(url string) string {
	var slug string
	for _, prefix := range []string{"git@github.com:", "ssh://git@github.com/", "https://github.com/", "http://github.com/"} {
		if strings.HasPrefix(url, prefix) {
			slug = strings.TrimPrefix(url, prefix)
			break
		}
	}
	slug = strings.TrimSuffix(strings.TrimSuffix(slug, "/"), ".git")
	if strings.Count(slug, "/") != 1 || strings.HasPrefix(slug, "/") || strings.HasSuffix(slug, "/") {
		return ""
	}
	return slug
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// unsavedWork describes work that only exists locally; "" when clean or not
// a repo. A repo whose state can't be checked counts as unsaved.
func (a *app) unsavedWork(dir string) string {
	if !isGitRepo(dir) {
		return ""
	}
	if _, err := a.env.LookPath("git"); err != nil {
		return "a git repo whose state could not be checked (git not found)"
	}
	var parts []string
	if out, err := a.env.Run(dir, "git", "status", "--porcelain"); err != nil || out != "" {
		parts = append(parts, "uncommitted changes")
	}
	if out, err := a.env.Run(dir, "git", "log", "--branches", "--not", "--remotes", "--oneline", "-n", "1"); err == nil && out != "" {
		parts = append(parts, "commits not pushed to any remote")
	}
	return strings.Join(parts, " and ")
}

// removeByName removes a project by exact (or case-insensitive exact) name,
// alias or label. Fuzzy prefix/substring matches are not accepted here.
func (a *app) removeByName(query string) int {
	var exact []projects.Project
	for _, p := range projects.Resolve(projects.Discover(a.cfg), query) {
		for _, c := range []string{p.Name, p.Label(), p.Settings.Alias} {
			if c != "" && strings.EqualFold(c, query) {
				exact = append(exact, p)
				break
			}
		}
	}
	switch len(exact) {
	case 0:
		return a.fail(1, fmt.Errorf("no project named '%s' (remove needs the exact name; see 'ac list')", query))
	case 1:
		return a.removeProject(exact[0])
	}
	a.eprintf("ac: '%s' matches more than one project; use its group/name label:\n", query)
	for _, p := range exact {
		a.eprintf("      %s\n", p.Label())
	}
	return 1
}

// removeProject: guard -> confirm (typed name when there is unsaved work)
// -> optional GitHub repo delete, with gh verified before anything is
// deleted -> delete folder -> delete repo -> drop per-project config.
func (a *app) removeProject(p projects.Project) int {
	dir := p.Dir
	if err := SafeToRemove(dir, a.cfg.Roots(), a.env.Home); err != nil {
		return a.fail(1, fmt.Errorf("refusing to remove %s: %v", p.Label(), err))
	}
	a.printf("\n  Remove project %s\n  %s\n\n", p.Label(), config.Tildify(dir))

	var ok bool
	if unsaved := a.unsavedWork(dir); unsaved != "" {
		a.printf("  ! This repo has %s; deleting it may lose work.\n", unsaved)
		ans, _ := a.ask(fmt.Sprintf("  Type the project name (%s) to confirm: ", p.Name))
		ok = ans == p.Name
	} else {
		ok = a.yes("  Delete this folder and everything in it? This cannot be undone. (y/N): ", false)
	}
	if !ok {
		a.printf("\n  Cancelled; nothing removed.\n")
		return 1
	}

	slug := ""
	if isGitRepo(dir) {
		if _, err := a.env.LookPath("git"); err == nil {
			url, _ := a.env.Run(dir, "git", "remote", "get-url", "origin")
			slug = GitHubSlug(url)
		}
	}
	deleteGH := false
	if slug != "" {
		a.printf("\n  GitHub remote detected: %s\n", slug)
		deleteGH = a.yes(fmt.Sprintf("  Also delete the GitHub repo %s? This is permanent. (y/N): ", slug), false)
	}
	if deleteGH {
		if _, err := a.env.LookPath("gh"); err != nil {
			a.printf("\n  x gh is not installed; cancelled, nothing removed.\n")
			return 1
		}
		if _, err := a.env.Run(dir, "gh", "auth", "status"); err != nil {
			a.printf("\n  x gh is not authenticated (run 'gh auth login'); cancelled, nothing removed.\n")
			return 1
		}
	}

	if err := removeDir(dir); err != nil {
		return a.fail(1, fmt.Errorf("failed to remove %s: %v", dir, err))
	}
	a.printf("\n  Removed %s\n", config.Tildify(dir))

	code := 0
	if deleteGH {
		if _, err := a.env.Run(a.env.Home, "gh", "repo", "delete", slug, "--yes"); err != nil {
			a.printf("  x Failed to delete GitHub repo %s (check gh auth scopes: delete_repo)\n", slug)
			code = 1
		} else {
			a.printf("  Deleted GitHub repo %s\n", slug)
		}
	}

	if a.cfg.ProjectKey(p.Name) != "" {
		a.cfg.RemoveProject(p.Name)
		if err := config.Save(a.env.ConfigPath, a.cfg); err != nil {
			return a.fail(1, err)
		}
	}
	return code
}

// removeDir unlinks a symlink (never following it) or deletes a directory tree.
func removeDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return os.Remove(dir)
	}
	return os.RemoveAll(dir)
}
