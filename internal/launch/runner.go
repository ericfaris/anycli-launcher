package launch

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
)

// Command is one external program to run: a terminal's tab spawner, tmux,
// zellij. Args never pass through a shell.
type Command struct {
	Path string
	Args []string
	Dir  string
}

// String renders the command for notes and test failures (not for a shell).
func (c Command) String() string {
	return strings.Join(append([]string{c.Path}, c.Args...), " ")
}

// Runner executes Commands. Backends build their commands as pure data and
// hand them to a Runner, so tests can assert the exact argv without
// starting a terminal.
type Runner interface {
	// Run attaches the command to the terminal (stdio) and waits, returning
	// its exit code. Used for tmux attach and zellij sessions.
	Run(Command) (int, error)
	// Output runs the command detached from the terminal and returns its
	// stdout and exit code. A non-zero exit is not an error.
	Output(Command) (string, int, error)
}

// ExecRunner is the real Runner.
type ExecRunner struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Run implements Runner.
func (e ExecRunner) Run(c Command) (int, error) {
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.Stdin, e.Stdout, e.Stderr
	return exitCode(cmd.Run())
}

// Output implements Runner. Stderr is captured and folded into the error
// text when the command fails to start.
func (e ExecRunner) Output(c Command) (string, int, error) {
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code, err := exitCode(cmd.Run())
	if code != 0 && err == nil && errb.Len() > 0 {
		// Keep the program's own message for the caller's error report.
		return out.String() + errb.String(), code, nil
	}
	return out.String(), code, err
}

func exitCode(err error) (int, error) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
