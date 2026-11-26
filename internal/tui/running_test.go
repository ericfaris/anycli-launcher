package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
)

func taskRows() []TaskRow {
	return []TaskRow{
		{ID: "api/fix-login", Branch: "ac/fix-login", Task: "Fix the login bug when the session cookie expires mid-request and the user gets bounced", Age: "2h", Status: "running", Git: "dirty ↑2 (2 unpushed)", Backend: "tmux"},
		{ID: "web/add-tests", Branch: "ac/add-tests", Task: "add tests", Age: "3d", Status: "idle", Git: "clean", Backend: "tmux"},
		{ID: "work-api/x", Branch: "ac/x", Age: "45s", Status: "finished", Git: "↑1", Backend: "here"},
		{ID: "cli/gone", Branch: "ac/gone", Task: "old", Age: "9d", Status: "unknown", Git: "worktree missing", Backend: "tab"},
	}
}

func taskModel(rows []TaskRow) Model {
	m := model(3, "default", termenv.Ascii)
	m.opts.TaskCount = len(rows)
	m.opts.LoadTasks = func() []TaskRow { return rows }
	return m
}

// enterRunning presses w and delivers the async rows, like Bubble Tea would.
func enterRunning(t *testing.T, m Model) Model {
	t.Helper()
	r, cmd := send(t, m, "w")
	if !r.running || cmd == nil {
		t.Fatal("w must open the running view and load tasks")
	}
	tm, _ := r.Update(cmd())
	return tm.(Model)
}

func TestGoldenRunning(t *testing.T) {
	m := taskModel(taskRows())
	golden(t, "menu_tasks", m.View())

	loading, _ := send(t, m, "w")
	golden(t, "running_loading", loading.View())

	r := enterRunning(t, m)
	golden(t, "running", r.View())
	d, _ := send(t, r, "down", "down")
	golden(t, "running_cursor", d.View())

	empty := enterRunning(t, taskModel(nil))
	golden(t, "running_empty", empty.View())

	bad, _ := send(t, r, "9")
	golden(t, "running_no_task", bad.View())
}

func TestRunningKeys(t *testing.T) {
	m := taskModel(taskRows())
	r := enterRunning(t, m)

	cases := []struct {
		keys   []string
		action Action
		task   string
	}{
		{[]string{"enter"}, ActTaskOpen, "api/fix-login"},
		{[]string{"down", "enter"}, ActTaskOpen, "web/add-tests"},
		{[]string{"j", "j", "k", "enter"}, ActTaskOpen, "web/add-tests"},
		{[]string{"3"}, ActTaskOpen, "work-api/x"}, // unambiguous: at once
		{[]string{"down", "f"}, ActTaskFinish, "web/add-tests"},
		{[]string{"q"}, ActQuit, ""},
		{[]string{"ctrl+c"}, ActQuit, ""},
	}
	for _, c := range cases {
		got, _ := send(t, r, c.keys...)
		if !got.done || got.Result().Action != c.action || got.Result().Task != c.task {
			t.Errorf("%v: done=%v %+v", c.keys, got.done, got.Result())
		}
	}
	// Cursor stays in bounds.
	b, _ := send(t, r, "up", "down", "down", "down", "down", "down", "down")
	if b.tcursor != 3 {
		t.Fatalf("cursor %d", b.tcursor)
	}
	// Esc and w go back to the menu, which still works.
	for _, k := range []string{"esc", "w"} {
		back, _ := send(t, r, k)
		if back.running || back.done {
			t.Fatalf("%s: running=%v done=%v", k, back.running, back.done)
		}
		sel, _ := send(t, back, "4")
		if sel.Result().Entry.Label != "alpha" {
			t.Fatalf("%s then 4: %+v", k, sel.Result())
		}
	}
	// r reloads.
	again, cmd := send(t, r, "r")
	if cmd == nil || again.tasksLoaded {
		t.Fatal("r must reload")
	}
	// Nothing to open or finish while loading or empty.
	loading, _ := send(t, m, "w", "enter")
	if loading.done {
		t.Fatal("enter before rows arrived")
	}
	empty := enterRunning(t, taskModel(nil))
	for _, k := range []string{"enter", "f", "1"} {
		if e, _ := send(t, empty, k); e.done {
			t.Fatalf("%s on empty view", k)
		}
	}
	// No loader: an empty view, not a crash.
	nl := model(3, "default", termenv.Ascii)
	if r := enterRunning(t, nl); !r.tasksLoaded || len(r.tasks) != 0 {
		t.Fatal("nil LoadTasks")
	}
}

// Many tasks: "1" waits for a second digit like the menu does.
func TestRunningNumbers(t *testing.T) {
	var rows []TaskRow
	for i := 0; i < 12; i++ {
		rows = append(rows, TaskRow{ID: "p/t" + string(rune('a'+i)), Status: "running"})
	}
	r := enterRunning(t, taskModel(rows))
	w, _ := send(t, r, "1")
	if w.done {
		t.Fatal("1 must wait")
	}
	w, _ = send(t, w, "2")
	if w.Result().Task != "p/tl" {
		t.Fatalf("12: %+v", w.Result())
	}
	w, _ = send(t, r, "1", "enter")
	if w.Result().Task != "p/ta" {
		t.Fatalf("1 enter: %+v", w.Result())
	}
	w, _ = send(t, r, "1", "esc")
	if w.done || !w.running || w.input != "" {
		t.Fatal("esc clears the number first")
	}
	w, _ = send(t, r, "0")
	if w.done || w.input != "" {
		t.Fatal("leading 0 ignored")
	}
}

// Phase 2 regression: names starting with w still filter. "w" then a
// letter that isn't a running-view key becomes filter text "w"+letter.
func TestWStillFilters(t *testing.T) {
	m := New(Options{Entries: []Entry{
		{Kind: KindProject, Label: "web", Dir: "/p/web"},
		{Kind: KindProject, Label: "api", Dir: "/p/api"},
	}, Theme: NewTheme("default", renderer(termenv.Ascii))})
	r, _ := send(t, m, "w", "e", "b")
	if !r.filtering || r.filter != "web" || r.running {
		t.Fatalf("filtering=%v filter=%q running=%v", r.filtering, r.filter, r.running)
	}
	r, _ = send(t, r, "enter")
	if r.Result().Entry.Label != "web" {
		t.Fatalf("%+v", r.Result())
	}
	// The fallback is only for the first key after w.
	r, _ = send(t, m, "w", "down", "e")
	if r.filtering || !r.running {
		t.Fatal("only the first key falls back to the filter")
	}
	// Uppercase W still starts the filter directly (phase 2).
	r, _ = send(t, m, "W")
	if !r.filtering || r.filter != "W" {
		t.Fatalf("W: %q", r.filter)
	}
	// "/w" filters for w explicitly.
	r, _ = send(t, m, "/", "w", "f")
	if !r.filtering || r.filter != "wf" {
		t.Fatalf("/wf: %q", r.filter)
	}
	// w while typing a number or prefix is not the running view.
	r, _ = send(t, m, "x", "w")
	if r.running {
		t.Fatal("x then w")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("a  b\n c", 10) != "a b c" {
		t.Fatal("whitespace")
	}
	if got := truncate(strings.Repeat("é", 20), 5); got != "éééé…" {
		t.Fatal(got)
	}
}

var _ tea.Model = Model{}
