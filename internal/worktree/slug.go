// Package worktree runs task sessions: `ac <project> --task "..."` makes a
// git worktree on a new branch, the CLI runs there, and a small registry
// (tasks.json, next to the frecency history) remembers each session so the
// running view can show, attach to and finish it.
//
// Git is always run as argv (never through a shell), with "--" before
// paths, and every value that reaches git is either generated here
// (branches, paths) or validated (the --base ref).
package worktree

import (
	"fmt"
	"strings"
)

// BranchPrefix starts every task branch. finish only deletes branches with
// this prefix, so a tampered registry can't point it at "main".
const BranchPrefix = "ac/"

// maxSlug keeps branch names and paths short (Windows MAX_PATH, tmux).
const maxSlug = 40

// Slug turns a task description into a branch/path component: lowercase
// ASCII letters and digits, runs of anything else become one '-', trimmed,
// at most 40 characters. A description with nothing usable gives "task".
// The result never starts with '-' or '.', so git and shells can't read it
// as an option, and never is a Windows device name.
func Slug(task string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(task) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	s := b.String()
	if len(s) > maxSlug {
		s = s[:maxSlug]
		// Prefer cutting at a word boundary when one is reasonably close.
		if i := strings.LastIndexByte(s, '-'); i >= maxSlug/2 {
			s = s[:i]
		}
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		return "task"
	}
	if reservedName(s) {
		s += "-task"
	}
	return s
}

// SafeComponent turns a project label ("work/api", "My App") into one path
// component: [A-Za-z0-9._-] kept, '/' and everything else become '-', no
// leading '.' or '-', no trailing '.', not a Windows device name.
func SafeComponent(label string) string {
	var b strings.Builder
	for _, r := range label {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'), r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.TrimLeft(b.String(), ".-")
	s = strings.TrimRight(s, ".")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], ".")
	}
	if s == "" {
		return "project"
	}
	if reservedName(s) {
		s += "-project"
	}
	return s
}

// reservedName reports Windows device names (CON, NUL, COM1...), which
// can't be used as file names there even with an extension.
func reservedName(s string) bool {
	base := strings.ToLower(s)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "con", "prn", "aux", "nul":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

// maxTries bounds Unique's search.
const maxTries = 100

// Unique returns slug, or slug-2, slug-3, ... the first one taken reports
// as free. taken is asked about the branch, the path and the registry.
func Unique(slug string, taken func(string) (bool, error)) (string, error) {
	for i := 1; i <= maxTries; i++ {
		s := slug
		if i > 1 {
			s += fmt.Sprintf("-%d", i)
		}
		t, err := taken(s)
		if err != nil {
			return "", err
		}
		if !t {
			return s, nil
		}
	}
	return "", fmt.Errorf("no free task name for %q after %d tries; finish or prune old tasks", slug, maxTries)
}

// ValidBranch reports whether b is a branch this package created:
// "ac/" + a slug (optionally with a -N suffix).
func ValidBranch(b string) bool {
	s, ok := strings.CutPrefix(b, BranchPrefix)
	if !ok || s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}
