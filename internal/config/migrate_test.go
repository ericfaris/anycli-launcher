package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtures = "../../tests/fixtures"

func TestMigrateV3YAMLFixture(t *testing.T) {
	m, err := MigrateFile(filepath.Join(fixtures, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c := m.Config
	if c.ProjectsDir != "~/dev" || c.DefaultFlags != "--trust-all-tools" || c.CLI != "kiro-cli" || c.CLILaunch != "chat" {
		t.Fatalf("top-level: %+v", c)
	}
	want := map[string]Project{
		"beta":       {Flags: Str("--no-interactive")},
		"my-app":     {Flags: Str("--agent mechanic"), Title: "My App"},
		"v2.core":    {Flags: Str("--no-interactive")},
		"ml-project": {Flags: Str("--model=claude --effort=high"), Color: "#00cc66", Title: "ML Project", Prompt: "help me with ML", Alias: "ML"},
		"silent":     {Flags: Str("none")},
		"spacey":     {Flags: Str("--effort high   --no-interactive")},
		"prompted":   {Prompt: "help me with this"},
	}
	if !reflect.DeepEqual(c.Projects, want) {
		t.Fatalf("projects:\n got %+v\nwant %+v", c.Projects, want)
	}
	// Flag semantics survive the import.
	if ResolveFlags(c.DefaultFlags, c.Projects["silent"]) != "" {
		t.Fatal("none must suppress defaults")
	}
	if ResolveFlags(c.DefaultFlags, c.Projects["beta"]) != "--trust-all-tools --no-interactive" {
		t.Fatal("additive flags")
	}
}

func TestMigrateFlatFixtures(t *testing.T) {
	cases := map[string]map[string]Project{
		"config":            {"beta": {Flags: Str("--trust-all-tools --no-interactive")}},
		"config-no-newline": {"beta": {Flags: Str("--no-interactive")}},
		"config-edge-cases": {
			"my-app":     {Flags: Str("--agent mechanic")},
			"v2.core":    {Flags: Str("--no-interactive")},
			"ml-project": {Flags: Str("--model=claude --effort=high")},
			"silent":     {Flags: Str("")},
			"spacey":     {Flags: Str("--trust-all-tools   --no-interactive")},
		},
	}
	for file, want := range cases {
		t.Run(file, func(t *testing.T) {
			m, err := MigrateFile(filepath.Join(fixtures, file))
			if err != nil {
				t.Fatal(err)
			}
			if m.Config.ProjectsDir != "~/dev" || m.Config.DefaultFlags != "--trust-all-tools" {
				t.Fatalf("top-level: %+v", m.Config)
			}
			if !reflect.DeepEqual(m.Config.Projects, want) {
				t.Fatalf("got %+v want %+v", m.Config.Projects, want)
			}
			// Explicit empty override inherits defaults (v3 _ac_get_flags).
			if p, ok := m.Config.Projects["silent"]; ok && ResolveFlags(m.Config.DefaultFlags, p) != "--trust-all-tools" {
				t.Fatal("empty override should inherit defaults")
			}
		})
	}
}

func TestParseV3YAMLQuotingAndSections(t *testing.T) {
	in := `projects_dir: "~/code"
default_flags: ""
cli_tool: "claude"
cli_launch: ""
cli_config_dir: '~/.claude'
auto_update_check: "true"
theme: "Dracula"
sort_order: recent          # comment v3 kept by mistake
scan_depth: "2"
show_git_status: "true"
show_sessions: "true"
mystery: x
extra_dirs:
  - "~/work"
  - /opt/src

projects:
  "quoted \"name\"":
    title: "has # hash and \\ slash"
  bare-name:
    prompt: 'it''s fine'
  header-only:

tracked_apps:
  "launcher":
    path: "~/projects/x"
    flags: "must not leak into projects"
`
	m, err := ParseV3YAML(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	c := m.Config
	if c.ProjectsDir != "~/code" || c.CLI != "claude" || c.CLIConfigDir != "~/.claude" || c.SortOrder != "recent" || c.ScanDepth != 2 || !c.ShowGitStatus || c.Theme != "default" {
		t.Fatalf("top-level: %+v", c)
	}
	if !reflect.DeepEqual(c.ExtraDirs, []string{"~/work", "/opt/src"}) {
		t.Fatalf("extra_dirs: %v", c.ExtraDirs)
	}
	want := map[string]Project{
		`quoted "name"`: {Title: `has # hash and \ slash`},
		"bare-name":     {Prompt: "it's fine"},
	}
	if !reflect.DeepEqual(c.Projects, want) {
		t.Fatalf("projects: %+v", c.Projects)
	}
	notes := strings.Join(m.Notes, "\n")
	for _, s := range []string{"Dracula", "show_sessions", "tracked_apps", "mystery", "recent"} {
		if !strings.Contains(notes, s) {
			t.Errorf("notes missing %q:\n%s", s, notes)
		}
	}
}

func TestParseV3YAMLCRLF(t *testing.T) {
	m, err := ParseV3YAML(strings.NewReader("cli_tool: claude\r\nprojects:\r\n  a:\r\n    alias: A\r\n"))
	if err != nil || m.Config.CLI != "claude" || m.Config.Projects["a"].Alias != "A" {
		t.Fatalf("%+v %v", m.Config, err)
	}
}

func TestRemapLegacyKeys(t *testing.T) {
	c := Config{Projects: map[string]Project{
		"public_knowledge_network": {Alias: "PKN"},
		"my_app":                   {Alias: "legacy"},
		"keep_me":                  {Alias: "no dir"},
		"other_thing":              {Alias: "stale"},
		"other-thing":              {Alias: "current"},
	}}
	dirs := []string{"public-knowledge-network", "my_app", "my-app", "other-thing"}
	notes := RemapLegacyKeys(&c, dirs)
	if c.Projects["public-knowledge-network"].Alias != "PKN" {
		t.Fatal("legacy key not remapped")
	}
	// A real my_app dir keeps its own key; my-app must not steal it.
	if c.Projects["my_app"].Alias != "legacy" {
		t.Fatal("collision: real my_app key moved")
	}
	if _, ok := c.Projects["my-app"]; ok {
		t.Fatal("my-app must not inherit my_app's settings")
	}
	if c.Projects["other-thing"].Alias != "current" {
		t.Fatal("existing entry overwritten")
	}
	if _, ok := c.Projects["keep_me"]; !ok {
		t.Fatal("unmatched key dropped")
	}
	if len(notes) != 1 {
		t.Fatalf("notes: %v", notes)
	}
}

func TestYAMLScalar(t *testing.T) {
	for in, want := range map[string]string{
		`"a\"b"`:     `a"b`,
		`"a\\b"`:     `a\b`,
		`"a\nb"`:     `a\nb`,
		`'x''y'`:     `x'y`,
		`bare # c`:   `bare`,
		`"q # not"`:  `q # not`,
		`a#b`:        `a#b`,
		`  spaced  `: `spaced`,
		`"`:          `"`,
	} {
		if got := yamlScalar(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}
