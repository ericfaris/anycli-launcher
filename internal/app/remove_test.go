package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/internal/config"
)

// Regression tests for the v3 3.3.0/3.3.1 removal guards. Each of these
// once deleted (or could delete) the wrong thing.

func TestSafeToRemoveGuards(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "projects")
	extra := filepath.Join(home, "elsewhere")
	sibling := filepath.Join(home, "projects2")
	for _, d := range []string{root, extra, sibling, filepath.Join(root, "app"), filepath.Join(root, "group", "nested"), filepath.Join(sibling, "x"), filepath.Join(home, "outside")} {
		os.MkdirAll(d, 0o755)
	}
	inner := filepath.Join(root, "group", "nested")
	roots := []string{root, extra, inner}

	cases := []struct {
		name, dir string
		ok        bool
	}{
		{"project under root", filepath.Join(root, "app"), true},
		{"empty path", "", false},
		{"missing dir", filepath.Join(root, "ghost"), false},
		{"filesystem root", string(filepath.Separator), false},
		{"home", home, false},
		{"the root itself", root, false},
		{"root with trailing slash", root + string(filepath.Separator), false},
		{"extra root itself", extra, false},
		{"outside every root", filepath.Join(home, "outside"), false},
		{"sibling prefix dir", filepath.Join(sibling, "x"), false},
		{"dot-dot escape", filepath.Join(root, "..", "outside"), false},
		{"contains a configured root", filepath.Join(root, "group"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := SafeToRemove(c.dir, roots, home)
			if (err == nil) != c.ok {
				t.Fatalf("SafeToRemove(%q) = %v, want ok=%v", c.dir, err, c.ok)
			}
		})
	}
	if runtime.GOOS != "windows" {
		if err := SafeToRemove("/", []string{"/"}, home); err == nil {
			t.Fatal("/ must never be removable, even as a root")
		}
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// A project that is a symlink to a folder outside the root may be removed,
// but only the link goes: the target must survive.
func TestRemoveSymlinkProjectKeepsTarget(t *testing.T) {
	h := newHarness(t)
	target := filepath.Join(h.home, "real-code")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "precious.txt"), []byte("x"), 0o644)
	symlinkOrSkip(t, target, filepath.Join(h.root, "linked"))
	h.input("y\n")
	if code := h.run("remove", "linked"); code != 0 {
		t.Fatalf("code %d: %s %s", code, h.out.String(), h.err.String())
	}
	if _, err := os.Lstat(filepath.Join(h.root, "linked")); !os.IsNotExist(err) {
		t.Fatal("link should be gone")
	}
	if _, err := os.Stat(filepath.Join(target, "precious.txt")); err != nil {
		t.Fatal("symlink target was deleted")
	}
}

// A symlinked root must not make the root itself removable.
func TestSymlinkedRootNotRemovable(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real-root")
	os.MkdirAll(filepath.Join(real, "p"), 0o755)
	link := filepath.Join(home, "projects")
	symlinkOrSkip(t, real, link)
	if err := SafeToRemove(real, []string{link}, home); err == nil {
		t.Fatal("resolved root must be refused")
	}
	if err := SafeToRemove(filepath.Join(link, "p"), []string{link}, home); err != nil {
		t.Fatalf("project under symlinked root: %v", err)
	}
}

func TestRemoveDeclineKeepsEverything(t *testing.T) {
	h := newHarness(t)
	h.mkproj("app")
	for _, in := range []string{"\n", "n\n", "", "yes please\n"} {
		h.input(in)
		if h.run("remove", "app") != 1 {
			t.Fatalf("input %q should cancel", in)
		}
		if _, err := os.Stat(filepath.Join(h.root, "app")); err != nil {
			t.Fatalf("input %q deleted the folder", in)
		}
	}
}

func TestRemoveCleanFolderDropsConfig(t *testing.T) {
	h := newHarness(t)
	h.mkproj("app", "other")
	h.writeConfig(func(c *config.Config) {
		c.Projects = map[string]config.Project{"app": {Alias: "A"}, "other": {Alias: "O"}}
	})
	h.input("y\n")
	if h.run("remove", "app") != 0 {
		t.Fatal(h.err.String())
	}
	if _, err := os.Stat(filepath.Join(h.root, "app")); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
	c := h.loadConfig()
	if _, ok := c.Projects["app"]; ok || c.Projects["other"].Alias != "O" {
		t.Fatalf("config: %+v", c.Projects)
	}
	if len(h.ghCalls) != 0 {
		t.Fatal("gh must not be called for a non-repo")
	}
}

func TestRemoveRequiresExactName(t *testing.T) {
	h := newHarness(t)
	h.mkproj("my-application")
	h.input("y\n")
	if h.run("remove", "my-app") != 1 || !strings.Contains(h.err.String(), "exact name") {
		t.Fatal("fuzzy match must not be accepted for remove")
	}
	if _, err := os.Stat(filepath.Join(h.root, "my-application")); err != nil {
		t.Fatal("deleted on a fuzzy match")
	}
}

