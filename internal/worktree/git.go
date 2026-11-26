package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Git runs git with argv in dir and returns trimmed stdout. A failing
// command's error carries git's stderr. Run is the seam for tests; nil
// means the real git.
type Git struct {
	Run func(dir string, args ...string) (string, error)
}

// ExecGit is the real runner: exec.Command("git", args...) in dir, no shell.
func ExecGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
			err = fmt.Errorf("git %s: %s", args[0], msg)
		}
	}
	return strings.TrimSpace(string(out)), err
}

func (g Git) run(dir string, args ...string) (string, error) {
	if g.Run == nil {
		return ExecGit(dir, args...)
	}
	return g.Run(dir, args...)
}

// TopLevel is the repository root containing dir; an error means dir is
// not in a git work tree.
func (g Git) TopLevel(dir string) (string, error) {
	out, err := g.run(dir, "rev-parse", "--show-toplevel")
	if err != nil || out == "" {
		return "", fmt.Errorf("%s is not a git repository", dir)
	}
	return filepath.Clean(filepath.FromSlash(out)), nil
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// ValidSHA reports whether s is a full object id (SHA-1 or SHA-256).
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// ResolveBase turns a --base ref (or "" for HEAD) into a commit id. A ref
// starting with '-' is refused outright, so it can never act as an option.
func (g Git) ResolveBase(repo, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\n\r") {
		return "", fmt.Errorf("invalid base ref %q", ref)
	}
	out, err := g.run(repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || !ValidSHA(out) {
		if ref == "HEAD" {
			return "", errors.New("the repository has no commits yet; commit something first (a worktree needs a base commit)")
		}
		return "", fmt.Errorf("base %q is not a commit in this repository", ref)
	}
	return out, nil
}

// BranchExists reports whether refs/heads/<branch> exists.
func (g Git) BranchExists(repo, branch string) bool {
	_, err := g.run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// Add creates the worktree at path on a new branch starting at base.
func (g Git) Add(repo, branch, path, base string) error {
	if !ValidBranch(branch) || !ValidSHA(base) {
		return fmt.Errorf("refusing to create worktree: bad branch %q or base %q", branch, base)
	}
	_, err := g.run(repo, "worktree", "add", "--quiet", "-b", branch, "--", path, base)
	return err
}

// Entry is one `git worktree list --porcelain` record.
type Entry struct {
	Path   string
	Head   string
	Branch string // short name ("ac/x"); "" when detached
	Main   bool   // the first entry: the repository's own work tree
}

// List returns the repository's worktrees.
func (g Git) List(repo string) ([]Entry, error) {
	out, err := g.run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseList(out), nil
}

// ParseList parses `git worktree list --porcelain` output.
func ParseList(out string) []Entry {
	var list []Entry
	for _, block := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
		var e Entry
		for _, line := range strings.Split(block, "\n") {
			k, v, _ := strings.Cut(line, " ")
			switch k {
			case "worktree":
				e.Path = filepath.Clean(filepath.FromSlash(v))
			case "HEAD":
				e.Head = v
			case "branch":
				e.Branch = strings.TrimPrefix(v, "refs/heads/")
			}
		}
		if e.Path != "" {
			e.Main = len(list) == 0
			list = append(list, e)
		}
	}
	return list
}

// Find returns the repository's worktree that is the same directory as
// path (compared with os.SameFile, so 8.3 names, symlinked temp dirs and
// slash styles don't matter).
func (g Git) Find(repo, path string) (Entry, bool, error) {
	list, err := g.List(repo)
	if err != nil {
		return Entry{}, false, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return Entry{}, false, nil
	}
	for _, e := range list {
		if est, err := os.Stat(e.Path); err == nil && os.SameFile(st, est) {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

// Remove deletes a registered worktree through git (which refuses
// anything that isn't one). force discards uncommitted changes.
func (g Git) Remove(repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := g.run(repo, append(args, "--", path)...)
	return err
}

// DeleteBranch force-deletes a task branch (only "ac/..." ones).
func (g Git) DeleteBranch(repo, branch string) error {
	if !ValidBranch(branch) {
		return fmt.Errorf("refusing to delete branch %q: not an ac task branch", branch)
	}
	_, err := g.run(repo, "branch", "-D", "--", branch)
	return err
}

// Prune drops git's records of worktrees whose directory is gone.
func (g Git) Prune(repo string) error {
	_, err := g.run(repo, "worktree", "prune")
	return err
}

// State is a worktree's git summary for the running view and finish.
type State struct {
	Branch   string
	Dirty    bool // uncommitted changes, untracked files included
	Ahead    int  // commits since the base
	Unpushed int  // of those, commits on no remote
	// Err is set when the state could not be read; callers treat that as
	// "may hold work" (finish asks for the typed confirmation).
	Err error
}

// String renders "ac/x* ↑2 (2 unpushed)".
func (s State) String() string {
	if s.Err != nil {
		return "git state unknown"
	}
	out := s.Branch
	if s.Dirty {
		out += "*"
	}
	if s.Ahead > 0 {
		out += fmt.Sprintf(" ↑%d", s.Ahead)
	}
	if s.Unpushed > 0 {
		out += fmt.Sprintf(" (%d unpushed)", s.Unpushed)
	}
	return out
}

// Status reads dirty / ahead / unpushed for the worktree at path. base is
// the recorded base commit; when it isn't a valid id the counts cover every
// commit not on a remote.
func (g Git) Status(path, base string) State {
	var s State
	branch, err := g.run(path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		s.Err = err
		return s
	}
	s.Branch = branch
	st, err := g.run(path, "status", "--porcelain")
	if err != nil {
		s.Err = err
		return s
	}
	s.Dirty = st != ""
	rng := []string{"HEAD"}
	if ValidSHA(base) {
		rng = []string{base + "..HEAD"}
		n, err := g.count(path, rng...)
		if err != nil {
			s.Err = err
			return s
		}
		s.Ahead = n
	}
	n, err := g.count(path, append(rng, "--not", "--remotes")...)
	if err != nil {
		s.Err = err
		return s
	}
	s.Unpushed = n
	if !ValidSHA(base) {
		s.Ahead = n
	}
	return s
}

func (g Git) count(dir string, revs ...string) (int, error) {
	out, err := g.run(dir, append([]string{"rev-list", "--count"}, revs...)...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// MinGit is the oldest git with everything used here (`worktree remove`
// arrived in 2.17).
var MinGit = [2]int{2, 17}

// Version parses `git version` output ("git version 2.43.0",
// "2.39.3 (Apple Git-145)", "2.42.0.windows.2").
func Version(out string) (major, minor int, ok bool) {
	f := strings.Fields(out)
	if len(f) < 3 || f[0] != "git" || f[1] != "version" {
		return 0, 0, false
	}
	parts := strings.Split(f[2], ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(parts[1])
	return ma, mi, err1 == nil && err2 == nil
}

// VersionOK reports whether major.minor is at least MinGit.
func VersionOK(major, minor int) bool {
	return major > MinGit[0] || (major == MinGit[0] && minor >= MinGit[1])
}
