package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	cfg, _, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("want defaults, got %+v", cfg)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	in := Default()
	in.CLI = "claude"
	in.DefaultFlags = `--model "opus max"`
	in.ExtraDirs = []string{"~/work"}
	in.ScanDepth = 2
	in.ShowGitStatus = true
	in.Projects = map[string]Project{
		"my-app":  {Alias: "App", Flags: Str("--x"), Color: "#00cc66", Title: `quote " and # hash`, Prompt: "multi\nline"},
		"silent":  {Flags: Str("none")},
		"empty":   {Flags: Str("")},
		"v2.core": {CLI: "codex"},
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, warns, err := Load(path)
	if err != nil || len(warns) > 0 {
		t.Fatalf("load: %v %v", err, warns)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch\n in: %+v\nout: %+v", in, out)
	}
	if e := out.Projects["empty"].Flags; e == nil || *e != "" {
		t.Fatalf("explicit empty flags must survive, got %v", e)
	}
}

func TestLoadUnknownKeysWarn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(path, []byte("cli = \"claude\"\nbogus = 1\n"), 0o644)
	cfg, warns, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLI != "claude" || len(warns) != 1 || !strings.Contains(warns[0], "bogus") {
		t.Fatalf("got %+v %v", cfg, warns)
	}
}

func TestLoadBadTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(path, []byte("cli = \n"), 0o644)
	if _, _, err := Load(path); err == nil {
		t.Fatal("want parse error")
	}
}

func TestNormalize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(path, []byte("scan_depth = 7\n"), 0o644)
	cfg, _, _ := Load(path)
	if cfg.ScanDepth != 1 || cfg.SortOrder != "name" || cfg.Theme != "default" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestResolveFlags(t *testing.T) {
	cases := []struct {
		name, defaults string
		flags          *string
		want           string
	}{
		{"unset", "--d", nil, "--d"},
		{"empty inherits", "--d", Str(""), "--d"},
		{"none suppresses", "--d", Str("none"), ""},
		{"additive", "--d", Str("--p"), "--d --p"},
		{"no defaults", "", Str("--p"), "--p"},
		{"both empty", "", nil, ""},
		{"trimmed none", "--d", Str("  none "), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveFlags(c.defaults, Project{Flags: c.flags}); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestSettingsOverlayAndPrecedence(t *testing.T) {
	cfg := Default()
	cfg.Projects = map[string]Project{"App": {Title: "user title", Flags: Str("--user")}}
	ov := &Project{Alias: "from-repo", Title: "repo title", Prompt: "repo prompt", Flags: Str("--repo")}
	got := cfg.Settings("app", ov) // case-insensitive key match
	want := Project{Alias: "from-repo", Title: "user title", Prompt: "repo prompt", Flags: Str("--user")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if *ov.Flags != "--repo" {
		t.Fatal("overlay must not be mutated")
	}
	if got := cfg.Settings("other", nil); !got.IsZero() {
		t.Fatalf("want zero, got %+v", got)
	}
}

func TestProjectKeyExactBeatsCaseInsensitive(t *testing.T) {
	cfg := Config{Projects: map[string]Project{"app": {Alias: "lower"}, "App": {Alias: "upper"}}}
	if cfg.ProjectKey("App") != "App" || cfg.ProjectKey("app") != "app" {
		t.Fatal("exact key must win")
	}
	if cfg.ProjectKey("missing") != "" {
		t.Fatal("want empty")
	}
}

func TestSetAndRemoveProject(t *testing.T) {
	var cfg Config
	cfg.SetProject("a", Project{Alias: "A"})
	if cfg.Projects["a"].Alias != "A" {
		t.Fatal("set failed")
	}
	cfg.SetProject("A", Project{Alias: "B"}) // replaces case-variant key
	if _, ok := cfg.Projects["a"]; ok || cfg.Projects["A"].Alias != "B" {
		t.Fatalf("got %+v", cfg.Projects)
	}
	cfg.SetProject("A", Project{})
	if len(cfg.Projects) != 0 {
		t.Fatal("zero project should delete")
	}
	cfg.SetProject("x", Project{Color: "#000000"})
	cfg.RemoveProject("X")
	if len(cfg.Projects) != 0 {
		t.Fatal("remove failed")
	}
}

func TestRootsAndEnvOverride(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := Config{ProjectsDir: "~/p", ExtraDirs: []string{"", "/abs/x"}}
	t.Setenv("AC_PROJECTS_DIR", "")
	got := cfg.Roots()
	want := []string{filepath.Join(home, "p"), filepath.Clean("/abs/x")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	env := t.TempDir()
	t.Setenv("AC_PROJECTS_DIR", env)
	if cfg.ProjectsRoot() != env {
		t.Fatalf("env override ignored: %v", cfg.ProjectsRoot())
	}
}

func TestExpandTildify(t *testing.T) {
	home, _ := os.UserHomeDir()
	if ExpandHome("~") != home || ExpandHome("~/a") != filepath.Join(home, "a") || ExpandHome("/x/~") != "/x/~" || ExpandHome("~bob") != "~bob" {
		t.Fatal("ExpandHome")
	}
	if Tildify(filepath.Join(home, "a", "b")) != "~/a/b" || Tildify(home) != "~" {
		t.Fatalf("Tildify: %q", Tildify(filepath.Join(home, "a", "b")))
	}
	if Tildify(home+"x") != home+"x" {
		t.Fatal("Tildify must not match a sibling prefix")
	}
}

func TestPathsFromEnv(t *testing.T) {
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	t.Setenv("AC_CONFIG", "")
	if DefaultPath() != filepath.Join(x, "anycli-launcher", "config.toml") {
		t.Fatal(DefaultPath())
	}
	if LegacyYAMLPath() != filepath.Join(x, "anycli-launcher", "config.yaml") {
		t.Fatal(LegacyYAMLPath())
	}
	t.Setenv("AC_CONFIG", "/custom.toml")
	if DefaultPath() != "/custom.toml" {
		t.Fatal("AC_CONFIG ignored")
	}
}

func TestValidColor(t *testing.T) {
	for s, want := range map[string]bool{"#00cc66": true, "#ABCDEF": true, "00cc66": false, "#00cc6": false, "#00cc6g": false, "": false} {
		if ValidColor(s) != want {
			t.Errorf("%q", s)
		}
	}
}

func TestOverlay(t *testing.T) {
	dir := t.TempDir()
	if p, err := LoadOverlay(dir); p != nil || err != nil {
		t.Fatalf("missing overlay: %v %v", p, err)
	}
	os.WriteFile(filepath.Join(dir, OverlayFile), []byte("alias = \"Repo\"\nflags = \"none\"\ncli = \"claude\"\n"), 0o644)
	p, err := LoadOverlay(dir)
	if err != nil || p.Alias != "Repo" || *p.Flags != "none" || p.CLI != "claude" {
		t.Fatalf("got %+v %v", p, err)
	}
	for _, bad := range []string{`cli = "./evil.sh"`, `cli = "C:\\x\\evil.exe"`, `alias = `} {
		os.WriteFile(filepath.Join(dir, OverlayFile), []byte(bad+"\n"), 0o644)
		if _, err := LoadOverlay(dir); err == nil {
			t.Errorf("want error for %s", bad)
		}
	}
}
