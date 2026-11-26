package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Migration is the result of importing a v3 config.
type Migration struct {
	Config Config
	// Notes describe values that were dropped or changed.
	Notes []string
}

// LegacyYAMLPath is v3's config.yaml next to config.toml.
func LegacyYAMLPath() string { return filepath.Join(Dir(), "config.yaml") }

// LegacyFlatPath is the pre-YAML key=value config v3 still auto-migrated.
func LegacyFlatPath() string { return filepath.Join(Dir(), "config") }

// MigrateFile imports a v3 config.yaml or legacy flat config, detecting the
// format from the content.
func MigrateFile(path string) (Migration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Migration{}, err
	}
	if looksFlat(string(data)) {
		return ParseV3Flat(strings.NewReader(string(data)))
	}
	return ParseV3YAML(strings.NewReader(string(data)))
}

var flatKeyRe = regexp.MustCompile(`^\s*(PROJECTS_DIR|CLI_FLAGS|KIRO_FLAGS)(_[^=]*)?=`)

func looksFlat(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if flatKeyRe.MatchString(line) {
			return true
		}
	}
	return false
}

// ParseV3Flat parses the oldest KEY=value config (PROJECTS_DIR, CLI_FLAGS or
// KIRO_FLAGS, and per-project CLI_FLAGS_<name>/KIRO_FLAGS_<name>).
func ParseV3Flat(r io.Reader) (Migration, error) {
	m := Migration{Config: Default()}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch {
		case key == "PROJECTS_DIR":
			m.Config.ProjectsDir = val
		case key == "CLI_FLAGS" || key == "KIRO_FLAGS":
			m.Config.DefaultFlags = val
		case strings.HasPrefix(key, "CLI_FLAGS_") || strings.HasPrefix(key, "KIRO_FLAGS_"):
			name := key[strings.Index(key, "FLAGS_")+len("FLAGS_"):]
			p := m.Config.Projects[name]
			p.Flags = Str(val)
			m.Config.SetProject(name, p)
		default:
			m.Notes = append(m.Notes, fmt.Sprintf("dropped unknown key %s", key))
		}
	}
	if err := sc.Err(); err != nil {
		return m, err
	}
	m.Notes = append(m.Notes, "legacy flat config: no cli set; run `ac setup` to choose one")
	return m, nil
}

var (
	reTop      = regexp.MustCompile(`^([a-z_]+):\s+(.*)$`)
	reSection  = regexp.MustCompile(`^([a-z_]+):\s*$`)
	reQuoted   = regexp.MustCompile(`^ {2}("(?:[^"\\]|\\.)*"):\s*$`)
	reBare     = regexp.MustCompile(`^ {2}(\S[^:]*):\s*$`)
	reListItem = regexp.MustCompile(`^ {2}-\s+(.*)$`)
	reProp     = regexp.MustCompile(`^ {4}([a-z_]+):\s+(.*)$`)
)

