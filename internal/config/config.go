// Package config loads and saves the anycli-launcher v4 configuration
// (~/.config/anycli-launcher/config.toml), applies per-project .ac.toml
// overlays, and imports v3 configs (see migrate.go).
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Project holds per-project settings, keyed by directory basename in
// Config.Projects and also readable from a project's own .ac.toml.
type Project struct {
	Alias string `toml:"alias,omitempty"`
	// Flags is a pointer so "set to empty" (inherit defaults only) is
	// distinguishable from "not set". The literal "none" suppresses
	// default_flags entirely (v3 semantics).
	Flags  *string `toml:"flags,omitempty"`
	Color  string  `toml:"color,omitempty"`
	Title  string  `toml:"title,omitempty"`
	Prompt string  `toml:"prompt,omitempty"`
	// CLI overrides the global cli for this project.
	CLI string `toml:"cli,omitempty"`
	// Backend overrides the global backend for this project. Only the names
	// in Backends are accepted (an .ac.toml may pick one, never a command).
	Backend string `toml:"backend,omitempty"`
}

// IsZero reports whether no field is set.
func (p Project) IsZero() bool {
	return p.Alias == "" && p.Flags == nil && p.Color == "" && p.Title == "" && p.Prompt == "" && p.CLI == "" && p.Backend == ""
}

