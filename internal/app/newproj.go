package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ericfaris/anycli-launcher/internal/config"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateNewName checks a new project name against root (v3 rules).
func ValidateNewName(root, name string) error {
	switch {
	case name == "":
		return errors.New("name cannot be empty")
	case strings.ContainsAny(name, `/\`):
		return errors.New(`name cannot contain a path separator (/ or \); projects are created directly in the root`)
	case !nameRe.MatchString(name):
		return errors.New("use letters, digits, dot, dash or underscore; must start with a letter or digit")
	}
	if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
		return fmt.Errorf("a project named '%s' already exists", name)
	}
	return nil
}

// newProject creates root/<name>, git-inits it, and offers to set its
// alias/color/title/prompt. cd says whether to hand the new folder to the
// shim (true for `ac new`, false when called from the menu).
func (a *app) newProject(name string, cd bool) int {
	if err := a.requireRoot(); err != nil {
		return a.fail(1, err)
	}
	root := a.cfg.ProjectsRoot()
	if name != "" {
		if err := ValidateNewName(root, name); err != nil {
			return a.fail(1, err)
		}
	} else {
		a.printf("\n  New project in %s\n", config.Tildify(root))
		for {
			n, ok := a.ask("  Project name (blank to cancel): ")
			n = strings.TrimSpace(n)
			if !ok || n == "" {
				return 1
			}
			if err := ValidateNewName(root, n); err != nil {
				a.printf("  x %v\n", err)
				continue
			}
			name = n
			break
		}
	}

	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return a.fail(1, fmt.Errorf("could not create %s: %v", dir, err))
	}
	note := "git repository initialized"
	if _, err := a.env.LookPath("git"); err != nil {
		note = "git not found on PATH; folder created without a repo"
	} else if _, err := a.env.Run(dir, "git", "init", "-q"); err != nil {
		note = "git init failed; folder created without a repo"
	}
	a.printf("\n  Created %s\n  %s\n", config.Tildify(dir), note)

	if a.yes("\n  Set alias / color / title / prompt now? (Y/n): ", true) {
		p := a.cfg.Settings(name, nil)
		if v, ok := a.ask("  Alias (display name, blank to skip): "); ok {
			p.Alias = strings.TrimSpace(v)
		}
		for {
			v, ok := a.ask("  Tab color #rrggbb (blank to skip): ")
			v = strings.TrimSpace(v)
			if !ok || v == "" || config.ValidColor(v) {
				p.Color = v
				break
			}
			a.printf("  x want a 6-digit hex color like #00cc66\n")
		}
		if v, ok := a.ask("  Tab title (blank to skip): "); ok {
			p.Title = strings.TrimSpace(v)
		}
		if v, ok := a.ask("  Initial prompt (blank to skip): "); ok {
			p.Prompt = strings.TrimSpace(v)
		}
		if !p.IsZero() {
			a.cfg.SetProject(name, p)
			if err := config.Save(a.env.ConfigPath, a.cfg); err != nil {
				return a.fail(1, err)
			}
			a.printf("  Saved settings for %s\n", name)
		}
	}
	if cd {
		return a.cdOnly(dir)
	}
	return 0
}
