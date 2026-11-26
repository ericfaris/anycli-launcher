package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericfaris/anycli-launcher/v4/internal/clis"
	"github.com/ericfaris/anycli-launcher/v4/internal/config"
	"github.com/ericfaris/anycli-launcher/v4/internal/frecency"
	"github.com/ericfaris/anycli-launcher/v4/internal/projects"
	"github.com/ericfaris/anycli-launcher/v4/internal/tui"
)

type report struct {
	a            *app
	fails, warns int
}

func (r *report) section(s string) { r.a.printf("\n-- %s --\n", s) }
func (r *report) pass(s string)    { r.a.printf("  ✓ %s\n", s) }
func (r *report) info(s string)    { r.a.printf("    %s\n", s) }
func (r *report) warn(s, hint string) {
	r.warns++
	r.a.printf("  ! %s%s\n", s, suffix(hint))
}
func (r *report) fail(s, hint string) {
	r.fails++
	r.a.printf("  ✗ %s%s\n", s, suffix(hint))
}

func suffix(h string) string {
	if h == "" {
		return ""
	}
	return " (" + h + ")"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// doctor prints a health report; non-zero exit when a required check fails.
func (a *app) doctor() int {
	r := &report{a: a}
	a.printf("anycli-launcher doctor (v%s)\n", Version)

	r.section("Configuration")
	path := a.env.ConfigPath
	if a.cfgExists {
		r.pass("config file present (" + path + ")")
	} else {
		r.warn("no config file yet ("+path+")", "defaults in use; run 'ac setup'")
	}
	for _, w := range a.warns {
		r.warn(w, "")
	}
	if !a.cfgExists {
		if src := legacySource(path); src != "" {
			r.warn("v3 config found at "+src, "run 'ac migrate'")
		}
	}

	roots := a.cfg.Roots()
	if st, err := os.Stat(roots[0]); err != nil || !st.IsDir() {
		r.fail("projects directory not found ("+roots[0]+")", "set projects_dir or $AC_PROJECTS_DIR")
	} else {
		list := projects.Discover(a.cfg)
		if len(list) == 0 {
			r.warn("no projects found ("+roots[0]+")", "the menu will have nothing to list")
		} else {
			r.pass(fmt.Sprintf("%d %s under %s", len(list), plural(len(list), "project", "projects"), strings.Join(roots, ", ")))
		}
		for _, p := range list {
			if p.OverlayErr != nil {
				r.warn(p.Name+": "+p.OverlayErr.Error(), "file ignored")
			}
		}
	}
	for _, d := range roots[1:] {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			r.warn("extra dir not found ("+d+")", "skipped")
		}
	}
	if !tui.ValidTheme(a.cfg.Theme) {
		r.warn(fmt.Sprintf("unknown theme %q", a.cfg.Theme), "presets: "+strings.Join(tui.Presets, ", "))
	}
	if !config.ValidSortOrder(a.cfg.SortOrder) {
		r.warn(fmt.Sprintf("unknown sort_order %q", a.cfg.SortOrder), "use "+strings.Join(config.SortOrders, ", ")+"; sorting by name")
	}
	r.info(fmt.Sprintf("theme: %s · scan depth: %d · git status: %v · sort: %s", a.cfg.Theme, a.cfg.ScanDepth, a.cfg.ShowGitStatus, a.cfg.SortOrder))

	r.section("Launch history (frecency)")
	a.doctorFrecency(r)

	r.section("AI CLI")
	if a.cfg.CLI == "" {
		r.fail("no cli configured", "run 'ac setup'")
	} else {
		prof, known := clis.Lookup(a.cfg.CLI)
		if bin, err := a.env.LookPath(a.cfg.CLI); err == nil {
			r.pass(a.cfg.CLI + " on PATH -> " + bin)
		} else {
			r.fail(a.cfg.CLI+" not on PATH", "ac can cd but cannot launch it")
		}
		switch {
		case !known:
			r.warn("no built-in profile for "+a.cfg.CLI, "prompt passed positionally; no resume")
		case prof.Verified == "":
			r.warn("flags for "+a.cfg.CLI+" not verified against its --help", "report problems")
		default:
			r.pass("profile verified (" + prof.Verified + ")")
		}
		if known {
			if len(prof.Resume) > 0 {
				r.pass("resume: " + strings.Join(prof.Resume, " ") + " (r<N>, ac resume)")
			} else {
				r.warn("no resume support for "+a.cfg.CLI, "resume starts fresh")
			}
			if len(prof.Picker) > 0 {
				r.pass("session picker: " + strings.Join(prof.Picker, " ") + " (p<N>, ac pick)")
			} else {
				r.warn("no session picker for "+a.cfg.CLI, "picker starts fresh")
			}
		}
	}

	r.section("Shell & tools")
	if a.cdFile != "" {
		r.pass("shell integration active")
	} else {
		r.warn("shell integration not active", fmt.Sprintf(`add eval "$(ac init %s)" to your shell rc`, guessShell()))
	}
	if bin, err := a.env.LookPath("git"); err == nil {
		r.pass("git -> " + bin)
	} else {
		r.warn("git not on PATH", "no git status, ac new skips git init")
	}
	if bin, err := a.env.LookPath("gh"); err == nil {
		r.pass("gh -> " + bin)
	} else {
		r.warn("gh not on PATH", "only needed to delete GitHub repos in ac remove")
	}

	r.section("Launch backend")
	a.doctorBackends(r)

	r.section("Task worktrees")
	a.doctorTasks(r)

	a.printf("\n")
	if r.fails == 0 {
		a.printf("✓ Healthy")
		if r.warns > 0 {
			a.printf(" (%d %s)", r.warns, plural(r.warns, "warning", "warnings"))
		}
		a.printf("\n")
		return 0
	}
	a.printf("✗ Problems found (%d %s failed)\n", r.fails, plural(r.fails, "check", "checks"))
	return 1
}

