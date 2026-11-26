// Package tui is the interactive project menu (Bubble Tea).
//
// Selection keeps v3's muscle memory: type an entry number and press Enter.
// When no longer number could start with the digits typed so far (e.g. "4"
// in a 12-entry menu), it selects immediately. r<N> resumes, p<N> opens the
// CLI's session picker, x<N> removes a project. Arrow keys / j k + Enter
// also work.
//
// Typing a name filters instead: "/" starts the filter explicitly, and any
// letter that is not a menu key at an empty prompt starts it implicitly
// (r/p/x followed by a letter becomes the filter text too, so "rust" still
// works). See filter.go.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ericfaris/anycli-launcher/internal/clis"
	"github.com/ericfaris/anycli-launcher/internal/projects"
)

// Kind is an entry type.
type Kind int

const (
	KindTerminal Kind = iota // cd to the root, no CLI
	KindRoot                 // cd to the root and launch
	KindConfig               // cd to the CLI's config folder and launch
	KindProject
)

// Entry is one numbered menu row.
type Entry struct {
	Kind  Kind
	Label string
	Dir   string
}

// Action is what the user chose.
type Action int

const (
	ActQuit Action = iota
	ActSelect
	ActNew
	ActRemove
	ActConfig
)

// Result is returned when the menu exits.
type Result struct {
	Action Action
	Entry  Entry
	Mode   clis.Mode
}

// Options configure the menu.
type Options struct {
	Version string
	CLI     string // shown in the header
	Entries []Entry
	ShowGit bool
	// CanResume/CanPick decide whether "?" help lists r<N>/p<N>.
	CanResume, CanPick bool
	Theme              Theme
	// GitFunc fetches statuses; nil means projects.GitAll.
	GitFunc func([]string) map[string]projects.GitStatus
	// Scores are frecency scores keyed by directory (see internal/frecency);
	// the filter uses them to break ties between equally good matches.
	Scores map[string]float64
}

// GitMsg delivers git statuses keyed by directory.
type GitMsg map[string]projects.GitStatus

// Model is the Bubble Tea model.
type Model struct {
	opts   Options
	cursor int
	input  string
	status string
	help   bool
	git    map[string]projects.GitStatus
	done   bool
	result Result
	// filtering is true while the type-to-filter prompt is active.
	filtering bool
	filter    string
	fcursor   int
}

// New builds a model.
func New(o Options) Model {
	return Model{opts: o, git: map[string]projects.GitStatus{}}
}

// Result is the user's choice (valid after the program exits).
func (m Model) Result() Result { return m.result }

// Init starts the async git status fetch when enabled.
func (m Model) Init() tea.Cmd {
	if !m.opts.ShowGit {
		return nil
	}
	var dirs []string
	for _, e := range m.opts.Entries {
		if e.Kind == KindProject || e.Kind == KindConfig {
			dirs = append(dirs, e.Dir)
		}
	}
	fn := m.opts.GitFunc
	if fn == nil {
		fn = projects.GitAll
	}
	return func() tea.Msg { return GitMsg(fn(dirs)) }
}