// ParseV3YAML parses v3's config.yaml. v3 wrote (and read) a fixed YAML
// subset with its own line parser; this mirrors that parser rather than
// using a general YAML library so hand-edited v3 files import the same way
// v3 read them.
func ParseV3YAML(r io.Reader) (Migration, error) {
	m := Migration{Config: Default()}
	c := &m.Config
	c.Projects = map[string]Project{}
	section, project := "", ""
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if g := reTop.FindStringSubmatch(line); g != nil {
			section, project = "", ""
			v := yamlScalar(g[2])
			switch g[1] {
			case "projects_dir":
				c.ProjectsDir = v
			case "default_flags":
				c.DefaultFlags = v
			case "cli_tool":
				c.CLI = v
			case "cli_launch":
				c.CLILaunch = v
			case "cli_config_dir":
				c.CLIConfigDir = v
			case "sort_order":
				c.SortOrder = v
				if v == "recent" {
					m.Notes = append(m.Notes, "sort_order \"recent\" kept; v4 ranks by launch frecency, and that history starts empty, so the list is in name order until you launch projects with v4")
				}
			case "scan_depth":
				if n, err := strconv.Atoi(v); err == nil {
					c.ScanDepth = n
				}
			case "show_git_status":
				c.ShowGitStatus = v == "true"
			case "theme":
				if t := strings.ToLower(v); isPreset(t) {
					c.Theme = t
				} else if v != "" && v != "Default" {
					m.Notes = append(m.Notes, fmt.Sprintf("theme %q is a v3 terminal color scheme; v4 uses presets (default, mono, dusk), set to default", v))
				}
			case "show_sessions":
				if v == "true" {
					m.Notes = append(m.Notes, "dropped show_sessions (session listing returns in a later v4 release)")
				}
			case "auto_update_check":
				// v4 has no self-update; nothing to carry over.
			default:
				m.Notes = append(m.Notes, fmt.Sprintf("dropped unknown key %s", g[1]))
			}
			continue
		}
		if g := reSection.FindStringSubmatch(line); g != nil {
			section, project = g[1], ""
			if section == "tracked_apps" {
				m.Notes = append(m.Notes, "dropped tracked_apps (self-update/version tracking is not in v4)")
			}
			continue
		}
		if g := reQuoted.FindStringSubmatch(line); g != nil {
			project = projectHeader(section, yamlScalar(g[1]))
			continue
		}
		if g := reBare.FindStringSubmatch(line); g != nil && !strings.HasPrefix(strings.TrimSpace(line), "-") {
			project = projectHeader(section, yamlScalar(g[1]))
			continue
		}
		if g := reListItem.FindStringSubmatch(line); g != nil {
			if section == "extra_dirs" {
				if v := yamlScalar(g[1]); v != "" {
					c.ExtraDirs = append(c.ExtraDirs, v)
				}
			}
			continue
		}
		if g := reProp.FindStringSubmatch(line); g != nil && section == "projects" && project != "" {
			p := c.Projects[project]
			v := yamlScalar(g[2])
			switch g[1] {
			case "flags":
				p.Flags = Str(v)
			case "color":
				p.Color = v
			case "title":
				p.Title = v
			case "prompt":
				p.Prompt = v
			case "alias":
				p.Alias = v
			}
			c.Projects[project] = p
		}
	}
	if err := sc.Err(); err != nil {
		return m, err
	}
	// Drop entries with nothing set (v3 wrote bare headers for registered names).
	for k, p := range c.Projects {
		if p.IsZero() {
			delete(c.Projects, k)
		}
	}
	c.normalize()
	return m, nil
}

func projectHeader(section, name string) string {
	if section != "projects" {
		return ""
	}
	return name
}

func isPreset(t string) bool { return t == "default" || t == "mono" || t == "dusk" }

// yamlScalar unquotes a v3 scalar: double quotes with \" and \\ escapes,
// single quotes with ” escapes, or a bare value (trailing " # comment"
// stripped, which v3 itself failed to do).
func yamlScalar(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		var b strings.Builder
		in := v[1 : len(v)-1]
		for i := 0; i < len(in); i++ {
			if in[i] == '\\' && i+1 < len(in) && (in[i+1] == '"' || in[i+1] == '\\') {
				i++
			}
			b.WriteByte(in[i])
		}
		return b.String()
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// RemapLegacyKeys repairs per-project keys written by old v3 versions that
// turned "-" into "_" (e.g. "public_knowledge_network" for the directory
// "public-knowledge-network"). For each key that matches no directory name,
// if exactly one directory matches the hyphen->underscore form, the entry is
// moved onto that name, never overwriting an existing entry. Returns notes.
func RemapLegacyKeys(c *Config, dirNames []string) []string {
	have := map[string]bool{}
	for _, n := range dirNames {
		have[n] = true
	}
	var keys []string
	for k := range c.Projects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var notes []string
	for _, k := range keys {
		if have[k] {
			continue
		}
		var match []string
		for _, n := range dirNames {
			if strings.Contains(n, "-") && strings.ReplaceAll(n, "-", "_") == k {
				match = append(match, n)
			}
		}
		if len(match) != 1 {
			continue
		}
		if _, taken := c.Projects[match[0]]; taken {
			continue
		}
		c.Projects[match[0]] = c.Projects[k]
		delete(c.Projects, k)
		notes = append(notes, fmt.Sprintf("renamed legacy project key %q to %q", k, match[0]))
	}
	return notes
}
