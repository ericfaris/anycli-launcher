package worktree

import (
	"errors"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Fix the login bug":                    "fix-the-login-bug",
		"  --dangerously-skip-permissions  ":   "dangerously-skip-permissions",
		"$(touch pwned); rm -rf / `id` && x":   "touch-pwned-rm-rf-id-x",
		"../../etc/passwd":                     "etc-passwd",
		"feat: add ÜNICODE naïve café support": "feat-add-nicode-na-ve-caf-support",
		"日本語":               "task",
		"":                  "task",
		"---":               "task",
		"CON":               "con-task",
		"lpt1":              "lpt1-task",
		"com10":             "com10",
		"a.lock":            "a-lock",
		"x\x00y\nz\x1b[31m": "x-y-z-31m",
		"refs/heads/main":   "refs-heads-main",
		"@{-1}":             "1",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q want %q", in, got, want)
		}
	}
	long := Slug(strings.Repeat("word ", 30))
	if len(long) > maxSlug || strings.HasSuffix(long, "-") || !strings.HasPrefix(long, "word-word") {
		t.Fatalf("long: %q", long)
	}
	if got := Slug(strings.Repeat("a", 100)); got != strings.Repeat("a", 40) {
		t.Fatalf("no boundary: %q", got)
	}
	// Every slug makes a valid task branch.
	for in := range cases {
		if b := BranchPrefix + Slug(in); !ValidBranch(b) {
			t.Errorf("%q -> invalid branch %q", in, b)
		}
	}
}

func TestSafeComponent(t *testing.T) {
	cases := map[string]string{
		"api":           "api",
		"work/api":      "work-api",
		`work\api`:      "work-api",
		"..":            "project",
		".hidden":       "hidden",
		"-rf":           "rf",
		"My App (v2)":   "My-App--v2-",
		"name.":         "name",
		"nul":           "nul-project",
		"Aux.txt":       "Aux.txt-project",
		"":              "project",
		"a:b*c?d\"e<f>": "a-b-c-d-e-f-",
	}
	for in, want := range cases {
		if got := SafeComponent(in); got != want {
			t.Errorf("SafeComponent(%q) = %q want %q", in, got, want)
		}
	}
	if got := SafeComponent(strings.Repeat("x", 80)); len(got) != 60 {
		t.Fatalf("len %d", len(got))
	}
}

func TestUnique(t *testing.T) {
	taken := map[string]bool{"fix": true, "fix-2": true}
	got, err := Unique("fix", func(s string) (bool, error) { return taken[s], nil })
	if err != nil || got != "fix-3" {
		t.Fatalf("%q %v", got, err)
	}
	got, _ = Unique("new", func(s string) (bool, error) { return taken[s], nil })
	if got != "new" {
		t.Fatal(got)
	}
	if _, err := Unique("x", func(string) (bool, error) { return true, nil }); err == nil || !strings.Contains(err.Error(), "no free task name") {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if _, err := Unique("x", func(string) (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	// The longest slug plus the largest suffix is still a valid branch.
	long := strings.Repeat("a", maxSlug)
	n := 0
	got, _ = Unique(long, func(string) (bool, error) { n++; return n < maxTries, nil })
	if !ValidBranch(BranchPrefix+got) || got != long+"-100" {
		t.Fatalf("%q", got)
	}
}

func TestValidBranch(t *testing.T) {
	for b, want := range map[string]bool{
		"ac/fix":      true,
		"ac/fix-2":    true,
		"ac/":         false,
		"main":        false,
		"ac/-x":       false,
		"ac/x-":       false,
		"ac/X":        false,
		"ac/a/b":      false,
		"ac/a..b":     false,
		"ac/x;rm":     false,
		"refs/ac/fix": false,
	} {
		if ValidBranch(b) != want {
			t.Errorf("%q: want %v", b, want)
		}
	}
}
