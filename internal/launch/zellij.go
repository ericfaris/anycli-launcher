package launch

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// zellij backend. UNVERIFIED: zellij was not installed where this was
// written, so the flags below come from the zellij docs (0.40+):
//   zellij list-sessions --short
//   zellij --session <name> --new-session-with-layout <layout.kdl>
//   zellij --session <name> action new-tab --layout <layout.kdl>
//   zellij attach <name>
//   zellij action new-tab --layout <layout.kdl>   (inside zellij)
// `ac doctor` says so. The CLI runs from a generated KDL layout, where every
// value is a quoted KDL string (KDLString), never shell text.

// ZellijLayout renders a layout with one tab running argv in dir. The
// default tab template keeps zellij's tab bar and status bar.
func ZellijLayout(dir, title string, argv []string) string {
	var b strings.Builder
	b.WriteString("layout {\n")
	b.WriteString("    default_tab_template {\n")
	b.WriteString("        pane size=1 borderless=true {\n            plugin location=\"zellij:tab-bar\"\n        }\n")
	b.WriteString("        children\n")
	b.WriteString("        pane size=2 borderless=true {\n            plugin location=\"zellij:status-bar\"\n        }\n")
	b.WriteString("    }\n")
	b.WriteString("    tab")
	if t := stripControl(title); t != "" {
		b.WriteString(" name=" + KDLString(t))
	}
	b.WriteString(" cwd=" + KDLString(dir) + " {\n")
	b.WriteString("        pane command=" + KDLString(argv[0]) + " cwd=" + KDLString(dir))
	if len(argv) > 1 {
		b.WriteString(" {\n            args")
		for _, a := range argv[1:] {
			b.WriteString(" " + KDLString(a))
		}
		b.WriteString("\n        }\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("    }\n}\n")
	return b.String()
}

// ZellijPlan mirrors TmuxPlan. Inside zellij there is no reliable way to
// switch sessions from the CLI, so the CLI opens as a new tab in the
// current session instead (Attach is nil).
type ZellijPlan struct {
	Setup  [][]string
	Attach []string
	Note   string
}

// PlanZellij is the pure core of the zellij backend; layout is the path of
// the file holding ZellijLayout's output.
func PlanZellij(session, layout string, exists, inside, resume bool) ZellijPlan {
	switch {
	case inside:
		return ZellijPlan{
			Setup: [][]string{{"action", "new-tab", "--layout", layout}},
			Note:  "already inside zellij: opened a new tab in this session",
		}
	case !exists:
		return ZellijPlan{Attach: []string{"--session", session, "--new-session-with-layout", layout}}
	case !resume:
		return ZellijPlan{
			Setup:  [][]string{{"--session", session, "action", "new-tab", "--layout", layout}},
			Attach: []string{"attach", session},
		}
	}
	return ZellijPlan{Attach: []string{"attach", session}}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// ZellijHasSession checks `zellij list-sessions --short` output.
func ZellijHasSession(out, session string) bool {
	for _, line := range strings.Split(ansiRe.ReplaceAllString(out, ""), "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == session {
			return true
		}
	}
	return false
}

// Zellij is the zellij backend.
type Zellij struct {
	Deps
}

// Launch creates or re-attaches the project's session (see PlanZellij).
func (z *Zellij) Launch(r Request) (int, error) {
	d := z.Deps.withDefaults()
	bin, err := d.LookPath("zellij")
	if err != nil {
		return 0, fmt.Errorf("%w: zellij not found on PATH", ErrUnavailable)
	}
	cli, err := d.LookPath(r.Name)
	if err != nil {
		return 127, fmt.Errorf("%s: %w", r.Name, ErrNotFound)
	}
	session := sessionFor(r)
	out, code, err := d.Runner.Output(Command{Path: bin, Args: []string{"list-sessions", "--short"}})
	if err != nil {
		return 1, err
	}
	exists := code == 0 && ZellijHasSession(out, session)
	inside := d.Env.Getenv("ZELLIJ") != ""
	tmp := d.TempDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	f, err := os.CreateTemp(tmp, "ac-zellij-*.kdl")
	if err != nil {
		return 1, err
	}
	layout := f.Name()
	defer os.Remove(layout)
	_, werr := f.WriteString(ZellijLayout(r.Dir, r.Title, append([]string{cli}, r.Args...)))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return 1, werr
	}
	plan := PlanZellij(session, layout, exists, inside, r.Resume)
	for _, s := range plan.Setup {
		o, c, err := d.Runner.Output(Command{Path: bin, Args: s})
		if err != nil {
			return 1, err
		}
		if c != 0 {
			return 1, fmt.Errorf("zellij %s: exit %d: %s", strings.Join(s, " "), c, strings.TrimSpace(o))
		}
	}
	if plan.Note != "" && d.Stderr != nil {
		fmt.Fprintf(d.Stderr, "  %s\n", plan.Note)
	}
	if plan.Attach == nil {
		return 0, nil
	}
	if err := WriteCDFile(r.CDFile, r.Dir); err != nil {
		return 1, err
	}
	return d.Runner.Run(Command{Path: bin, Args: plan.Attach})
}
