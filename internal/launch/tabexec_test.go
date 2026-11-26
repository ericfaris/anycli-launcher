package launch

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type execCall struct {
	argv []string
	dir  string
	env  []string
}

// stubTabExec replaces the trampoline's process seams for one test.
func stubTabExec(t *testing.T, os_ string) *[]execCall {
	t.Helper()
	t.Chdir(t.TempDir()) // TabExec chdirs; restore the cwd afterwards
	var calls []execCall
	oldExec, oldDrain, oldPause, oldLook, oldExe, oldGOOS := execFn, drainFn, pauseFn, lookPathFn, exeFn, goos
	t.Cleanup(func() {
		execFn, drainFn, pauseFn, lookPathFn, exeFn, goos = oldExec, oldDrain, oldPause, oldLook, oldExe, oldGOOS
	})
	execFn = func(argv []string, dir string, env []string) (int, error) {
		calls = append(calls, execCall{argv, dir, env})
		return 7, nil
	}
	drainFn = func() {}
	pauseFn = func(io.Writer) {}
	lookPathFn = func(n string) (string, error) { return "/bin/" + n, nil }
	exeFn = func() (string, error) { return "/usr/local/bin/ac", nil }
	goos = os_
	return &calls
}

func TestTabExecRunsSpec(t *testing.T) {
	calls := stubTabExec(t, "linux")
	dir := t.TempDir()
	prompt := "it's \"x\"; $(touch pwned)\nline"
	path, err := WriteSpec(t.TempDir(), TabSpec{Dir: dir, Argv: []string{"/bin/claude", prompt}, Title: "T\x1b]x", Color: "#102030", ColorSeq: "iterm"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := TabExec([]string{path}, &out, &errb); code != 7 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("spec must be deleted before the CLI runs")
	}
	if len(*calls) != 1 {
		t.Fatalf("calls %v", *calls)
	}
	c := (*calls)[0]
	if !reflect.DeepEqual(c.argv, []string{"/bin/claude", prompt}) || c.dir != dir {
		t.Fatalf("%+v", c)
	}
	if c.env[len(c.env)-1] != "_AC_SPAWNED_TAB=1" {
		t.Fatalf("env tail %q", c.env[len(c.env)-1])
	}
	wd, _ := os.Getwd()
	if r1, _ := filepath.EvalSymlinks(wd); r1 != mustEval(t, dir) {
		t.Fatalf("cwd %s want %s", wd, dir)
	}
	want := "\x1b]0;T]x\a" + "\x1b]6;1;bg;red;brightness;16\a\x1b]6;1;bg;green;brightness;32\a\x1b]6;1;bg;blue;brightness;48\a"
	if out.String() != want {
		t.Fatalf("out %q", out.String())
	}
}

func mustEval(t *testing.T, p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTabExecLoginStage(t *testing.T) {
	calls := stubTabExec(t, "linux")
	path, _ := WriteSpec(t.TempDir(), TabSpec{Dir: t.TempDir(), Argv: []string{"/bin/claude"}, Login: true})
	if code := TabExec([]string{path}, &bytes.Buffer{}, &bytes.Buffer{}); code != 7 {
		t.Fatal(code)
	}
	want := []string{"/bin/bash", "-l", "-c", `exec "$0" __tab-exec --login-done "$1"`, "/usr/local/bin/ac", path}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].argv, want) {
		t.Fatalf("%v", *calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the login stage must leave the spec for the second stage")
	}
	// Second stage (after the login shell): runs the CLI and deletes the spec.
	*calls = nil
	TabExec([]string{"--login-done", path}, &bytes.Buffer{}, &bytes.Buffer{})
	if len(*calls) != 1 || (*calls)[0].argv[0] != "/bin/claude" {
		t.Fatalf("%v", *calls)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("spec kept")
	}
}

func TestTabExecLoginSkippedOnWindowsAndFallsBack(t *testing.T) {
	calls := stubTabExec(t, "windows")
	path, _ := WriteSpec(t.TempDir(), TabSpec{Dir: t.TempDir(), Argv: []string{"c.exe"}, Login: true})
	TabExec([]string{path}, &bytes.Buffer{}, &bytes.Buffer{})
	if len(*calls) != 1 || (*calls)[0].argv[0] != "c.exe" {
		t.Fatalf("%v", *calls)
	}
	// A failing login shell falls back to running the CLI directly.
	calls = stubTabExec(t, "linux")
	n := 0
	execFn = func(argv []string, dir string, env []string) (int, error) {
		n++
		*calls = append(*calls, execCall{argv: argv})
		if n == 1 {
			return 127, errors.New("no bash")
		}
		return 0, nil
	}
	path, _ = WriteSpec(t.TempDir(), TabSpec{Dir: t.TempDir(), Argv: []string{"/bin/c"}, Login: true})
	var errb bytes.Buffer
	if code := TabExec([]string{path}, &bytes.Buffer{}, &errb); code != 0 || len(*calls) != 2 || (*calls)[1].argv[0] != "/bin/c" {
		t.Fatalf("code %d calls %v err %s", code, *calls, errb.String())
	}
}

func TestTabExecErrors(t *testing.T) {
	calls := stubTabExec(t, "linux")
	paused := 0
	pauseFn = func(io.Writer) { paused++ }
	var errb bytes.Buffer
	if TabExec(nil, &bytes.Buffer{}, &errb) != 2 {
		t.Fatal("usage")
	}
	if TabExec([]string{filepath.Join(t.TempDir(), "missing.json")}, &bytes.Buffer{}, &errb) != 1 || paused != 1 {
		t.Fatal("missing spec")
	}
	bad := filepath.Join(t.TempDir(), "ac-tab-bad.json")
	os.WriteFile(bad, []byte(`{"dir":"/x"}`), 0o600)
	if TabExec([]string{bad}, &bytes.Buffer{}, &errb) != 1 {
		t.Fatal("incomplete spec")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Fatal("bad spec should be removed")
	}
	path, _ := WriteSpec(t.TempDir(), TabSpec{Dir: filepath.Join(t.TempDir(), "gone"), Argv: []string{"/bin/c"}})
	if TabExec([]string{path}, &bytes.Buffer{}, &errb) != 1 || len(*calls) != 0 {
		t.Fatal("missing dir must not run the CLI")
	}
	execFn = func([]string, string, []string) (int, error) { return 127, errors.New("exec format error") }
	path, _ = WriteSpec(t.TempDir(), TabSpec{Dir: t.TempDir(), Argv: []string{"/bin/c"}})
	if TabExec([]string{path}, &bytes.Buffer{}, &errb) != 127 || !strings.Contains(errb.String(), "exec format error") {
		t.Fatal(errb.String())
	}
}

func TestReadSpecRejectsSharedWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	path, _ := WriteSpec(t.TempDir(), TabSpec{Dir: "/x", Argv: []string{"/bin/c"}})
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSpec(path); err == nil || !strings.Contains(err.Error(), "writable by others") {
		t.Fatal(err)
	}
	link := path + ".lnk"
	os.Chmod(path, 0o600)
	os.Symlink(path, link)
	if _, err := ReadSpec(link); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestITermColorSeq(t *testing.T) {
	if ITermColorSeq("#ff0001") != "\x1b]6;1;bg;red;brightness;255\a\x1b]6;1;bg;green;brightness;0\a\x1b]6;1;bg;blue;brightness;1\a" {
		t.Fatal(ITermColorSeq("#ff0001"))
	}
	for _, bad := range []string{"", "red", "#fff", "#gggggg"} {
		if ITermColorSeq(bad) != "" {
			t.Errorf("%q", bad)
		}
	}
}

func TestLoginArgvIsFixedText(t *testing.T) {
	// The spec path and exe are $0/$1, never part of the -c text.
	a := LoginArgv("/bin/bash", "/x/it's ac", "/t/$(x).json")
	if a[3] != `exec "$0" __tab-exec --login-done "$1"` || a[4] != "/x/it's ac" || a[5] != "/t/$(x).json" {
		t.Fatalf("%q", a)
	}
}
