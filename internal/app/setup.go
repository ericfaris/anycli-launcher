package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ericfaris/anycli-launcher/v4/internal/clis"
	"github.com/ericfaris/anycli-launcher/v4/internal/config"
)

// rootCandidates are common project roots that exist and have at least one
// subdirectory; ~/projects (the recommended convention) comes first.
func rootCandidates(home string) []string {
	var out []string
	for _, n := range []string{"projects", "code", "dev", "repos", "src", "workspace"} {
		d := filepath.Join(home, n)
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// normalizePath expands "~" and makes the path absolute.
func normalizePath(p string) string {
	p = config.ExpandHome(strings.TrimSpace(p))
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// ensureDir offers to create a missing directory; false means cancel.
func (a *app) ensureDir(dir string) bool {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return true
	}
	if !a.yes("  "+dir+" does not exist yet. Create it? (Y/n): ", true) {
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.printf("  x could not create %s: %v\n", dir, err)
		return false
	}
	return true
}

// wizard is the first-run guided setup: projects root, CLI, default flags.
func (a *app) wizard() int {
	home := a.env.Home
	a.printf("\n  Welcome to anycli-launcher\n  No config yet; three quick questions.\n")

	a.printf("\n  1. Where do your projects live? (~/projects is the recommended convention)\n\n")
	cands := rootCandidates(home)
	root := ""
	if len(cands) > 0 {
		for i, c := range cands {
			tag := ""
			if c == filepath.Join(home, "projects") {
				tag = "  (recommended)"
			}
			a.printf("     %d  %s%s\n", i+1, config.Tildify(c), tag)
		}
		a.printf("     %d  other (type a path)\n\n", len(cands)+1)
		pick, ok := a.ask("  > ")
		if !ok {
			return 1
		}
		if n, err := strconv.Atoi(strings.TrimSpace(pick)); err == nil && n >= 1 && n <= len(cands) {
			root = cands[n-1]
		}
	}
	if root == "" {
		def := ""
		if len(cands) == 0 {
			def = filepath.Join(home, "projects")
			a.printf("  Enter your projects directory, or press Enter for ~/projects:\n")
		} else {
			a.printf("  Enter your projects directory (e.g. ~/projects):\n")
		}
		typed, ok := a.ask("  > ")
		if !ok {
			return 1
		}
		if strings.TrimSpace(typed) == "" {
			typed = def
		}
		root = normalizePath(typed)
		if root == "" {
			a.printf("  No directory given; setup cancelled.\n")
			return 1
		}
	}
	if !a.ensureDir(root) {
		a.printf("  Setup cancelled.\n")
		return 1
	}

	a.printf("\n  2. Which AI CLI should I launch?\n\n")
	cli := ""
	if found := clis.Detect(a.env.LookPath); len(found) > 0 {
		for i, c := range found {
			a.printf("     %d  %s\n", i+1, c)
		}
		pick, ok := a.ask("\n  > ")
		if !ok {
			return 1
		}
		cli = found[0]
		if n, err := strconv.Atoi(strings.TrimSpace(pick)); err == nil && n >= 1 && n <= len(found) {
			cli = found[n-1]
		}
	} else {
		a.printf("     No known AI CLI found on PATH.\n")
		typed, ok := a.ask("     Enter the command to run (e.g. claude, aider): ")
		cli = strings.TrimSpace(typed)
		if !ok || cli == "" {
			a.printf("  Setup cancelled; no CLI given.\n")
			return 1
		}
	}

	a.printf("\n  3. Default flags for every launch? (optional, Enter to skip)\n")
	flags, ok := a.ask("  > ")
	if !ok {
		return 1
	}

	cfg := config.Default()
	cfg.ProjectsDir = config.Tildify(root)
	cfg.CLI = cli
	cfg.DefaultFlags = strings.TrimSpace(flags)
	if err := config.Save(a.env.ConfigPath, cfg); err != nil {
		return a.fail(1, err)
	}
	a.cfg, a.cfgExists = cfg, true
	a.printf("\n  Setup complete. Config saved to %s\n  Change it later with 'ac setup' or 'ac config'.\n", a.env.ConfigPath)
	return 0
}

// setup re-runs the three questions with current values as defaults; Enter
// keeps each, "-" clears flags. Per-project settings and other preferences
// are preserved.
func (a *app) setup() int {
	cfg := a.cfg
	a.printf("\n  anycli-launcher setup. Press Enter to keep the current value.\n")

	a.printf("\n  1. Projects directory (current: %s)\n", cfg.ProjectsDir)
	v, ok := a.ask("  > ")
	if !ok {
		return 1
	}
	if strings.TrimSpace(v) != "" {
		root := normalizePath(v)
		if a.ensureDir(root) {
			cfg.ProjectsDir = config.Tildify(root)
		} else {
			a.printf("  Keeping current value.\n")
		}
	}

	cur := cfg.CLI
	if cur == "" {
		cur = "(none)"
	}
	a.printf("\n  2. AI CLI (current: %s)\n", cur)
	found := clis.Detect(a.env.LookPath)
	for i, c := range found {
		a.printf("       %d  %s\n", i+1, c)
	}
	v, ok = a.ask("  > number to switch, a command name, or Enter to keep: ")
	if !ok {
		return 1
	}
	v = strings.TrimSpace(v)
	newCLI := ""
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(found) {
		newCLI = found[n-1]
	} else if v != "" {
		newCLI = v
	}
	if newCLI != "" && newCLI != cfg.CLI {
		cfg.CLI, cfg.CLILaunch = newCLI, ""
	}

	cur = cfg.DefaultFlags
	if cur == "" {
		cur = "(none)"
	}
	a.printf("\n  3. Default flags (current: %s); Enter to keep, '-' to clear\n", cur)
	v, ok = a.ask("  > ")
	if !ok {
		return 1
	}
	switch v = strings.TrimSpace(v); v {
	case "":
	case "-":
		cfg.DefaultFlags = ""
	default:
		cfg.DefaultFlags = v
	}

	if err := config.Save(a.env.ConfigPath, cfg); err != nil {
		return a.fail(1, err)
	}
	a.cfg = cfg
	a.printf("\n  Setup saved to %s. Per-project settings were preserved.\n", a.env.ConfigPath)
	return 0
}
