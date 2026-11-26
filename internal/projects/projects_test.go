package projects

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ericfaris/anycli-launcher/internal/config"
)

func mkdirs(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if err := os.MkdirAll(filepath.Join(root, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func labels(list []Project) []string {
	var out []string
	for _, p := range list {
		out = append(out, p.Label())
	}
	return out
}

func TestDiscoverFixture(t *testing.T) {
	t.Setenv("AC_PROJECTS_DIR", "")
	cfg := config.Default()
	cfg.ProjectsDir = "../../tests/fixtures/projects"
	got := labels(Discover(cfg))
	if !reflect.DeepEqual(got, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDiscoverOrderHiddenFilesExtraDirs(t *testing.T) {
	t.Setenv("AC_PROJECTS_DIR", "")
	root, extra := t.TempDir(), t.TempDir()
	mkdirs(t, root, "Zeta", "alpha", "Beta", ".hidden")
	os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o644)
	mkdirs(t, extra, "aaa")
	cfg := config.Default()
	cfg.ProjectsDir = root
	cfg.ExtraDirs = []string{extra, filepath.Join(root, "missing")}
	got := labels(Discover(cfg))
	// Each root in order; extra dirs after the main root.
	if !reflect.DeepEqual(got, []string{"alpha", "Beta", "Zeta", "aaa"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDiscoverScanDepth2(t *testing.T) {
	t.Setenv("AC_PROJECTS_DIR", "")
	root := t.TempDir()
	mkdirs(t, root, "work/api", "work/web", "personal/api", "repo/.git", "repo/sub", "empty")
	cfg := config.Default()
	cfg.ProjectsDir = root
	cfg.ScanDepth = 2
	got := labels(Discover(cfg))
	want := []string{"empty", "personal/api", "repo", "work/api", "work/web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	cfg.ScanDepth = 1
	if got := labels(Discover(cfg)); !reflect.DeepEqual(got, []string{"empty", "personal", "repo", "work"}) {
		t.Fatalf("depth 1: %v", got)
	}
}

func TestDiscoverAliasAndOverlay(t *testing.T) {
	t.Setenv("AC_PROJECTS_DIR", "")
	root := t.TempDir()
	mkdirs(t, root, "ml-project", "repo-owned", "bad")
	os.WriteFile(filepath.Join(root, "repo-owned", config.OverlayFile), []byte(`alias = "Owned"`+"\n"), 0o644)
	os.WriteFile(filepath.Join(root, "bad", config.OverlayFile), []byte("cli = \"../x\"\n"), 0o644)
	cfg := config.Default()
	cfg.ProjectsDir = root
	cfg.Projects = map[string]config.Project{"ml-project": {Alias: "ML"}}
	list := Discover(cfg)
	if got := labels(list); !reflect.DeepEqual(got, []string{"bad", "ML", "Owned"}) {
		t.Fatalf("got %v", got)
	}
	if list[0].OverlayErr == nil {
		t.Fatal("bad overlay should be reported")
	}
}

func TestResolve(t *testing.T) {
	list := []Project{
		{Name: "my-app", Settings: config.Project{Alias: "ML"}},
		{Name: "my-api"},
		{Name: "API", Group: "work"},
		{Name: "api", Group: "personal"},
		{Name: "beta"},
	}
	names := func(ps []Project) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Label())
		}
		return out
	}
	cases := map[string][]string{
		"beta":         {"beta"},
		"BETA":         {"beta"},
		"ML":           {"ML"},
		"ml":           {"ML"},
		"my":           {"ML", "my-api"},
		"work/API":     {"work/API"},
		"api":          {"personal/api"}, // exact beats case-insensitive
		"Api":          {"work/API", "personal/api"},
		"et":           {"beta"},
		"nothing-here": nil,
		"":             nil,
	}
	for q, want := range cases {
		if got := names(Resolve(list, q)); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", q, got, want)
		}
	}
	if !ExactExists(list, "ML") || !ExactExists(list, "beta") || ExactExists(list, "bet") {
		t.Fatal("ExactExists")
	}
	if !reflect.DeepEqual(Names(list[:2]), []string{"my-app", "my-api"}) {
		t.Fatal("Names")
	}
}

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitStatus(t *testing.T) {
	gitOrSkip(t)
	if (GitStatus{}).String() != "" {
		t.Fatal("empty")
	}
	if s := (GitStatus{Branch: "main", Dirty: true, Ahead: 2, Behind: 1}).String(); s != "main* ↑2 ↓1" {
		t.Fatal(s)
	}

	plain := t.TempDir()
	if Git(plain) != (GitStatus{}) {
		t.Fatal("non-repo must be empty")
	}

	remote := t.TempDir()
	run(t, remote, "init", "-q", "--bare", "-b", "main")
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	if g := Git(repo); g.String() != "main" {
		t.Fatalf("clean: %q", g)
	}
	os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644)
	if g := Git(repo); g.String() != "main*" {
		t.Fatalf("dirty: %q", g)
	}
	run(t, repo, "remote", "add", "origin", remote)
	run(t, repo, "push", "-q", "-u", "origin", "main")
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "two")
	if g := Git(repo); g.String() != "main* ↑1" {
		t.Fatalf("ahead: %q", g)
	}
	all := GitAll([]string{repo, plain})
	if all[repo].Ahead != 1 || all[plain].Branch != "" {
		t.Fatalf("GitAll: %+v", all)
	}
}

func TestResolveFuzzyTier(t *testing.T) {
	list := []Project{{Name: "anycli-launcher", Dir: "/p/a"}, {Name: "ansible", Dir: "/p/b"}, {Name: "beta", Dir: "/p/c"}}
	if got := Resolve(list, "acl"); len(got) != 1 || got[0].Name != "anycli-launcher" {
		t.Fatalf("acl: %v", got)
	}
	// Substring still beats fuzzy: "be" is in beta, and fuzzy-only elsewhere.
	if got := Resolve(list, "be"); len(got) != 1 || got[0].Name != "beta" {
		t.Fatalf("be: %v", got)
	}
	if got := Resolve(list, "zq"); got != nil {
		t.Fatalf("zq: %v", got)
	}
}

// sc builds a scores map keyed the way frecency keys it (cleaned paths).
func sc(kv ...any) map[string]float64 {
	m := map[string]float64{}
	for i := 0; i < len(kv); i += 2 {
		m[filepath.Clean(kv[i].(string))] = kv[i+1].(float64)
	}
	return m
}

func TestResolveRanked(t *testing.T) {
	list := []Project{
		{Name: "my-app", Dir: "/p/my-app"},
		{Name: "my-api", Dir: "/p/my-api"},
		{Name: "API", Group: "work", Dir: "/p/work/API"},
		{Name: "api", Group: "personal", Dir: "/p/personal/api"},
		{Name: "rapid", Dir: "/p/rapid"},
		{Name: "mapper", Dir: "/p/mapper"},
	}
	labels := func(ps []Project) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Label())
		}
		return out
	}
	cases := []struct {
		name   string
		query  string
		scores map[string]float64
		want   []string
	}{
		// Phase 1 behavior without history.
		{"no history ambiguous", "my", nil, []string{"my-app", "my-api"}},
		// Frecency breaks a prefix tie.
		{"prefix tie", "my", sc("/p/my-api", 4.0), []string{"my-api"}},
		// Equal scores stay ambiguous, in list order.
		{"equal scores", "my", sc("/p/my-app", 2.0, "/p/my-api", 2.0), []string{"my-app", "my-api"}},
		// Ambiguous results are ordered by score.
		{"ordered", "pi", sc("/p/rapid", 2.0, "/p/personal/api", 2.0), []string{"personal/api", "rapid", "my-api", "work/API"}},
		// Exact always wins, even against a frecent substring match.
		{"exact wins", "api", sc("/p/rapid", 100.0), []string{"personal/api"}},
		// Case-insensitive exact ties stay ambiguous (no frecency there).
		{"ci exact", "Api", sc("/p/work/API", 100.0), []string{"work/API", "personal/api"}},
		// A prefix match still beats a more frecent substring match.
		{"tier before frecency", "rap", sc("/p/mapper", 50.0), []string{"rapid"}},
		// Substring tie broken by frecency.
		{"substring tie", "pp", sc("/p/mapper", 1.0), []string{"mapper"}},
		// Fuzzy tie broken by frecency.
		{"fuzzy frecency", "mpr", sc("/p/mapper", 0.25), []string{"mapper"}},
	}
	for _, c := range cases {
		if got := labels(ResolveRanked(list, c.query, c.scores)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q got %v want %v", c.name, c.query, got, c.want)
		}
	}
}

func TestSortByFrecency(t *testing.T) {
	list := []Project{{Name: "a", Dir: "/p/a"}, {Name: "b", Dir: "/p/b"}, {Name: "c", Dir: "/p/c"}, {Name: "d", Dir: "/p/d"}}
	got := SortByFrecency(list, sc("/p/c", 8.0, "/p/b", 1.0, "/p/d", 1.0))
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	if !reflect.DeepEqual(names, []string{"c", "b", "d", "a"}) {
		t.Fatalf("got %v", names)
	}
	if list[0].Name != "a" {
		t.Fatal("input must not be reordered")
	}
	if got := SortByFrecency(list, nil); !reflect.DeepEqual(got, list) {
		t.Fatal("no history keeps name order")
	}
}
