// Package launch starts the AI CLI through a pluggable backend:
//
//   - here: run the CLI in the current terminal, in the project directory,
//     and tell the shell shim to cd there afterwards (the default).
//   - tab: open a new terminal tab (Windows Terminal, iTerm2, Terminal.app,
//     kitty, WezTerm, GNOME Terminal) and run it there (tab.go).
//   - tmux / zellij: one session per project, created or re-attached.
//
// Backends build their commands as plain data and run them through an
// injectable Runner, so every argv is unit-tested.
package launch

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

// Request is one launch.
type Request struct {
	Dir   string
	Name  string // CLI command name or path
	Args  []string
	Title string // terminal title, set via OSC 0 when non-empty
	// CDFile is the shim's --cd-file; the project dir is written there so the
	// parent shell lands in it after the CLI exits.
	CDFile string

	// Used by the tab and multiplexer backends only; Here ignores them.

	// Color is the tab color (#rrggbb, already validated) or "".
	Color string
	// Session names the project for tmux/zellij sessions.
	Session string
	// Resume asks a multiplexer to re-attach to an existing session as is,
	// instead of opening another window running a fresh CLI.
	Resume bool
}

// Backend runs a launch request and returns the CLI's exit code.
type Backend interface {
	Launch(Request) (int, error)
}

// Here runs the CLI as a child process sharing this terminal.
type Here struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	LookPath       func(string) (string, error)
}

// ErrNotFound means the CLI binary is not on PATH.
var ErrNotFound = errors.New("not found on PATH")

// Launch writes the cd-file first (so the shell still lands in the project
// if the CLI crashes), sets the title, then runs the CLI and waits.
func (h Here) Launch(r Request) (int, error) {
	look := h.LookPath
	if look == nil {
		look = exec.LookPath
	}
	bin, err := look(r.Name)
	if err != nil {
		return 127, fmt.Errorf("%s: %w", r.Name, ErrNotFound)
	}
	if err := WriteCDFile(r.CDFile, r.Dir); err != nil {
		return 1, err
	}
	if r.Title != "" && h.Stdout != nil {
		fmt.Fprint(h.Stdout, TitleSeq(r.Title))
	}
	cmd := exec.Command(bin, r.Args...)
	cmd.Dir = r.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = h.Stdin, h.Stdout, h.Stderr
	// Ctrl-C belongs to the CLI; don't let it kill the launcher first.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

// WriteCDFile records dir for the shim. A blank path is a no-op (no shim).
func WriteCDFile(path, dir string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(dir), 0o600)
}

// TitleSeq is the OSC 0 sequence setting the terminal/tab title. Control
// characters are stripped so a title can't smuggle escape sequences.
func TitleSeq(title string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, title)
	return "\x1b]0;" + clean + "\a"
}
