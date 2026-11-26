package launch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Environ is the slice of the outside world that terminal detection reads.
// Every field has a real default (see withDefaults), and tests replace them.
type Environ struct {
	Getenv func(string) string
	GOOS   string
	// ProcVersion returns /proc/version (WSL detection fallback).
	ProcVersion func() string
	Glob        func(string) ([]string, error)
	Stat        func(string) (os.FileInfo, error)
}

func (e Environ) withDefaults() Environ {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.GOOS == "" {
		e.GOOS = runtime.GOOS
	}
	if e.ProcVersion == nil {
		e.ProcVersion = func() string {
			b, _ := os.ReadFile("/proc/version")
			return string(b)
		}
	}
	if e.Glob == nil {
		e.Glob = filepath.Glob
	}
	if e.Stat == nil {
		e.Stat = os.Stat
	}
	return e
}

// Platforms, as far as launching tabs is concerned.
const (
	PlatformLinux   = "linux"
	PlatformWSL     = "wsl"
	PlatformWindows = "windows" // native, incl. Git Bash / MSYS (the binary is a Windows exe)
	PlatformMac     = "darwin"
)

// Platform classifies the host. WSL is Linux with WSL_DISTRO_NAME /
// WSL_INTEROP set, or "microsoft" in /proc/version (v3's check).
func (e Environ) Platform() string {
	e = e.withDefaults()
	switch e.GOOS {
	case "windows":
		return PlatformWindows
	case "darwin":
		return PlatformMac
	case "linux":
		if e.Getenv("WSL_DISTRO_NAME") != "" || e.Getenv("WSL_INTEROP") != "" ||
			strings.Contains(strings.ToLower(e.ProcVersion()), "microsoft") {
			return PlatformWSL
		}
	}
	return PlatformLinux
}

// Terminal identifies a tab-capable terminal emulator.
type Terminal string

const (
	TermNone        Terminal = ""
	TermWT          Terminal = "wt" // Windows Terminal
	TermITerm       Terminal = "iterm"
	TermTerminalApp Terminal = "terminal-app"
	TermKitty       Terminal = "kitty"
	TermWezTerm     Terminal = "wezterm"
	TermGnome       Terminal = "gnome-terminal"
)

// Terminals lists the tab terminals in the order doctor reports them.
var Terminals = []Terminal{TermWT, TermITerm, TermTerminalApp, TermKitty, TermWezTerm, TermGnome}

// ValidTerminal reports whether s names a supported terminal (or is empty).
func ValidTerminal(s string) bool {
	if s == "" {
		return true
	}
	for _, t := range Terminals {
		if string(t) == s {
			return true
		}
	}
	return false
}

// Label is the short display name (menu header).
func (t Terminal) Label() string {
	switch t {
	case TermWT:
		return "wt"
	case TermITerm:
		return "iTerm2"
	case TermTerminalApp:
		return "Terminal"
	case TermKitty:
		return "kitty"
	case TermWezTerm:
		return "WezTerm"
	case TermGnome:
		return "GNOME"
	}
	return "none"
}

// Detection is the result of DetectTerminal.
type Detection struct {
	Terminal Terminal
	// Reason says which signal decided it (doctor shows it).
	Reason string
}

// DetectTerminal finds the terminal ac is running in from environment
// variables each emulator sets for its children. forced (the config's
// `terminal` key) wins when valid. The innermost terminal's own variable
// is checked first: a WezTerm started from Windows Terminal inherits
// WT_SESSION, but WEZTERM_PANE is its own.
//
// Windows Terminal is also assumed on WSL/Windows when wt.exe exists even
// without WT_SESSION (v3 parity: v3 looked for WT's settings.json); the
// caller passes wtFound for that.
func DetectTerminal(e Environ, forced string, wtFound bool) Detection {
	e = e.withDefaults()
	if forced != "" && ValidTerminal(forced) {
		return Detection{Terminal(forced), "terminal = \"" + forced + "\" in config"}
	}
	get := e.Getenv
	switch {
	case get("WEZTERM_PANE") != "" || get("TERM_PROGRAM") == "WezTerm":
		return Detection{TermWezTerm, "WEZTERM_PANE / TERM_PROGRAM=WezTerm"}
	case get("KITTY_WINDOW_ID") != "":
		return Detection{TermKitty, "KITTY_WINDOW_ID"}
	case get("TERM_PROGRAM") == "iTerm.app":
		return Detection{TermITerm, "TERM_PROGRAM=iTerm.app"}
	case get("TERM_PROGRAM") == "Apple_Terminal":
		return Detection{TermTerminalApp, "TERM_PROGRAM=Apple_Terminal"}
	case get("WT_SESSION") != "" || get("WT_PROFILE_ID") != "":
		return Detection{TermWT, "WT_SESSION"}
	case get("GNOME_TERMINAL_SCREEN") != "" || get("GNOME_TERMINAL_SERVICE") != "":
		return Detection{TermGnome, "GNOME_TERMINAL_SCREEN"}
	}
	if p := e.Platform(); (p == PlatformWSL || p == PlatformWindows) && wtFound {
		return Detection{TermWT, "wt.exe found on " + p}
	}
	return Detection{TermNone, "no supported terminal detected"}
}

// FindWT locates wt.exe: PATH first, then the per-user WindowsApps alias
// (from WSL via /mnt/c, or %LOCALAPPDATA% natively). v3 used the same
// search order.
func FindWT(e Environ, look func(string) (string, error)) string {
	e = e.withDefaults()
	if look != nil {
		if p, err := look("wt.exe"); err == nil {
			return p
		}
	}
	switch e.Platform() {
	case PlatformWSL:
		if m, _ := e.Glob("/mnt/c/Users/*/AppData/Local/Microsoft/WindowsApps/wt.exe"); len(m) > 0 {
			return m[0]
		}
	case PlatformWindows:
		if la := e.Getenv("LOCALAPPDATA"); la != "" {
			p := filepath.Join(la, "Microsoft", "WindowsApps", "wt.exe")
			if _, err := e.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// TerminalProgram is the program a terminal's tab spawner runs.
func TerminalProgram(t Terminal) string {
	switch t {
	case TermWT:
		return "wt.exe"
	case TermITerm, TermTerminalApp:
		return "osascript"
	case TermKitty:
		return "kitty"
	case TermWezTerm:
		return "wezterm"
	case TermGnome:
		return "gnome-terminal"
	}
	return ""
}

// FindTerminalProgram resolves TerminalProgram(t) to a path, "" if absent.
func FindTerminalProgram(t Terminal, e Environ, look func(string) (string, error)) string {
	if t == TermWT {
		return FindWT(e, look)
	}
	name := TerminalProgram(t)
	if name == "" || look == nil {
		return ""
	}
	p, err := look(name)
	if err != nil {
		return ""
	}
	return p
}
