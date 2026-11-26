package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
)

func TestFilterGoldens(t *testing.T) {
	m := model(10, "default", termenv.Ascii)

	auto, _ := send(t, m, "e", "t")
	golden(t, "filter_auto", auto.View())

	explicit, _ := send(t, m, "/")
	golden(t, "filter_empty", explicit.View())

	moved, _ := send(t, m, "/", "a", "down", "down")
	golden(t, "filter_cursor", moved.View())

	none, _ := send(t, m, "z", "z", "z", "enter")
	golden(t, "filter_no_match", none.View())

	// Matched characters are highlighted (visible only with color).
	hl, _ := send(t, model(3, "default", termenv.TrueColor), "a", "p")
	golden(t, "filter_highlight", hl.View())
}

func TestFilterEnterLaunchesTopMatch(t *testing.T) {
	m := model(10, "default", termenv.Ascii)
	cases := []struct {
		keys  []string
		label string
	}{
		{[]string{"a", "l", "enter"}, "alpha"},
		{[]string{"/", "a", "p", "i", "enter"}, "work/api"},
		{[]string{"t", "h", "enter"}, "theta"},
		{[]string{"/", "t", "e", "r", "m", "enter"}, "terminal"},
		// Down moves within the matches.
		{[]string{"e", "t", "a", "down", "enter"}, "zeta"},
		// "r" + letter becomes filter text, not r<N>.
		{[]string{"r", "o", "j", "enter"}, "projects"},
		// Uppercase letters that aren't commands filter too.
		{[]string{"G", "enter"}, "gamma"},
	}
	for _, c := range cases {
		r, cmd := send(t, m, c.keys...)
		if cmd == nil || !r.done || r.Result().Action != ActSelect || r.Result().Entry.Label != c.label {
			t.Errorf("%v: got done=%v %+v", c.keys, r.done, r.Result())
		}
	}
}

func TestFilterKeysAreText(t *testing.T) {
	m := model(10, "default", termenv.Ascii)
	// Inside the filter, digits and command letters are text.
	for _, keys := range [][]string{{"/", "4"}, {"a", "q"}, {"a", "n"}, {"a", "c"}, {"a", "x", "1"}, {"a", "?"}, {"a", "j", "k"}} {
		r, _ := send(t, m, keys...)
		if r.done || !r.filtering || r.filter != strings.Join(keys, "")[strings.Count(keys[0], "/"):] {
			t.Errorf("%v: done=%v filtering=%v filter=%q", keys, r.done, r.filtering, r.filter)
		}
	}
	// Space is text.
	r, _ := send(t, m, "a")
	tm, _ := r.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if f := tm.(Model).filter; f != "a " {
		t.Fatalf("space: %q", f)
	}
}

func TestFilterCommandsStillWork(t *testing.T) {
	m := model(3, "default", termenv.Ascii)
	cases := []struct {
		keys   []string
		action Action
	}{
		{[]string{"N"}, ActNew},
		{[]string{"C"}, ActConfig},
		{[]string{"Q"}, ActQuit},
		{[]string{"X", "4"}, ActRemove},
		{[]string{"R", "4"}, ActSelect},
	}
	for _, c := range cases {
		r, _ := send(t, m, c.keys...)
		if !r.done || r.Result().Action != c.action {
			t.Errorf("%v: got done=%v %+v", c.keys, r.done, r.Result())
		}
	}
	// j/k still move the cursor at an empty prompt.
	r, _ := send(t, m, "j", "j", "k")
	if r.filtering || r.cursor != 1 {
		t.Fatalf("j/k: filtering=%v cursor=%d", r.filtering, r.cursor)
	}
}

func TestFilterEscAndBackspace(t *testing.T) {
	m := model(3, "default", termenv.Ascii)
	r, _ := send(t, m, "a", "l", "esc")
	if r.done || r.filtering || r.filter != "" {
		t.Fatalf("esc should clear the filter: %+v", r)
	}
	r, _ = send(t, r, "esc")
	if !r.done || r.Result().Action != ActQuit {
		t.Fatal("second esc quits")
	}
	r, _ = send(t, m, "a", "l", "backspace")
	if !r.filtering || r.filter != "a" {
		t.Fatalf("backspace: %q", r.filter)
	}
	r, _ = send(t, r, "backspace")
	if r.filtering {
		t.Fatal("backspace on the last character leaves filter mode")
	}
	// Back at the normal prompt, numbers select again.
	r, _ = send(t, r, "4")
	if r.Result().Entry.Label != "alpha" {
		t.Fatalf("number after filter: %+v", r.Result())
	}
	// "/" then backspace leaves filter mode too.
	r, _ = send(t, m, "/", "backspace")
	if r.filtering {
		t.Fatal("/ backspace")
	}
	// ctrl+c quits from the filter.
	r, _ = send(t, m, "a", "ctrl+c")
	if !r.done || r.Result().Action != ActQuit {
		t.Fatal("ctrl+c")
	}
	// No match: Enter reports instead of launching.
	r, _ = send(t, m, "z", "z", "enter")
	if r.done || !strings.Contains(r.status, `nothing matches "zz"`) {
		t.Fatalf("no match: %+v", r)
	}
	// Cursor stays within the matches.
	r, _ = send(t, m, "/", "a", "l", "p", "down", "down", "up", "up", "up")
	if r.fcursor != 0 {
		t.Fatalf("fcursor %d", r.fcursor)
	}
}

func TestFilterOpensFromHelp(t *testing.T) {
	r, _ := send(t, model(3, "default", termenv.Ascii), "?", "a")
	if r.help || !r.filtering {
		t.Fatalf("help=%v filtering=%v", r.help, r.filtering)
	}
	r, _ = send(t, model(3, "default", termenv.Ascii), "é")
	if !r.filtering || r.filter != "é" {
		t.Fatalf("non-ASCII letter: %q", r.filter)
	}
	// Alt-modified keys don't start a filter.
	m := model(3, "default", termenv.Ascii)
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true})
	if tm.(Model).filtering {
		t.Fatal("alt+a")
	}
}

func TestFilterRanksByFrecencyOnTies(t *testing.T) {
	es := []Entry{
		{Kind: KindProject, Label: "api-one", Dir: "/p/one"},
		{Kind: KindProject, Label: "api-two", Dir: "/p/two"},
		{Kind: KindProject, Label: "api", Dir: "/p/api"},
	}
	opts := Options{Entries: es, Theme: NewTheme("default", renderer(termenv.Ascii))}
	order := func(m Model) []string {
		var out []string
		for _, mt := range m.matches() {
			out = append(out, m.opts.Entries[mt.idx].Label)
		}
		return out
	}
	m, _ := send(t, New(opts), "a", "p", "i")
	// Better match first; equal matches in menu order.
	if got := strings.Join(order(m), ","); got != "api,api-one,api-two" {
		t.Fatalf("no history: %s", got)
	}
	opts.Scores = map[string]float64{filepath.Clean("/p/two"): 4, filepath.Clean("/p/api"): 1}
	m, _ = send(t, New(opts), "a", "p", "i")
	// Match quality still beats frecency; frecency breaks the tie.
	if got := strings.Join(order(m), ","); got != "api,api-two,api-one" {
		t.Fatalf("with history: %s", got)
	}
	// Enter launches the top match.
	m, _ = send(t, New(opts), "a", "p", "i", "-", "enter")
	if m.Result().Entry.Label != "api-two" {
		t.Fatalf("enter: %+v", m.Result())
	}
}
