package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// gitEnv isolates git from the runner's config and gives it an identity.
func gitEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(cfg, []byte("[user]\n\tname = t\n\temail = t@t\n[commit]\n\tgpgsign = false\n"), 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := ExecGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func newRepo(t *testing.T) string {
	t.Helper()
	gitEnv(t)
	repo := filepath.Join(t.TempDir(), "repo with space")
	os.MkdirAll(repo, 0o755)
	mustGit(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
	mustGit(t, repo, "add", "a.txt")
	mustGit(t, repo, "commit", "-q", "-m", "init")
	return repo
}

func TestParseList(t *testing.T) {
	out := "worktree /r\nHEAD abc\nbranch refs/heads/main\n\nworktree /w/x\nHEAD def\nbranch refs/heads/ac/x\n\nworktree /w/d\nHEAD 123\ndetached\n"
	got := ParseList(out)
	want := []Entry{
		{Path: filepath.Clean(filepath.FromSlash("/r")), Head: "abc", Branch: "main", Main: true},
		{Path: filepath.Clean(filepath.FromSlash("/w/x")), Head: "def", Branch: "ac/x"},
		{Path: filepath.Clean(filepath.FromSlash("/w/d")), Head: "123"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if len(ParseList("")) != 0 {
		t.Fatal("empty")
	}
}

func TestVersion(t *testing.T) {
	for out, want := range map[string][2]int{
		"git version 2.43.0":                 {2, 43},
		"git version 2.39.3 (Apple Git-145)": {2, 39},
		"git version 2.42.0.windows.2":       {2, 42},
		"git version 2.16.1\n":               {2, 16},
	} {
		ma, mi, ok := Version(out)
		if !ok || [2]int{ma, mi} != want {
			t.Errorf("%q: %d.%d %v", out, ma, mi, ok)
		}
	}
	for _, bad := range []string{"", "hub version 2", "git version x.y"} {
		if _, _, ok := Version(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if VersionOK(2, 16) || !VersionOK(2, 17) || !VersionOK(3, 0) || VersionOK(1, 99) {
		t.Fatal("VersionOK")
	}
}

// A real worktree lifecycle in a temp repo: add, status (dirty, ahead,
// unpushed), find via SameFile, guarded remove, branch delete, prune.
func TestGitLifecycle(t *testing.T) {
	repo := newRepo(t)
	g := Git{}
	top, err := g.TopLevel(filepath.Join(repo))
	if err != nil || !sameOrUnder(top, repo) || !sameOrUnder(repo, top) {
		t.Fatalf("toplevel %q %v", top, err)
	}
	if _, err := g.TopLevel(t.TempDir()); err == nil {
		t.Fatal("non-repo has no toplevel")
	}
	base, err := g.ResolveBase(repo, "")
	if err != nil || !ValidSHA(base) {
		t.Fatalf("base %q %v", base, err)
	}
	if b2, err := g.ResolveBase(repo, "main"); err != nil || b2 != base {
		t.Fatal("main")
	}
	for _, bad := range []string{"--output=/tmp/x", "-h", "nope", "main\nHEAD", "HEAD~5"} {
		if _, err := g.ResolveBase(repo, bad); err == nil {
			t.Errorf("base %q accepted", bad)
		}
	}

	root := filepath.Join(t.TempDir(), "wt")
	path := filepath.Join(root, "repo", "fix")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if g.BranchExists(repo, "ac/fix") {
		t.Fatal("branch exists early")
	}
	if err := g.Add(repo, "ac/fix", path, base); err != nil {
		t.Fatal(err)
	}
	if !g.BranchExists(repo, "ac/fix") || g.BranchExists(repo, "ac/other") {
		t.Fatal("BranchExists")
	}
	if err := g.Add(repo, "main", path+"2", base); err == nil {
		t.Fatal("non-task branch accepted")
	}
	if err := g.Add(repo, "ac/fix2", path+"2", "HEAD"); err == nil {
		t.Fatal("non-sha base accepted")
	}
	e, ok, err := g.Find(repo, path)
	if err != nil || !ok || e.Branch != "ac/fix" || e.Main {
		t.Fatalf("find %+v %v %v", e, ok, err)
	}
	if e, ok, _ := g.Find(repo, repo); !ok || !e.Main {
		t.Fatal("main worktree")
	}
	if _, ok, _ := g.Find(repo, t.TempDir()); ok {
		t.Fatal("unrelated dir found")
	}

	if s := g.Status(path, base); s.Err != nil || s.Dirty || s.Ahead != 0 || s.Unpushed != 0 || s.Branch != "ac/fix" {
		t.Fatalf("clean: %+v", s)
	}
	os.WriteFile(filepath.Join(path, "new.txt"), []byte("x"), 0o644)
	if s := g.Status(path, base); !s.Dirty || s.String() != "ac/fix*" {
		t.Fatalf("untracked: %+v %q", s, s.String())
	}
	mustGit(t, path, "add", "new.txt")
	mustGit(t, path, "commit", "-q", "-m", "work")
	s := g.Status(path, base)
	if s.Dirty || s.Ahead != 1 || s.Unpushed != 1 || s.String() != "ac/fix ↑1 (1 unpushed)" {
		t.Fatalf("committed: %+v %q", s, s.String())
	}
	// Push to a bare remote: still ahead of base, nothing unpushed.
	remote := filepath.Join(t.TempDir(), "remote.git")
	mustGit(t, repo, "init", "-q", "--bare", remote)
	mustGit(t, repo, "remote", "add", "origin", remote)
	mustGit(t, path, "push", "-q", "origin", "ac/fix")
	if s := g.Status(path, base); s.Ahead != 1 || s.Unpushed != 0 {
		t.Fatalf("pushed: %+v", s)
	}
	if s := g.Status(t.TempDir(), base); s.Err == nil || s.String() != "git state unknown" {
		t.Fatal("non-repo status must error")
	}

	// Dirty worktree: plain remove refuses, --force works.
	os.WriteFile(filepath.Join(path, "a.txt"), []byte("changed\n"), 0o644)
	if err := g.Remove(repo, path, false); err == nil {
		t.Fatal("dirty remove without force")
	}
	if err := g.Remove(repo, path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("worktree dir kept")
	}
	if !g.BranchExists(repo, "ac/fix") {
		t.Fatal("remove must keep the branch")
	}
	if err := g.DeleteBranch(repo, "main"); err == nil || !g.BranchExists(repo, "main") {
		t.Fatal("non-task branch deleted")
	}
	if err := g.DeleteBranch(repo, "ac/fix"); err != nil || g.BranchExists(repo, "ac/fix") {
		t.Fatal(err)
	}

	// A directory that isn't a worktree can't be removed through git.
	plain := filepath.Join(root, "repo", "plain")
	os.MkdirAll(plain, 0o755)
	os.WriteFile(filepath.Join(plain, "keep"), nil, 0o644)
	if err := g.Remove(repo, plain, true); err == nil {
		t.Fatal("git removed a non-worktree")
	}
	if _, err := os.Stat(filepath.Join(plain, "keep")); err != nil {
		t.Fatal("non-worktree touched")
	}

	// Prune: a worktree deleted behind git's back.
	p2 := filepath.Join(root, "repo", "gone")
	if err := g.Add(repo, "ac/gone", p2, base); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(p2)
	if err := g.Prune(repo); err != nil {
		t.Fatal(err)
	}
	list, _ := g.List(repo)
	for _, e := range list {
		if strings.HasSuffix(filepath.ToSlash(e.Path), "/gone") {
			t.Fatalf("prune kept %v", list)
		}
	}
}

// Hostile names never reach git as options or shell text: a branch made
// from a hostile task is a plain ac/ slug, and the base is validated.
func TestGitHostileTask(t *testing.T) {
	repo := newRepo(t)
	g := Git{}
	base, _ := g.ResolveBase(repo, "")
	task := "--upload-pack=touch pwned; $(touch pwned2) `touch pwned3` ../../x"
	slug := Slug(task)
	root := t.TempDir()
	path := filepath.Join(root, SafeComponent("work/$(x)"), slug)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := g.Add(repo, BranchPrefix+slug, path, base); err != nil {
		t.Fatal(err)
	}
	if out := mustGit(t, repo, "branch", "--list", BranchPrefix+"*"); !strings.Contains(out, BranchPrefix+slug) {
		t.Fatalf("branches %q", out)
	}
	for _, f := range []string{"pwned", "pwned2", "pwned3"} {
		for _, d := range []string{repo, root, path} {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				t.Fatalf("%s created in %s", f, d)
			}
		}
	}
}
