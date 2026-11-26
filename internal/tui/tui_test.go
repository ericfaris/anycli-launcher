package tui

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/ericfaris/anycli-launcher/v4/internal/clis"
	"github.com/ericfaris/anycli-launcher/v4/internal/projects"
)

var update = flag.Bool("update", false, "rewrite golden files")

func renderer(p termenv.Profile) *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(p)
	return r
}

func entries(nProjects int) []Entry {
	es := []Entry{
		{Kind: KindTerminal, Label: "terminal", Dir: "/p"},
		{Kind: KindRoot, Label: "projects", Dir: "/p"},
		{Kind: KindConfig, Label: ".claude", Dir: "/h/.claude"},
	}
	names := []string{"alpha", "ML", "work/api", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota"}
	for i := 0; i < nProjects; i++ {
		es = append(es, Entry{Kind: KindProject, Label: names[i], Dir: "/p/" + names[i]})
	}
	return es
}

func model(n int, theme string, p termenv.Profile) Model {
	return New(Options{
		Version: "4.0.0", CLI: "claude", Entries: entries(n),
		CanResume: true, CanPick: true, Theme: NewTheme(theme, renderer(p)),
	})
}

func send(t *testing.T, m Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		var tm tea.Model
		tm, cmd = m.Update(msg)
		m = tm.(Model)
	}
	return m, cmd
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -update)", err)
	}
	if string(want) != got {
		t.Fatalf("%s mismatch\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestGoldenViews(t *testing.T) {
	m := model(3, "default", termenv.Ascii)
	golden(t, "menu", m.View())

	g := m
	g.opts.ShowGit = true
	tm, _ := g.Update(GitMsg{"/p/alpha": {Branch: "main", Dirty: true, Ahead: 2}, "/h/.claude": {Branch: "master"}, "/p/ML": {}})
	golden(t, "menu_git", tm.(Model).View())

	// 13 entries so "1" waits for a second digit.
	c, _ := send(t, model(10, "default", termenv.Ascii), "down", "down", "down", "down", "1")
	golden(t, "menu_cursor_input", c.View())

	h, _ := send(t, m, "?")
	golden(t, "help", h.View())

	noResume := New(Options{Version: "4.0.0", Entries: entries(0), Theme: NewTheme("default", renderer(termenv.Ascii))})
	golden(t, "empty_no_cli", noResume.View())
	nh, _ := send(t, noResume, "?")
	golden(t, "help_no_sessions", nh.View())

	bad, _ := send(t, m, "9")
	golden(t, "status_no_entry", bad.View())

	// Phase 3: the active backend, right-aligned on the CLI line.
	for name, label := range map[string]string{"menu_backend_auto": "auto: wt", "menu_backend_tmux": "tmux"} {
		b := model(3, "default", termenv.Ascii)
		b.opts.Backend = label
		golden(t, name, b.View())
	}
	// A long CLI name plus the longest label still fits the box.
	long := model(1, "default", termenv.Ascii)
	long.opts.CLI, long.opts.Backend = "kiro-cli", "auto: Terminal"
	for _, line := range strings.Split(long.View(), "\n")[:5] {
		if n := len([]rune(line)); n > 0 && n != 40 {
			t.Fatalf("header line width %d: %q", n, line)
		}
	}
}

func TestGoldenThemes(t *testing.T) {
	for _, name := range Presets {
		t.Run(name, func(t *testing.T) {
			golden(t, "theme_"+name, model(2, name, termenv.TrueColor).View())
		})
	}
	a := model(2, "default", termenv.TrueColor).View()
	b := model(2, "dusk", termenv.TrueColor).View()
	if a == b {
		t.Fatal("themes should differ")
	}
	if NewTheme("nope", nil).Name != "default" || !ValidTheme("mono") || ValidTheme("nope") {
		t.Fatal("theme lookup")
	}
}

func TestNumberSelection(t *testing.T) {
	// 6 entries: any single digit is unambiguous -> immediate select.
	m := model(3, "default", termenv.Ascii)
	r, cmd := send(t, m, "4")
	if cmd == nil || r.Result().Action != ActSelect || r.Result().Entry.Label != "alpha" || r.Result().Mode != clis.Fresh {
		t.Fatalf("got %+v", r.Result())
	}
	if r.View() != "" {
		t.Fatal("view must be empty after selection")
	}

	// 13 entries: "1" waits (could be 10-13), Enter commits it.
	m = model(10, "default", termenv.Ascii)
	w, cmd := send(t, m, "1")
	if cmd != nil || w.done {
		t.Fatal("1 must wait for more digits")
	}
	w, _ = send(t, w, "enter")
	if w.Result().Entry.Kind != KindTerminal {
		t.Fatalf("got %+v", w.Result())
	}
	w, _ = send(t, m, "1", "2")
	if w.Result().Entry.Label != "theta" {
		t.Fatalf("12: got %+v", w.Result())
	}
	w, _ = send(t, m, "1", "4")
	if w.done || w.status != "no entry 14" {
		t.Fatalf("14: %+v", w)
	}
	w, _ = send(t, m, "1", "backspace", "5")
	if w.Result().Entry.Label != "ML" {
		t.Fatalf("backspace: %+v", w.Result())
	}
	w, _ = send(t, m, "0")
	if w.done || w.input != "" {
		t.Fatal("leading 0 ignored")
	}
}

func TestPrefixes(t *testing.T) {
	m := model(3, "default", termenv.Ascii)
	cases := []struct {
		keys   []string
		action Action
		mode   clis.Mode
		label  string
	}{
		{[]string{"r", "4"}, ActSelect, clis.Resume, "alpha"},
		{[]string{"P", "5"}, ActSelect, clis.Picker, "ML"},
		{[]string{"x", "6"}, ActRemove, clis.Fresh, "work/api"},
		{[]string{"n"}, ActNew, clis.Fresh, ""},
		{[]string{"c"}, ActConfig, clis.Fresh, ""},
		{[]string{"q"}, ActQuit, clis.Fresh, ""},
		{[]string{"ctrl+c"}, ActQuit, clis.Fresh, ""},
		{[]string{"esc"}, ActQuit, clis.Fresh, ""},
		{[]string{"down", "down", "enter"}, ActSelect, clis.Fresh, ".claude"},
		{[]string{"down", "up", "up", "enter"}, ActSelect, clis.Fresh, "terminal"},
	}
	for _, c := range cases {
		r, _ := send(t, m, c.keys...)
		if !r.done || r.Result().Action != c.action || r.Result().Mode != c.mode || r.Result().Entry.Label != c.label {
			t.Errorf("%v: got done=%v %+v", c.keys, r.done, r.Result())
		}
	}
	// Prefixed numbers must target projects, not the fixed entries.
	r, _ := send(t, m, "x", "2")
	if r.done || !strings.Contains(r.status, "x<N> needs a project") {
		t.Fatalf("x2: %+v", r)
	}
	// Esc clears a pending prefix instead of quitting.
	r, _ = send(t, m, "r", "esc")
	if r.done || r.input != "" {
		t.Fatal("esc should clear input")
	}
	// q while typing a prefix is ignored, not quit.
	r, _ = send(t, m, "r", "q")
	if r.done {
		t.Fatal("q after prefix must not quit")
	}
	// Cursor stays in bounds.
	r, _ = send(t, m, "up", "down", "down", "down", "down", "down", "down", "down", "down")
	if r.cursor != 5 {
		t.Fatalf("cursor %d", r.cursor)
	}
}

func TestInitGit(t *testing.T) {
	m := model(2, "default", termenv.Ascii)
	if m.Init() != nil {
		t.Fatal("no git fetch when disabled")
	}
	var asked []string
	m.opts.ShowGit = true
	m.opts.GitFunc = func(d []string) map[string]projects.GitStatus {
		asked = d
		return map[string]projects.GitStatus{"/p/alpha": {Branch: "main"}}
	}
	msg := m.Init()()
	if len(asked) != 3 { // config + 2 projects
		t.Fatalf("asked %v", asked)
	}
	tm, _ := m.Update(msg)
	if !strings.Contains(tm.(Model).View(), "alpha  main") {
		t.Fatal("git status not rendered")
	}
}
