package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The tab backend opens a new terminal tab and runs the CLI there.
//
// Every terminal is driven the same way: ac writes a JSON spec (dir, argv,
// title, color) to a private temp file and asks the terminal to run
// `ac __tab-exec <spec>` in a new tab. That trampoline (tabexec.go) is ac
// itself: it deletes the spec, sets the title, cds, drains stray terminal
// replies and execs the CLI. So user data (project names, prompts, flags)
// only ever travels as JSON, never through wt.exe's ';' parsing, the WSL
// interop re-quoting, AppleScript or a shell; the terminal command line
// carries just two paths that SafePath vetted. v3 did the same with a
// generated bash script.

// TabSpec is the trampoline's input.
type TabSpec struct {
	Dir   string   `json:"dir"`
	Argv  []string `json:"argv"`
	Title string   `json:"title,omitempty"`
	Color string   `json:"color,omitempty"`
	// ColorSeq selects an escape sequence for the tab color ("iterm").
	// Windows Terminal takes the color on its command line instead.
	ColorSeq string `json:"color_seq,omitempty"`
	// Login re-execs the trampoline through `bash -l` first, so the CLI
	// sees the login environment (PATH from ~/.profile, nvm, API keys), as
	// v3's `bash -l script` did. Off where the tab already runs an
	// interactive shell (iTerm2, Terminal.app) and on native Windows.
	Login bool `json:"login,omitempty"`
}

// TabInvocation is everything BuildTab needs; it is plain data so each
// terminal's exact argv is table-tested.
type TabInvocation struct {
	Terminal Terminal
	Program  string // resolved terminal program (wt.exe, kitty, osascript...)
	Exe      string // the ac binary
	Spec     string // spec file path
	Dir      string
	Title    string
	Color    string // validated #rrggbb or ""
	Platform string
	// WT-only.
	ProfileID string // $WT_PROFILE_ID: open the tab with the same profile
	Distro    string // $WSL_DISTRO_NAME (default Ubuntu, as v3)
}

// BuildTab returns the command that opens the tab and runs the trampoline.
func BuildTab(inv TabInvocation) (Command, error) {
	if inv.Platform != PlatformWindows && (!SafePath(inv.Exe) || !SafePath(inv.Spec)) {
		return Command{}, fmt.Errorf("%w: ac's path %q or temp dir %q has characters a terminal command line can't carry safely", ErrUnavailable, inv.Exe, inv.Spec)
	}
	tramp := []string{inv.Exe, "__tab-exec", inv.Spec}
	c := Command{Path: inv.Program}
	switch inv.Terminal {
	case TermWT:
		a := []string{"-w", "0", "new-tab"}
		if inv.ProfileID != "" {
			a = append(a, "--profile", WTEscape(stripControl(inv.ProfileID)))
		}
		if t := WTTitle(inv.Title); t != "" {
			a = append(a, "--title", t)
		}
		if inv.Color != "" {
			a = append(a, "--tabColor", inv.Color)
		}
		if inv.Platform == PlatformWSL {
			distro := inv.Distro
			if distro == "" || !SafePath(distro) {
				distro = "Ubuntu"
			}
			// Never pin wt's --startingDirectory to a \\wsl.localhost\ UNC
			// path: WSL can't start in one (Wsl/ERROR_PATH_NOT_FOUND). Pass
			// the Linux path to wsl.exe --cd instead (v3).
			a = append(a, "wsl.exe", "-d", distro, "--cd", WSLCdArg(inv.Dir), "--exec")
			a = append(a, tramp...)
		} else {
			// Native Windows / Git Bash: pin the starting directory so a
			// stale profile startingDirectory can't fail the tab (v3).
			a = append(a, "-d", WTEscape(inv.Dir), "--")
			for _, s := range tramp {
				a = append(a, WTEscape(s))
			}
		}
		c.Args = a
	case TermKitty:
		a := []string{"@", "launch", "--type=tab", "--cwd", inv.Dir}
		if t := stripControl(inv.Title); t != "" {
			a = append(a, "--tab-title", t)
		}
		c.Args = append(a, tramp...)
	case TermWezTerm:
		c.Args = append([]string{"cli", "spawn", "--cwd", inv.Dir, "--"}, tramp...)
	case TermGnome:
		c.Args = append([]string{"--tab", "--working-directory=" + inv.Dir, "--"}, tramp...)
	case TermITerm, TermTerminalApp:
		cmd := "exec " + ShQuote(inv.Exe) + " __tab-exec " + ShQuote(inv.Spec)
		var script string
		if inv.Terminal == TermITerm {
			script = "tell application \"iTerm2\"\n" +
				"\tif (count of windows) = 0 then\n" +
				"\t\tcreate window with default profile\n" +
				"\t\tset s to current session of current window\n" +
				"\telse\n" +
				"\t\ttell current window\n" +
				"\t\t\tset t to (create tab with default profile)\n" +
				"\t\t\tset s to current session of t\n" +
				"\t\tend tell\n" +
				"\tend if\n" +
				"\ttell s to write text " + AppleScriptString(cmd) + "\n" +
				"end tell"
		} else {
			// Terminal.app can only open tabs via System Events keystrokes
			// (needs Accessibility permission), so it gets a new window.
			script = "tell application \"Terminal\"\n" +
				"\tactivate\n" +
				"\tdo script " + AppleScriptString(cmd) + "\n" +
				"end tell"
		}
		c.Args = []string{"-e", script}
	default:
		return Command{}, fmt.Errorf("%w: unknown terminal %q", ErrUnavailable, inv.Terminal)
	}
	return c, nil
}

