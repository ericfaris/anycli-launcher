package launch

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
)

// SessionName turns a project name into a tmux/zellij session name: only
// [A-Za-z0-9_-] survive (tmux rejects '.' and ':'; everything else is
// replaced by '_'). When anything was replaced, a short hash of the
// original is appended so "work/api" and "work.api" get different sessions.
func SessionName(project string) string {
	var b strings.Builder
	changed := false
	for _, r := range project {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
			changed = true
		}
	}
	name := strings.Trim(b.String(), "_-")
	if name == "" {
		name = "ac"
		changed = changed || project != ""
	}
	if len(name) > 48 {
		name, changed = name[:48], true
	}
	if changed {
		h := fnv.New32a()
		h.Write([]byte(project))
		name += fmt.Sprintf("-%04x", h.Sum32()&0xffff)
	}
	return name
}

func sessionFor(r Request) string {
	s := r.Session
	if s == "" {
		s = filepath.Base(r.Dir)
	}
	return SessionName(s)
}

// TmuxPlan is what the tmux backend will do.
type TmuxPlan struct {
	// Setup commands run detached (Runner.Output) and must exit 0.
	Setup [][]string
	// Attach runs in the foreground (Runner.Run): attach-session, or
	// switch-client when already inside tmux (never a nested tmux).
	Attach []string
}

// PlanTmux is the pure core of the tmux backend. Every path ends with a
// session named session holding the CLI, then attaches to it:
//
//   - no session yet: new-session -d running argv in dir.
//   - session exists, fresh launch: new-window in it running argv.
//   - session exists, resume: just attach (the CLI is still running there).
//
// argv goes to tmux as separate arguments, which tmux executes directly
// without sh -c. A single-element argv would be handed to sh -c, so it is
// shell-quoted. -c and -n are tmux formats, so '#' is escaped (see
// TmuxFormatEscape).
func PlanTmux(session, dir, title string, argv []string, exists, inside, resume bool) TmuxPlan {
	cmdArgs := argv
	if len(argv) == 1 {
		cmdArgs = []string{ShQuote(argv[0])}
	}
	var p TmuxPlan
	win := func(a []string) []string {
		a = append(a, "-c", TmuxFormatEscape(dir))
		if t := stripControl(title); t != "" {
			a = append(a, "-n", TmuxFormatEscape(t))
		}
		return append(append(a, "--"), cmdArgs...)
	}
	switch {
	case !exists:
		p.Setup = append(p.Setup, win([]string{"new-session", "-d", "-s", session}))
	case !resume:
		p.Setup = append(p.Setup, win([]string{"new-window", "-t", "=" + session + ":"}))
	}
	if inside {
		p.Attach = []string{"switch-client", "-t", "=" + session}
	} else {
		p.Attach = []string{"attach-session", "-t", "=" + session}
	}
	return p
}

// Tmux is the tmux backend.
type Tmux struct {
	Deps
}

func (t *Tmux) cmd(bin string, args ...string) Command {
	if t.TmuxSocket != "" {
		args = append([]string{"-L", t.TmuxSocket}, args...)
	}
	return Command{Path: bin, Args: args}
}

// Launch creates or re-attaches the project's session. Outside tmux the
// cd-file is written first, like Here, since the attach takes over this
// terminal; inside tmux the client switches and this shell stays put.
func (t *Tmux) Launch(r Request) (int, error) {
	d := t.Deps.withDefaults()
	bin, err := d.LookPath("tmux")
	if err != nil {
		return 0, fmt.Errorf("%w: tmux not found on PATH", ErrUnavailable)
	}
	cli, err := d.LookPath(r.Name)
	if err != nil {
		return 127, fmt.Errorf("%s: %w", r.Name, ErrNotFound)
	}
	session := sessionFor(r)
	_, code, err := d.Runner.Output(t.cmd(bin, "has-session", "-t", "="+session))
	if err != nil {
		return 1, err
	}
	inside := d.Env.Getenv("TMUX") != ""
	plan := PlanTmux(session, r.Dir, r.Title, append([]string{cli}, r.Args...), code == 0, inside, r.Resume)
	for _, s := range plan.Setup {
		out, c, err := d.Runner.Output(t.cmd(bin, s...))
		if err != nil {
			return 1, err
		}
		if c != 0 {
			return 1, fmt.Errorf("tmux %s: exit %d: %s", s[0], c, strings.TrimSpace(out))
		}
	}
	if !inside {
		if err := WriteCDFile(r.CDFile, r.Dir); err != nil {
			return 1, err
		}
	}
	return d.Runner.Run(t.cmd(bin, plan.Attach...))
}
