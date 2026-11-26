package launch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake CLI: it prints its cwd and args.
func TestMain(m *testing.M) {
	if os.Getenv("LAUNCH_HELPER") == "1" {
		wd, _ := os.Getwd()
		if out := os.Getenv("LAUNCH_HELPER_OUT"); out != "" {
			// tmux integration: the pane's output isn't ours to read, so
			// append a JSON record and stay alive until the server is killed.
			rec, _ := json.Marshal(map[string]any{"cwd": wd, "args": os.Args[1:]})
			f, _ := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			f.Write(append(rec, '\n'))
			f.Close()
			time.Sleep(60 * time.Second)
			os.Exit(0)
		}
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

// Regression: the phase 3 Request fields (Color, Session, Resume) must not
// change what Here does. Same argv, cwd, title sequence and cd-file, byte
// for byte, as a phase 2 request.
func TestHereIgnoresPhase3Fields(t *testing.T) {
	dir := t.TempDir()
	run := func(r Request) (string, string) {
		h := helper(t)
		var out bytes.Buffer
		h.Stdout, h.Stderr = &out, &out
		r.CDFile = filepath.Join(t.TempDir(), "cd")
		if code, err := h.Launch(r); code != 0 || err != nil {
			t.Fatalf("code=%d err=%v", code, err)
		}
		cd, _ := os.ReadFile(r.CDFile)
		return out.String(), string(cd)
	}
	base := Request{Dir: dir, Name: "fake", Args: []string{"--x", "a b"}, Title: "T"}
	wantOut, wantCD := run(base)
	ext := base
	ext.Color, ext.Session, ext.Resume = "#ff0000", "proj", true
	gotOut, gotCD := run(ext)
	if gotOut != wantOut || gotCD != wantCD {
		t.Fatalf("Here changed:\n%q\n%q", wantOut, gotOut)
	}
	if fmt.Sprintf("%T", New(BackendHere, Deps{})) != "launch.Here" {
		t.Fatal("here must map to Here")
	}
	if fmt.Sprintf("%T", New("bogus", Deps{})) != "launch.Here" {
		t.Fatal("unknown names must map to Here")
	}
}

func TestTitleSeq(t *testing.T) {
	if got := TitleSeq("a\x07b\x1b]c\n"); got != "\x1b]0;ab]c\a" {
		t.Fatalf("%q", got)
	}
}
