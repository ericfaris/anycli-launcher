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
	"path/filepath"
	"strconv"
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
	// PIDFile, when set, receives the CLI's process id once it starts (here:
	// the child; tab: the trampoline, which on Unix execs into the CLI). Task
	// sessions use it for liveness. tmux/zellij ignore it.
	PIDFile string
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
	if err = cmd.Start(); err == nil {
		// Best effort: a missing pid file only makes liveness "unknown".
		_ = WritePIDFile(r.PIDFile, cmd.Process.Pid)
		err = cmd.Wait()
	}
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

// WritePIDFile records pid at path (no-op for ""), replacing it atomically
// so a reader never sees a half-written number.
func WritePIDFile(path string, pid int) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".pid-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(strconv.Itoa(pid) + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
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