// SpecFor builds the trampoline spec for a terminal.
func SpecFor(t Terminal, platform, dir, title, color string, argv []string) TabSpec {
	s := TabSpec{Dir: dir, Argv: argv, Title: title, Color: color}
	switch t {
	case TermITerm:
		s.ColorSeq = "iterm"
	}
	switch {
	case platform == PlatformWindows:
	case t == TermITerm || t == TermTerminalApp:
	default:
		s.Login = true
	}
	return s
}

// Tab is the new-tab backend.
type Tab struct {
	Deps
}

// tabSpecPrefix names spec files so stale ones can be swept.
const tabSpecPrefix = "ac-tab-"

// Launch opens a tab. It returns ErrUnavailable (wrapped) when no
// supported terminal or its program is found, so the caller can fall back
// to "here"; nothing is written in that case. No cd-file is written: the
// current shell stays where it is, as in v3.
func (t *Tab) Launch(r Request) (int, error) {
	d := t.Deps.withDefaults()
	wt := ""
	if p := d.Env.Platform(); p == PlatformWSL || p == PlatformWindows || d.Terminal == string(TermWT) {
		wt = FindWT(d.Env, d.LookPath)
	}
	det := DetectTerminal(d.Env, d.Terminal, wt != "")
	if det.Terminal == TermNone {
		return 0, fmt.Errorf("%w: %s", ErrUnavailable, det.Reason)
	}
	prog := wt
	if det.Terminal != TermWT {
		prog = FindTerminalProgram(det.Terminal, d.Env, d.LookPath)
	}
	if prog == "" {
		return 0, fmt.Errorf("%w: %s not found", ErrUnavailable, TerminalProgram(det.Terminal))
	}
	bin, err := d.LookPath(r.Name)
	if err != nil {
		return 127, fmt.Errorf("%s: %w", r.Name, ErrNotFound)
	}
	exe, err := d.executable()
	if err != nil {
		return 1, err
	}
	platform := d.Env.Platform()
	spec := SpecFor(det.Terminal, platform, r.Dir, r.Title, r.Color, append([]string{bin}, r.Args...))
	tmp := d.TempDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	SweepSpecs(tmp, d.now(), 24*time.Hour)
	// Build first with a placeholder path so an unsafe ac path fails before
	// anything is written.
	inv := TabInvocation{
		Terminal: det.Terminal, Program: prog, Exe: exe, Dir: r.Dir, Title: r.Title, Color: r.Color,
		Platform: platform, ProfileID: d.Env.Getenv("WT_PROFILE_ID"), Distro: d.Env.Getenv("WSL_DISTRO_NAME"),
		Spec: filepath.Join(tmp, tabSpecPrefix+"00000000.json"),
	}
	if _, err := BuildTab(inv); err != nil {
		return 0, err
	}
	path, err := WriteSpec(tmp, spec)
	if err != nil {
		return 1, err
	}
	inv.Spec = path
	cmd, err := BuildTab(inv)
	if err != nil {
		os.Remove(path)
		return 0, err
	}
	out, code, err := d.Runner.Output(cmd)
	if err != nil || code != 0 {
		os.Remove(path)
		if err == nil {
			err = fmt.Errorf("%s exited %d: %s", filepath.Base(prog), code, strings.TrimSpace(out))
		}
		return 1, err
	}
	return 0, nil
}

// WriteSpec stores spec in a new private file under dir and returns its path.
func WriteSpec(dir string, spec TabSpec) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, tabSpecPrefix+"*.json")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// SweepSpecs removes spec files older than maxAge (tabs that never started).
func SweepSpecs(dir string, now time.Time, maxAge time.Duration) {
	m, _ := filepath.Glob(filepath.Join(dir, tabSpecPrefix+"*.json"))
	for _, p := range m {
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() && now.Sub(st.ModTime()) > maxAge {
			os.Remove(p)
		}
	}
}