func (a *app) doctorFrecency(r *report) {
	path := a.env.FrecencyPath
	s, err := frecency.Load(path)
	switch {
	case errors.Is(err, frecency.ErrCorrupt):
		r.warn("history file is corrupt ("+path+")", "ignored; the next launch starts a fresh history")
		return
	case err != nil:
		r.warn("history file unreadable: "+err.Error(), "ranking falls back to name order")
		return
	case !config.Exists(path):
		r.pass("no launches recorded yet (" + path + ")")
	default:
		r.pass(fmt.Sprintf("%d %s in %s", len(s.Entries), plural(len(s.Entries), "directory", "directories"), path))
	}
	if err := dirWritable(filepath.Dir(path)); err != nil {
		r.warn("history not writable: "+err.Error(), "launches won't be recorded")
	}
	if config.FrecencySort(a.cfg.SortOrder) {
		r.info("menu sorted by frecency (sort_order = " + a.cfg.SortOrder + ")")
	} else {
		r.info("menu sorted by name; frecency ranks the filter and 'ac <partial>' (sort_order = \"recent\" sorts the menu too)")
	}
}

// dirWritable checks that dir (or its nearest existing parent) accepts a
// new file, without leaving anything behind.
func dirWritable(dir string) error {
	for {
		if st, err := os.Stat(dir); err == nil {
			if !st.IsDir() {
				return fmt.Errorf("%s is not a directory", dir)
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("no existing parent for %s", dir)
		}
		dir = parent
	}
	f, err := os.CreateTemp(dir, ".ac-doctor-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// cmdMigrate: ac migrate [file] [--force].
func (a *app) cmdMigrate(args []string) int {
	force, src := false, ""
	for _, x := range args {
		if x == "--force" || x == "-f" {
			force = true
		} else {
			src = x
		}
	}
	if src == "" {
		src = legacySource(a.env.ConfigPath)
		if src == "" {
			src = config.LegacyYAMLPath()
		}
	}
	if err := a.migrate(src, a.env.ConfigPath, force); err != nil {
		return a.fail(1, err)
	}
	return 0
}

// migrate imports a v3 config into dst, leaving the v3 file untouched.
func (a *app) migrate(src, dst string, force bool) error {
	if config.Exists(dst) && !force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", dst)
	}
	m, err := config.MigrateFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no v3 config at %s", src)
	}
	if err != nil {
		return err
	}
	notes := append(m.Notes, config.RemapLegacyKeys(&m.Config, projects.Names(projects.Discover(m.Config)))...)
	if err := config.Save(dst, m.Config); err != nil {
		return err
	}
	a.eprintf("ac: imported v3 config %s -> %s (the v3 file was left in place)\n", src, dst)
	for _, n := range notes {
		a.eprintf("    - %s\n", n)
	}
	return nil
}
