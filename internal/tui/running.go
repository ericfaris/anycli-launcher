package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The running view ("w"): task sessions (ac <project> --task) with their
// status and git state. Keys:
//
//	↑ ↓ / j k     move
//	<N> / Enter   open task N / the selected one (attach to its tmux or
//	              zellij session, else cd into the worktree)
//	f             finish the selected task (ac finish, which asks first)
//	r             refresh
//	Esc / w       back to the menu      q  quit
//
// "w" used to start the filter with a w. To keep typing names that start
// with w, the first key after "w" that is a letter but not one of the keys
// above (j k f r q w) goes back to the menu filter with "w" plus that
// letter, so "web" still filters. "wj", "wk", "wf", "wr", "wq", "ww" and
// "w<digit>" need "/" first (e.g. "/wf").

// TaskRow is one task session in the running view (already sanitized).
type TaskRow struct {
	ID, Branch, Task, Age string
	Status               string // running, idle, finished, unknown
	Git                  string // "clean", "dirty ↑2", "worktree missing", ...
	Backend              string
}

// TasksMsg delivers the running view's rows.
type TasksMsg []TaskRow

// runningKeys are the view's letter commands (see the fallback above).
const runningKeys = "jkfrqw"

func (m Model) loadTasks() tea.Cmd {
	fn := m.opts.LoadTasks
	if fn == nil {
		return func() tea.Msg { return TasksMsg(nil) }
	}
	return func() tea.Msg { return TasksMsg(fn()) }
}

func (m Model) startRunning() (Model, tea.Cmd) {
	m.running, m.runFirst = true, true
	m.tasks, m.tasksLoaded, m.tcursor, m.input, m.help = nil, false, 0, "", false
	return m, m.loadTasks()
}

func (m Model) runningKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	first := m.runFirst
	m.runFirst = false
	s := k.String()
	switch s {
	case "ctrl+c":
		return m.finish(Result{Action: ActQuit})
	case "esc":
		if m.input != "" {
			m.input = ""
			return m, nil
		}
		m.running = false
		return m, nil
	case "up", "k":
		if m.tcursor > 0 {
			m.tcursor--
		}
		return m, nil
	case "down", "j":
		if m.tcursor < len(m.tasks)-1 {
			m.tcursor++
		}
		return m, nil
	case "backspace":
		if m.input != "" {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case "enter":
		if m.input != "" {
			return m.commitTask()
		}
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.finish(Result{Action: ActTaskOpen, Task: m.tasks[m.tcursor].ID})
	}
	if len(s) != 1 {
		return m, nil
	}
	c := s[0]
	switch {
	case c >= '0' && c <= '9':
		if !m.tasksLoaded {
			return m, nil
		}
		m.input += s
		n, _ := strconv.Atoi(m.input)
		if n == 0 {
			m.input = ""
			return m, nil
		}
		if n*10 > len(m.tasks) {
			return m.commitTask()
		}
		return m, nil
	case m.input != "":
		return m, nil
	case c == 'w':
		m.running = false
		return m, nil
	case c == 'q':
		return m.finish(Result{Action: ActQuit})
	case c == 'r':
		return m.startRunning()
	case c == 'f':
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.finish(Result{Action: ActTaskFinish, Task: m.tasks[m.tcursor].ID})
	case first && isLetter(c) && !strings.ContainsRune(runningKeys, rune(c)):
		// "we..." is a name being typed (see the package comment).
		m.running = false
		return m.startFilter("w" + s), nil
	}
	return m, nil
}

func (m Model) commitTask() (tea.Model, tea.Cmd) {
	n, _ := strconv.Atoi(m.input)
	m.input = ""
	if n < 1 || n > len(m.tasks) {
		m.status = fmt.Sprintf("no task %d", n)
		return m, nil
	}
	return m.finish(Result{Action: ActTaskOpen, Task: m.tasks[n-1].ID})
}

func (m Model) runningView() string {
	t := m.opts.Theme
	var b strings.Builder
	b.WriteString("  " + t.Title.Render("Task sessions") + "\n\n")
	switch {
	case !m.tasksLoaded:
		b.WriteString("     " + t.Dim.Render("checking task sessions...") + "\n")
	case len(m.tasks) == 0:
		b.WriteString("     " + t.Dim.Render(`none yet: ac <project> --task "..."`) + "\n")
	}
	idW := 0
	for _, r := range m.tasks {
		if n := len([]rune(r.ID)); n > idW {
			idW = n
		}
	}
	for i, r := range m.tasks {
		mark := "  "
		id := t.Item.Render(r.ID)
		if i == m.tcursor {
			mark = t.Cursor.Render("▸") + " "
			id = t.Cursor.Render(r.ID)
		}
		pad := strings.Repeat(" ", idW-len([]rune(r.ID)))
		st := fmt.Sprintf("%-8s", r.Status)
		switch r.Status {
		case "running":
			st = t.Accent.Render(st)
		case "idle":
			st = t.Num.Render(st)
		default:
			st = t.Dim.Render(st)
		}
		line := " " + mark + t.Num.Render(fmt.Sprintf("%2d", i+1)) + "  " + id + pad + "  " + st + "  " +
			t.Dim.Render(fmt.Sprintf("%4s", r.Age)) + "  " + t.Dim.Render(r.Git)
		b.WriteString(line + "\n")
		if r.Task != "" {
			b.WriteString("        " + t.Dim.Render(truncate(r.Task, 56)) + "\n")
		}
	}
	b.WriteString("\n")
	keys := []string{"Enter open", "f finish", "r refresh", "Esc back"}
	var parts []string
	for _, k := range keys {
		key, desc, _ := strings.Cut(k, " ")
		parts = append(parts, t.Num.Render(key)+" "+desc)
	}
	b.WriteString("    " + strings.Join(parts, "   ") + "\n")
	return b.String()
}

// truncate shortens s to n runes with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