// Config is the on-disk v4 configuration.
type Config struct {
	ProjectsDir   string   `toml:"projects_dir"`
	ExtraDirs     []string `toml:"extra_dirs,omitempty"`
	ScanDepth     int      `toml:"scan_depth,omitempty"`
	CLI           string   `toml:"cli"`
	CLILaunch     string   `toml:"cli_launch,omitempty"`
	CLIConfigDir  string   `toml:"cli_config_dir,omitempty"`
	DefaultFlags  string   `toml:"default_flags,omitempty"`
	SortOrder     string   `toml:"sort_order,omitempty"` // name | recent (frecent)
	ShowGitStatus bool     `toml:"show_git_status"`
	Theme         string   `toml:"theme,omitempty"`
	// Backend is how a launch runs: auto (default), here, tab, tmux, zellij.
	Backend string `toml:"backend,omitempty"`
	// Terminal forces the tab terminal instead of detecting it.
	Terminal string             `toml:"terminal,omitempty"`
	Projects map[string]Project `toml:"projects,omitempty"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		ProjectsDir: "~/projects",
		ScanDepth:   1,
		SortOrder:   "name",
		Theme:       "default",
	}
}

// Dir returns the config directory: $XDG_CONFIG_HOME/anycli-launcher, else
// ~/.config/anycli-launcher (on every OS, matching v3).
func Dir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "anycli-launcher")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "anycli-launcher")
}

// DefaultPath is the config.toml path, overridable with $AC_CONFIG.
func DefaultPath() string {
	if p := os.Getenv("AC_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Dir(), "config.toml")
}

// Load reads a config file. A missing file yields Default() and an error
// satisfying errors.Is(err, os.ErrNotExist). Unknown keys are returned as
// warnings rather than failing the load.
func Load(path string) (Config, []string, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, nil, err
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Default(), nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var warns []string
	for _, k := range md.Undecoded() {
		warns = append(warns, fmt.Sprintf("unknown config key %q", k.String()))
	}
	cfg.normalize()
	return cfg, warns, nil
}

func (c *Config) normalize() {
	if c.ScanDepth != 2 {
		c.ScanDepth = 1
	}
	if c.SortOrder == "" {
		c.SortOrder = "name"
	}
	if c.Theme == "" {
		c.Theme = "default"
	}
}

// Save writes the config atomically (temp file + rename), creating the
// directory if needed.
func Save(path string, cfg Config) error {
	var buf bytes.Buffer
	buf.WriteString("# anycli-launcher v4 config. See README for every key.\n")
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Exists reports whether a regular file exists at path.
func Exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// ExpandHome expands a leading "~" (or "~/", "~\") to the user's home.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[1:])
	}
	return p
}

// Tildify replaces a leading home directory with "~" for storage/display.
func Tildify(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + filepath.ToSlash(p[len(home):])
	}
	return p
}

// Roots returns the expanded project roots: projects_dir, then extra_dirs.
// $AC_PROJECTS_DIR overrides projects_dir (v3 parity).
func (c Config) Roots() []string {
	main := c.ProjectsDir
	if env := os.Getenv("AC_PROJECTS_DIR"); env != "" {
		main = env
	}
	roots := []string{filepath.Clean(ExpandHome(main))}
	for _, d := range c.ExtraDirs {
		if d != "" {
			roots = append(roots, filepath.Clean(ExpandHome(d)))
		}
	}
	return roots
}

// ProjectsRoot is the primary root (where `ac new` creates projects).
func (c Config) ProjectsRoot() string { return c.Roots()[0] }

// ProjectKey finds the Projects map key for a directory name: exact first,
// then case-insensitive. Returns "" when nothing matches.
func (c Config) ProjectKey(name string) string {
	if _, ok := c.Projects[name]; ok {
		return name
	}
	for k := range c.Projects {
		if strings.EqualFold(k, name) {
			return k
		}
	}
	return ""
}

// Settings returns the effective settings for a project: the .ac.toml
// overlay (may be nil) provides a base, and the user's config.toml entry
// overrides it field by field. The user's own config wins because the
// overlay is typically committed to the repo and shared.
func (c Config) Settings(name string, overlay *Project) Project {
	var out Project
	if overlay != nil {
		out = *overlay
	}
	if k := c.ProjectKey(name); k != "" {
		p := c.Projects[k]
		if p.Alias != "" {
			out.Alias = p.Alias
		}
		if p.Flags != nil {
			f := *p.Flags
			out.Flags = &f
		}
		if p.Color != "" {
			out.Color = p.Color
		}
		if p.Title != "" {
			out.Title = p.Title
		}
		if p.Prompt != "" {
			out.Prompt = p.Prompt
		}
		if p.CLI != "" {
			out.CLI = p.CLI
		}
		if p.Backend != "" {
			out.Backend = p.Backend
		}
	}
	return out
}

// SetProject stores settings for name, deleting the entry when empty.
func (c *Config) SetProject(name string, p Project) {
	if k := c.ProjectKey(name); k != "" && k != name {
		delete(c.Projects, k)
	}
	if p.IsZero() {
		delete(c.Projects, name)
		return
	}
	if c.Projects == nil {
		c.Projects = map[string]Project{}
	}
	c.Projects[name] = p
}

// RemoveProject drops any settings stored for name (exact or case-insensitive).
func (c *Config) RemoveProject(name string) {
	if k := c.ProjectKey(name); k != "" {
		delete(c.Projects, k)
	}
}

// ResolveFlags combines default flags with a project's flags (v3 semantics):
// unset or empty -> defaults; "none" -> nothing; otherwise defaults + project.
func ResolveFlags(defaults string, p Project) string {
	if p.Flags == nil {
		return defaults
	}
	v := strings.TrimSpace(*p.Flags)
	switch {
	case v == "none":
		return ""
	case v == "":
		return defaults
	case defaults == "":
		return v
	default:
		return defaults + " " + v
	}
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidColor reports whether s is a #rrggbb hex color.
func ValidColor(s string) bool { return colorRe.MatchString(s) }

// SortOrders are the accepted sort_order values. "recent" and "frecent"
// are the same: by launch frecency, ties in name order.
var SortOrders = []string{"name", "recent", "frecent"}

// FrecencySort reports whether a sort_order value means "by frecency".
func FrecencySort(order string) bool { return order == "recent" || order == "frecent" }

// ValidSortOrder reports whether order is one of SortOrders.
func ValidSortOrder(order string) bool {
	for _, o := range SortOrders {
		if o == order {
			return true
		}
	}
	return false
}

// Backends are the accepted backend names (launch.Backends; a test keeps
// the two in sync). "" means auto.
var Backends = []string{"auto", "here", "tab", "tmux", "zellij"}

// ValidBackend reports whether s is "" or one of Backends.
func ValidBackend(s string) bool {
	if s == "" {
		return true
	}
	for _, b := range Backends {
		if b == s {
			return true
		}
	}
	return false
}

// Str returns a pointer to s (helper for Project.Flags).
func Str(s string) *string { return &s }
