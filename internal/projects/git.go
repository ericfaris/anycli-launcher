package projects

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// GitStatus is a project's git summary. Branch is "" for non-repos.
type GitStatus struct {
	Branch        string
	Dirty         bool
	Ahead, Behind int
}

// String renders v3's marker: "main* ↑2 ↓1" (empty for non-repos).
func (g GitStatus) String() string {
	if g.Branch == "" {
		return ""
	}
	s := g.Branch
	if g.Dirty {
		s += "*"
	}
	if g.Ahead > 0 {
		s += fmt.Sprintf(" ↑%d", g.Ahead)
	}
	if g.Behind > 0 {
		s += fmt.Sprintf(" ↓%d", g.Behind)
	}
	return s
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// Git returns the status for dir. Any failure (no git, not a repo, no
// upstream) degrades to less information, never an error.
func Git(dir string) GitStatus {
	if !isDir(filepath.Join(dir, ".git")) && !isFile(filepath.Join(dir, ".git")) {
		return GitStatus{}
	}
	branch, err := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" {
		return GitStatus{}
	}
	g := GitStatus{Branch: branch}
	if st, err := git(dir, "status", "--porcelain"); err == nil && st != "" {
		g.Dirty = true
	}
	if counts, err := git(dir, "rev-list", "--left-right", "--count", "@{u}...HEAD"); err == nil {
		fmt.Sscanf(counts, "%d %d", &g.Behind, &g.Ahead)
	}
	return g
}

// GitAll fetches statuses for many dirs concurrently (bounded).
func GitAll(dirs []string) map[string]GitStatus {
	out := make(map[string]GitStatus, len(dirs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, d := range dirs {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			sem <- struct{}{}
			s := Git(d)
			<-sem
			mu.Lock()
			out[d] = s
			mu.Unlock()
		}(d)
	}
	wg.Wait()
	return out
}

// isFile catches worktrees and submodules, where .git is a file.
func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
