// Package projects discovers project directories under the configured
// roots and resolves user-typed names to them.
package projects

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/frecency"
	"github.com/ericfaris/anycli-launcher/internal/fuzzy"
)

// Project is one launchable directory.
type Project struct {
	Name  string // directory basename (the config key)
	Dir   string // absolute path
	Root  string // configured root it was found under
	Group string // grouping folder under scan_depth 2, else ""
	// Settings are the merged config.toml + .ac.toml settings.
	Settings config.Project
	// OverlayErr is set when .ac.toml exists but could not be used.
	OverlayErr error
}

// Label is what the menu shows: the alias, else group/name for nested
// projects (so work/api and personal/api stay distinct), else the name.
func (p Project) Label() string {
	if p.Settings.Alias != "" {
		return p.Settings.Alias
	}
	if p.Group != "" {
		return p.Group + "/" + p.Name
	}
	return p.Name
}

// Discover lists projects: each root in order, its subdirectories sorted by
// name (case-insensitive). Hidden directories are skipped. With
// scan_depth 2, a directory that is not a git repo but has subdirectories is
// a grouping folder and its children are listed in its place.
func Discover(cfg config.Config) []Project {
	var out []Project
	for _, root := range cfg.Roots() {
		for _, dir := range subdirs(root) {
			base := filepath.Base(dir)
			if cfg.ScanDepth == 2 && !isDir(filepath.Join(dir, ".git")) {
				if kids := subdirs(dir); len(kids) > 0 {
					for _, k := range kids {
						out = append(out, newProject(cfg, root, base, k))
					}
					continue
				}
			}
			out = append(out, newProject(cfg, root, "", dir))
		}
	}
	return out
}

func newProject(cfg config.Config, root, group, dir string) Project {
	p := Project{Name: filepath.Base(dir), Dir: dir, Root: root, Group: group}
	ov, err := config.LoadOverlay(dir)
	p.OverlayErr = err
	p.Settings = cfg.Settings(p.Name, ov)
	return p
}

func subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if isDir(full) { // follows symlinks, like bash's */ glob
			out = append(out, full)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(filepath.Base(out[i])), strings.ToLower(filepath.Base(out[j]))
		if a == b {
			return filepath.Base(out[i]) < filepath.Base(out[j])
		}
		return a < b
	})
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Names returns the directory basenames (for config key repair).
func Names(list []Project) []string {
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = p.Name
	}
	return out
}

// Resolve matches query against name, alias and label, trying exact,
// case-insensitive exact, case-insensitive prefix, substring, then fuzzy
// subsequence; the first tier with any match wins. One result means
// resolved; more is ambiguous.
func Resolve(list []Project, query string) []Project {
	return ResolveRanked(list, query, nil)
}

// ResolveRanked is Resolve with launch history. Exact and
// case-insensitive-exact matches always win as in Resolve. When the winning
// tier is prefix, substring or fuzzy and has several matches, the one with
// the strictly highest frecency score (scores keyed by Dir, see
// internal/frecency) is returned alone. Otherwise the ambiguous matches come
// back ordered by score, then (for fuzzy) match quality, then list order.
func ResolveRanked(list []Project, query string, scores map[string]float64) []Project {
	if query == "" {
		return nil
	}
	q := strings.ToLower(query)
	var tiers [5][]Project
	fz := map[string]int{}
	for _, p := range list {
		cands := []string{p.Name, p.Label()}
		if p.Settings.Alias != "" {
			cands = append(cands, p.Settings.Alias)
		}
		tier := -1
		for _, c := range cands {
			lc := strings.ToLower(c)
			t := -1
			switch {
			case c == query:
				t = 0
			case lc == q:
				t = 1
			case strings.HasPrefix(lc, q):
				t = 2
			case strings.Contains(lc, q):
				t = 3
			default:
				if s, _, ok := fuzzy.Match(query, c); ok {
					t = 4
					if old, seen := fz[p.Dir]; !seen || s > old {
						fz[p.Dir] = s
					}
				}
			}
			if t >= 0 && (tier < 0 || t < tier) {
				tier = t
			}
		}
		if tier >= 0 {
			tiers[tier] = append(tiers[tier], p)
		}
	}
	for i, t := range tiers {
		if len(t) == 0 {
			continue
		}
		if i < 2 || len(t) == 1 {
			return t
		}
		score := func(p Project) float64 { return frecency.Lookup(scores, p.Dir) }
		sort.SliceStable(t, func(a, b int) bool {
			if sa, sb := score(t[a]), score(t[b]); sa != sb {
				return sa > sb
			}
			return i == 4 && fz[t[a].Dir] > fz[t[b].Dir]
		})
		if s := score(t[0]); s > 0 && s > score(t[1]) {
			return t[:1]
		}
		return t
	}
	return nil
}

// SortByFrecency orders list by frecency score (highest first). Equal
// scores, including never-launched projects, keep their order in list,
// which is the name order Discover produces.
func SortByFrecency(list []Project, scores map[string]float64) []Project {
	out := append([]Project(nil), list...)
	sort.SliceStable(out, func(i, j int) bool {
		return frecency.Lookup(scores, out[i].Dir) > frecency.Lookup(scores, out[j].Dir)
	})
	return out
}

// ExactExists reports whether a project's name or alias equals query, so a
// project named like a subcommand ("doctor") wins over the subcommand.
func ExactExists(list []Project, query string) bool {
	for _, p := range list {
		if p.Name == query || p.Settings.Alias == query {
			return true
		}
	}
	return false
}
