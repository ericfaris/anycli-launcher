package launch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// TabExecCommand is the hidden subcommand the tab trampoline runs as.
const TabExecCommand = "__tab-exec"

// Seams for tests. execFn replaces the process with argv on Unix (it only
// returns on failure) and runs it as a child on Windows.
var (
	execFn     = execProcess
	drainFn    = func() { drainTTY(os.Stdin) }
	pauseFn    = pauseOnError
	lookPathFn = exec.LookPath
	exeFn      = os.Executable
	goos       = runtime.GOOS
)

// loginScript is run by `bash -l -c` with $0 = ac and $1 = the spec. Both
// are passed as separate argv entries, so nothing is interpolated.
const loginScript = `exec "$0" ` + TabExecCommand + ` --login-done "$1"`

// LoginArgv is the re-exec through a login shell (Unix, spec.Login).
func LoginArgv(shell, exe, spec string) []string {
	return []string{shell, "-l", "-c", loginScript, exe, spec}
}

// maxSpecSize bounds what the trampoline will read.
const maxSpecSize = 1 << 20

// ReadSpec loads and checks a spec file.
func ReadSpec(path string) (TabSpec, error) {
	var s TabSpec
	st, err := os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !st.Mode().IsRegular() {
		return s, fmt.Errorf("%s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o022 != 0 {
		return s, fmt.Errorf("%s is writable by others; refusing to run it", path)
	}
	if st.Size() > maxSpecSize {
		return s, fmt.Errorf("%s is too large", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	if len(s.Argv) == 0 || s.Argv[0] == "" || s.Dir == "" {
		return s, fmt.Errorf("%s: incomplete spec", path)
	}
	return s, nil
}

// ITermColorSeq sets an iTerm2 tab color (proprietary OSC 6).
func ITermColorSeq(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	var out string
	for i, name := range []string{"red", "green", "blue"} {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return ""
		}
		out += fmt.Sprintf("\x1b]6;1;bg;%s;brightness;%d\a", name, v)
	}
	return out
}

// TabExec is `ac __tab-exec [--login-done] <spec>`, run inside the new tab.
func TabExec(args []string, stdout, stderr io.Writer) int {
	loginDone := false
	if len(args) > 0 && args[0] == "--login-done" {
		loginDone, args = true, args[1:]
	}
	if len(args) != 1 {
		fmt.Fprintf(stderr, "ac: usage: ac %s <spec>\n", TabExecCommand)
		return 2
	}
	path := args[0]
	spec, err := ReadSpec(path)
	if err != nil {
		os.Remove(path)
		return tabFail(stderr, err, 1)
	}
	if spec.Login && !loginDone && goos != "windows" {
		if exe, err := exeFn(); err == nil {
			shell := "/bin/sh"
			if p, err := lookPathFn("bash"); err == nil {
				shell = p
			}
			argv := LoginArgv(shell, exe, path)
			// On Unix a successful exec never returns.
			code, err := execFn(argv, "", os.Environ())
			if err == nil {
				return code
			}
			fmt.Fprintf(stderr, "ac: login shell failed (%v); starting without it\n", err)
		}
	}
	os.Remove(path)
	if err := os.Chdir(spec.Dir); err != nil {
		return tabFail(stderr, err, 1)
	}
	if spec.Title != "" {
		fmt.Fprint(stdout, TitleSeq(spec.Title))
	}
	if spec.ColorSeq == "iterm" && spec.Color != "" {
		fmt.Fprint(stdout, ITermColorSeq(spec.Color))
	}
	// Drain terminal replies (DA / cursor-position / OSC color answers from
	// the new tab's handshake) so they don't show up as stray input like
	// "2c/3434" in the CLI's prompt (v3's _ac_drain_tty).
	drainFn()
	env := append(os.Environ(), "_AC_SPAWNED_TAB=1")
	// Best effort, like Here: no pid file only makes liveness "unknown".
	_ = WritePIDFile(spec.PIDFile, os.Getpid())
	code, err := execFn(spec.Argv, spec.Dir, env)
	if err != nil {
		return tabFail(stderr, err, 127)
	}
	return code
}

// tabFail reports an error and keeps the tab open long enough to read it.
func tabFail(stderr io.Writer, err error, code int) int {
	fmt.Fprintf(stderr, "ac: %v\n", err)
	pauseFn(stderr)
	return code
}

func pauseOnError(w io.Writer) {
	if st, err := os.Stdin.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Fprint(w, "Press Enter to close this tab.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