// v3 3.3.1: work/api vs personal/api deleted the wrong folder.
func TestRemoveBasenameCollision(t *testing.T) {
	h := newHarness(t)
	h.mkproj("work/api", "personal/api")
	h.writeConfig(func(c *config.Config) { c.ScanDepth = 2 })
	if h.run("remove", "api") != 1 || !strings.Contains(h.err.String(), "more than one") {
		t.Fatal("ambiguous basename must be refused")
	}
	h.input("y\n")
	if h.run("remove", "personal/api") != 0 {
		t.Fatal(h.err.String())
	}
	if _, err := os.Stat(filepath.Join(h.root, "work", "api")); err != nil {
		t.Fatal("wrong folder removed")
	}
	if _, err := os.Stat(filepath.Join(h.root, "personal", "api")); !os.IsNotExist(err) {
		t.Fatal("chosen folder not removed")
	}
}

func TestRemoveUnsavedWorkNeedsTypedName(t *testing.T) {
	h := newHarness(t)
	h.mkproj("app")
	dir := filepath.Join(h.root, "app")
	gitInit(t, dir) // a commit on no remote = unsaved
	h.input("y\n")
	if h.run("remove", "app") != 1 {
		t.Fatal("plain y must not be enough with unsaved work")
	}
	if !strings.Contains(h.out.String(), "commits not pushed") {
		t.Fatal(h.out.String())
	}
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	h.input("app\n")
	h.run("remove", "app")
	if !strings.Contains(h.out.String(), "uncommitted changes and commits not pushed") {
		t.Fatal(h.out.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("typed name should confirm")
	}
}

func TestRemoveRepoWithoutGitCountsAsUnsaved(t *testing.T) {
	h := newHarness(t)
	h.mkproj("app/.git")
	h.onPath["git"] = false
	h.input("y\n")
	if h.run("remove", "app") != 1 || !strings.Contains(h.out.String(), "could not be checked") {
		t.Fatal(h.out.String())
	}
}

func setupGitHubRepo(t *testing.T, h *harness, url string) string {
	h.mkproj("app")
	dir := filepath.Join(h.root, "app")
	gitInit(t, dir)
	gitRun(t, dir, "remote", "add", "origin", url)
	// Pretend main is pushed so the work counts as saved (plain y/N confirm).
	gitRun(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

func TestRemoveGitHubVerifiesGhBeforeDeleting(t *testing.T) {
	cases := []struct {
		name    string
		ghPath  bool
		ghAuth  bool
		wantMsg string
	}{
		{"gh missing", false, true, "gh is not installed"},
		{"gh unauthenticated", true, false, "not authenticated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			dir := setupGitHubRepo(t, h, "git@github.com:me/app.git")
			h.onPath["gh"], h.ghAuthOK = c.ghPath, c.ghAuth
			h.input("y\ny\n")
			if h.run("remove", "app") != 1 || !strings.Contains(h.out.String(), c.wantMsg) {
				t.Fatalf("%s", h.out.String())
			}
			if _, err := os.Stat(dir); err != nil {
				t.Fatal("folder deleted although gh could not delete the repo")
			}
			for _, call := range h.ghCalls {
				if call[0] == "repo" {
					t.Fatal("repo delete attempted")
				}
			}
		})
	}
}

func TestRemoveGitHubDeletesRepoAfterFolder(t *testing.T) {
	h := newHarness(t)
	dir := setupGitHubRepo(t, h, "https://github.com/me/app.git")
	h.input("y\ny\n")
	if h.run("remove", "app") != 0 {
		t.Fatal(h.out.String(), h.err.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("folder not removed")
	}
	last := h.ghCalls[len(h.ghCalls)-1]
	if strings.Join(last, " ") != "repo delete me/app --yes" {
		t.Fatalf("gh calls %v", h.ghCalls)
	}
}

func TestRemoveGitHubDeclineKeepsRepo(t *testing.T) {
	h := newHarness(t)
	setupGitHubRepo(t, h, "git@github.com:me/app.git")
	h.input("y\nn\n")
	if h.run("remove", "app") != 0 {
		t.Fatal(h.out.String())
	}
	if len(h.ghCalls) != 0 {
		t.Fatalf("gh called: %v", h.ghCalls)
	}
}

func TestRemoveGitHubDeleteFailureReported(t *testing.T) {
	h := newHarness(t)
	setupGitHubRepo(t, h, "git@github.com:me/app.git")
	h.ghFail = true
	h.input("y\ny\n")
	if h.run("remove", "app") != 1 || !strings.Contains(h.out.String(), "Failed to delete GitHub repo") {
		t.Fatal(h.out.String())
	}
}

func TestRemoveNonGitHubRemoteNeverAsks(t *testing.T) {
	h := newHarness(t)
	setupGitHubRepo(t, h, "git@gitlab.com:me/app.git")
	h.input("y\n")
	if h.run("remove", "app") != 0 || strings.Contains(h.out.String(), "GitHub remote detected") {
		t.Fatal(h.out.String())
	}
	if len(h.ghCalls) != 0 {
		t.Fatal("gh called for a GitLab remote")
	}
}

func TestGitHubSlug(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:me/app.git":       "me/app",
		"git@github.com:me/app":           "me/app",
		"ssh://git@github.com/me/app.git": "me/app",
		"https://github.com/me/app.git":   "me/app",
		"https://github.com/me/app/":      "me/app",
		"http://github.com/me/app":        "me/app",
		"https://gitlab.com/me/app.git":   "",
		"git@github.example.com:me/app":   "",
		"https://github.com/me":           "",
		"https://github.com/me/app/extra": "",
		"":                                "",
	} {
		if got := GitHubSlug(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
