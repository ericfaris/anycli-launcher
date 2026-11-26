package launch

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// Backend names, as accepted by config `backend`, .ac.toml and --backend.
const (
	BackendAuto   = "auto"
	BackendHere   = "here"
	BackendTab    = "tab"
	BackendTmux   = "tmux"
	BackendZellij = "zellij"
)

// Backends lists the accepted names. The set is closed: .ac.toml comes
// from the repo, so it may pick one of these but never a command.
var Backends = []string{BackendAuto, BackendHere, BackendTab, BackendTmux, BackendZellij}

// ValidBackend reports whether s is a backend name ("" means auto).
func ValidBackend(s string) bool {
	if s == "" {
		return true
	}
	for _, b := range Backends {
		if b == s {
			return true
		}
	}
	return false
}

// Choose resolves a backend setting to a concrete backend. "auto" (or "")
// is v3's rule: a new tab when the project sets a color or title, else
// here. Unknown names also resolve as auto; doctor reports them.
func Choose(setting string, hasColorOrTitle bool) string {
	switch setting {
	case BackendHere, BackendTab, BackendTmux, BackendZellij:
		return setting
	}
	if hasColorOrTitle {
		return BackendTab
	}
	return BackendHere
}

// ErrUnavailable means a backend can't run here (no supported terminal, the
// multiplexer isn't installed, ...). Callers fall back to "here".
var ErrUnavailable = errors.New("backend unavailable")

// Unverified marks backends and terminals whose command shapes come from
// documentation and could not be run against the real program while this
// was written. Doctor prints these. (Windows Terminal's flags are v3's,
// which ran in production, but the trampoline route is new.)
var Unverified = map[string]string{
	string(TermWT):          "flags from v3 (production); the wsl.exe --exec trampoline route is new and needs a manual check",
	string(TermITerm):       "AppleScript from iTerm2 docs; not run",
	string(TermTerminalApp): "AppleScript from Terminal docs; not run (opens a window, not a tab)",
	string(TermKitty):       "flags from kitty docs; needs allow_remote_control; not run",
	string(TermWezTerm):     "flags from wezterm docs; not run",
	string(TermGnome):       "flags from gnome-terminal docs; not run",
	BackendZellij:           "flags from zellij docs (0.40+); zellij not installed when written",
}

// Deps are what the non-trivial backends need. Zero values get real
// defaults (withDefaults).
type Deps struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	LookPath       func(string) (string, error)
	Runner         Runner
	Env            Environ
	// Terminal forces a tab terminal (config `terminal`); "" detects.
	Terminal string
	// Exe is the ac binary the tab trampoline runs; "" means os.Executable.
	Exe string
	// TempDir holds tab specs and zellij layouts; "" means os.TempDir.
	TempDir string
	// TmuxSocket, when set, adds `-L <socket>` to every tmux call (tests).
	TmuxSocket string
	Now        func() time.Time
}

func (d Deps) withDefaults() Deps {
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.Runner == nil {
		d.Runner = ExecRunner{Stdin: d.Stdin, Stdout: d.Stdout, Stderr: d.Stderr}
	}
	d.Env = d.Env.withDefaults()
	return d
}

func (d Deps) executable() (string, error) {
	if d.Exe != "" {
		return d.Exe, nil
	}
	return os.Executable()
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// New returns the backend for a concrete name (see Choose). Unknown names
// get Here.
func New(name string, d Deps) Backend {
	switch name {
	case BackendTab:
		return &Tab{Deps: d}
	case BackendTmux:
		return &Tmux{Deps: d}
	case BackendZellij:
		return &Zellij{Deps: d}
	}
	return Here{Stdin: d.Stdin, Stdout: d.Stdout, Stderr: d.Stderr, LookPath: d.LookPath}
}
