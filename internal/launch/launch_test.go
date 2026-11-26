package launch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The test binary doubles as a fake CLI: it prints its cwd and args.
func TestMain(m *testing.M) {
	if os.Getenv("LAUNCH_HELPER") == "1" {
		wd, _ := os.Getwd()
		fmt.Printf("cwd=%s\nargs=%s\n", wd, strings.Join(os.Args[1:], "|"))
		if len(os.Args) > 1 && os.Args[1] == "fail" {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helper(t *testing.T) Here {
	t.Setenv("LAUNCH_HELPER", "1")
	return Here{LookPath: func(n string) (string, error) {
		if n == "fake" {
			return os.Args[0], nil
		}
		return "", errors.New("no")
	}}
}

func TestHereRunsInDirAndWritesCDFile(t *testing.T) {
	h := helper(t)
	var out bytes.Buffer
	h.Stdout, h.Stderr = &out, &out
	dir := t.TempDir()
	cd := filepath.Join(t.TempDir(), "cd")
	code, err := h.Launch(Request{Dir: dir, Name: "fake", Args: []string{"--a", "two words"}, Title: "My\x1bTitle", CDFile: cd})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v out=%s", code, err, out.String())
	}
	got, _ := os.ReadFile(cd)
	if string(got) != dir {
		t.Fatalf("cd file %q want %q", got, dir)
	}
	s := out.String()
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(s, "\x1b]0;MyTitle\a") {
		t.Fatalf("title seq missing: %q", s)
	}
	if !strings.Contains(s, "args=--a|two words") || !(strings.Contains(s, "cwd="+dir) || strings.Contains(s, "cwd="+real)) {
		t.Fatalf("output %q", s)
	}
}

func TestHereExitCodeAndCDFileOnFailure(t *testing.T) {
	h := helper(t)
	h.Stdout, h.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cd := filepath.Join(t.TempDir(), "cd")
	code, err := h.Launch(Request{Dir: t.TempDir(), Name: "fake", Args: []string{"fail"}, CDFile: cd})
	if err != nil || code != 3 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, err := os.Stat(cd); err != nil {
		t.Fatal("cd file must be written even when the CLI fails")
	}
}

func TestHereNotFound(t *testing.T) {
	h := helper(t)
	cd := filepath.Join(t.TempDir(), "cd")
	code, err := h.Launch(Request{Dir: t.TempDir(), Name: "missing", CDFile: cd})
	if !errors.Is(err, ErrNotFound) || code != 127 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, err := os.Stat(cd); err == nil {
		t.Fatal("cd file must not be written when nothing launched")
	}
}

func TestWriteCDFileBlank(t *testing.T) {
	if err := WriteCDFile("", "/x"); err != nil {
		t.Fatal(err)
	}
}

func TestTitleSeq(t *testing.T) {
	if got := TitleSeq("a\x07b\x1b]c\n"); got != "\x1b]0;ab]c\a" {
		t.Fatalf("%q", got)
	}
}