// Update handles input.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case GitMsg:
		m.git = msg
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) finish(r Result) (tea.Model, tea.Cmd) {
	m.done, m.result = true, r
	return m, tea.Quit
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	if m.filtering {
		return m.filterKey(k)
	}
	s := k.String()
	switch s {
	case "ctrl+c":
		return m.finish(Result{Action: ActQuit})
	case "esc":
		if m.input != "" || m.help {
			m.input, m.help = "", false
			return m, nil
		}
		return m.finish(Result{Action: ActQuit})
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < len(m.opts.Entries)-1 {
			m.cursor++
		}
		return m, nil
	case "backspace":
		if m.input != "" {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case "enter":
		if m.input == "" {
			if len(m.opts.Entries) == 0 {
				return m, nil
			}
			return m.finish(Result{Action: ActSelect, Entry: m.opts.Entries[m.cursor]})
		}
		return m.commit()
	}
	if len(s) != 1 {
		if m.input == "" && k.Type == tea.KeyRunes && !k.Alt && len(k.Runes) == 1 && unicode.IsLetter(k.Runes[0]) {
			return m.startFilter(s), nil // non-ASCII letter (é, ü, ...)
		}
		return m, nil
	}
	c := s[0]
	switch {
	case c >= '0' && c <= '9':
		m.input += s
		n, prefix := m.parse()
		if n == 0 && prefix == "" && m.input == "0" {
			m.input = ""
			return m, nil
		}
		if n*10 > len(m.opts.Entries) {
			return m.commit()
		}
		return m, nil
	case m.input == "" && (c == 'r' || c == 'p' || c == 'x' || c == 'R' || c == 'P' || c == 'X'):
		m.input = strings.ToLower(s)
		return m, nil
	case m.input == "" && (c == 'q' || c == 'Q'):
		return m.finish(Result{Action: ActQuit})
	case m.input == "" && (c == 'n' || c == 'N'):
		return m.finish(Result{Action: ActNew})
	case m.input == "" && (c == 'c' || c == 'C'):
		return m.finish(Result{Action: ActConfig})
	case m.input == "" && c == '?':
		m.help = !m.help
		return m, nil
	case m.input == "" && c == '/':
		return m.startFilter(""), nil
	case m.input == "" && isLetter(c):
		return m.startFilter(s), nil
	case len(m.input) == 1 && strings.ContainsRune("rpx", rune(m.input[0])) && isLetter(c):
		// "r" then a letter is a name being typed, not r<N>.
		return m.startFilter(m.input + s), nil
	}
	return m, nil
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// parse splits the input into an optional r/p/x prefix and a number.
func (m Model) parse() (int, string) {
	in := m.input
	prefix := ""
	if in != "" && strings.ContainsRune("rpx", rune(in[0])) {
		prefix, in = in[:1], in[1:]
	}
	n, _ := strconv.Atoi(in)
	return n, prefix
}

func (m Model) commit() (tea.Model, tea.Cmd) {
	n, prefix := m.parse()
	m.input = ""
	if n < 1 || n > len(m.opts.Entries) {
		if n > 0 {
			m.status = fmt.Sprintf("no entry %d", n)
		}
		return m, nil
	}
	e := m.opts.Entries[n-1]
	if prefix != "" && e.Kind != KindProject {
		m.status = fmt.Sprintf("%s<N> needs a project number", prefix)
		return m, nil
	}
	switch prefix {
	case "r":
		return m.finish(Result{Action: ActSelect, Entry: e, Mode: clis.Resume})
	case "p":
		return m.finish(Result{Action: ActSelect, Entry: e, Mode: clis.Picker})
	case "x":
		return m.finish(Result{Action: ActRemove, Entry: e})
	}
	return m.finish(Result{Action: ActSelect, Entry: e})
}

const boxInner = 36

// View renders the menu. It is empty once a choice is made so the inline
// program leaves no menu behind in the scrollback.
func (m Model) View() string {
	if m.done {
		return ""
	}
	t := m.opts.Theme
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(m.header())
	b.WriteString("\n")
	switch {
	case m.filtering:
		b.WriteString(m.filterView())
	case m.help:
		b.WriteString(m.helpView())
	default:
		b.WriteString(m.listView())
	}
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString("  " + t.Error.Render(m.status) + "\n")
	}
	if m.filtering {
		b.WriteString("  " + t.Prompt.Render("›") + " " + t.Dim.Render("/") + m.filter + "\n")
	} else {
		b.WriteString("  " + t.Prompt.Render("›") + " " + m.input + "\n")
	}
	return b.String()
}

func (m Model) header() string {
	t := m.opts.Theme
	line := func(left, right string, leftStyle, rightStyle func(...string) string) string {
		pad := boxInner - 4 - len([]rune(left)) - len([]rune(right))
		if pad < 1 {
			pad = 1
		}
		return "  " + t.Title.Render("│") + "  " + leftStyle(left) + strings.Repeat(" ", pad) + rightStyle(right) + "  " + t.Title.Render("│") + "\n"
	}
	var b strings.Builder
	b.WriteString("  " + t.Title.Render("╭"+strings.Repeat("─", boxInner)+"╮") + "\n")
	b.WriteString(line("anycli-launcher", "v"+m.opts.Version, t.Title.Render, t.Dim.Render))
	if m.opts.CLI != "" {
		b.WriteString(line(m.opts.CLI, "", t.Dim.Render, t.Dim.Render))
	}
	b.WriteString("  " + t.Title.Render("╰"+strings.Repeat("─", boxInner)+"╯") + "\n")
	return b.String()
}

func (m Model) listView() string {
	t := m.opts.Theme
	var b strings.Builder
	for i, e := range m.opts.Entries {
		mark := "  "
		if i == m.cursor {
			mark = t.Cursor.Render("▸") + " "
		}
		num := t.Num.Render(fmt.Sprintf("%2d", i+1))
		var label string
		switch e.Kind {
		case KindTerminal:
			label = t.Accent.Render("● " + e.Label)
		case KindRoot:
			label = t.Accent.Render("● " + e.Label + " (root)")
		case KindConfig:
			label = t.Accent.Render("● " + e.Label + " (config)")
		default:
			if i == m.cursor {
				label = t.Cursor.Render(e.Label)
			} else {
				label = t.Item.Render(e.Label)
			}
		}
		line := " " + mark + num + "  " + label
		if g := m.git[e.Dir].String(); g != "" && (e.Kind == KindProject || e.Kind == KindConfig) {
			line += "  " + t.Dim.Render(g)
		}
		b.WriteString(line + "\n")
	}
	if !m.hasProjects() {
		b.WriteString("     " + t.Dim.Render("no projects yet (n to create one)") + "\n")
	}
	b.WriteString("\n")
	keys := []string{"c config", "n new", "x<N> remove", "? help", "q quit"}
	var parts []string
	for _, k := range keys {
		key, desc, _ := strings.Cut(k, " ")
		parts = append(parts, t.Num.Render(key)+" "+desc)
	}
	b.WriteString("    " + strings.Join(parts, "   ") + "\n")
	return b.String()
}

func (m Model) hasProjects() bool {
	for _, e := range m.opts.Entries {
		if e.Kind == KindProject {
			return true
		}
	}
	return false
}

func (m Model) helpView() string {
	t := m.opts.Theme
	rows := [][2]string{
		{"<N> Enter", "open entry N (selects at once when unambiguous)"},
		{"↑ ↓ / j k", "move, Enter to open"},
	}
	if m.opts.CanResume {
		rows = append(rows, [2]string{"r<N>", "resume project N's most recent session"})
	}
	if m.opts.CanPick {
		rows = append(rows, [2]string{"p<N>", "pick a session to resume for project N"})
	}
	rows = append(rows,
		[2]string{"x<N>", "remove project N (asks first)"},
		[2]string{"n", "new project"},
		[2]string{"c", "edit config"},
		[2]string{"/", "filter by name (or just type a name)"},
		[2]string{"Esc", "clear input / close help / quit"},
		[2]string{"q", "quit"},
	)
	var b strings.Builder
	b.WriteString("  " + t.Title.Render("Keys") + "\n\n")
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("    %s%s%s\n", t.Num.Render(r[0]), strings.Repeat(" ", 12-len([]rune(r[0]))), r[1]))
	}
	b.WriteString("\n    " + t.Dim.Render("? to go back") + "\n")
	return b.String()
}
