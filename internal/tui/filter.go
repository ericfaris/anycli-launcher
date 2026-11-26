package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ericfaris/anycli-launcher/internal/frecency"
	"github.com/ericfaris/anycli-launcher/internal/fuzzy"
)

// Type-to-filter. In filter mode every printable key is filter text
// (digits and q/n/c included), so the menu's single-key commands are off
// until Esc. Matching is case-insensitive fuzzy subsequence on the label;
// results rank by match quality, then frecency, then menu order.

// match is one filtered row.
type match struct {
	idx       int // index into opts.Entries (its menu number is idx+1)
	score     int
	positions []int
}

func (m Model) startFilter(text string) Model {
	m.filtering, m.filter, m.fcursor = true, text, 0
	m.input, m.help = "", false
	return m
}

func (m Model) stopFilter() Model {
	m.filtering, m.filter, m.fcursor = false, "", 0
	return m
}

func (m Model) filterKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m.finish(Result{Action: ActQuit})
	case "esc":
		return m.stopFilter(), nil
	case "backspace":
		r := []rune(m.filter)
		if len(r) <= 1 {
			return m.stopFilter(), nil
		}
		m.filter, m.fcursor = string(r[:len(r)-1]), 0
		return m, nil
	case "up", "ctrl+p", "shift+tab":
		if m.fcursor > 0 {
			m.fcursor--
		}
		return m, nil
	case "down", "ctrl+n", "tab":
		if m.fcursor < len(m.matches())-1 {
			m.fcursor++
		}
		return m, nil
	case "enter":
		ms := m.matches()
		if len(ms) == 0 {
			m.status = fmt.Sprintf("nothing matches %q", m.filter)
			return m, nil
		}
		return m.finish(Result{Action: ActSelect, Entry: m.opts.Entries[ms[m.fcursor].idx]})
	}
	if (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) && !k.Alt {
		m.filter += string(k.Runes)
		if k.Type == tea.KeySpace && len(k.Runes) == 0 {
			m.filter += " "
		}
		m.fcursor = 0
	}
	return m, nil
}

// matches ranks the entries against the filter text.
func (m Model) matches() []match {
	var out []match
	for i, e := range m.opts.Entries {
		if s, pos, ok := fuzzy.Match(m.filter, e.Label); ok {
			out = append(out, match{idx: i, score: s, positions: pos})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].score != out[b].score {
			return out[a].score > out[b].score
		}
		fa := frecency.Lookup(m.opts.Scores, m.opts.Entries[out[a].idx].Dir)
		fb := frecency.Lookup(m.opts.Scores, m.opts.Entries[out[b].idx].Dir)
		return fa > fb
	})
	return out
}

func (m Model) filterView() string {
	t := m.opts.Theme
	var b strings.Builder
	ms := m.matches()
	for i, mt := range ms {
		e := m.opts.Entries[mt.idx]
		mark := "  "
		if i == m.fcursor {
			mark = t.Cursor.Render("▸") + " "
		}
		num := t.Dim.Render(fmt.Sprintf("%2d", mt.idx+1))
		base := t.Item
		switch {
		case e.Kind != KindProject:
			base = t.Accent
		case i == m.fcursor:
			base = t.Cursor
		}
		label := highlight(e.Label, mt.positions, base.Render, t.Match.Render)
		if e.Kind != KindProject {
			label = t.Accent.Render("● ") + label
		}
		switch e.Kind {
		case KindRoot:
			label += t.Accent.Render(" (root)")
		case KindConfig:
			label += t.Accent.Render(" (config)")
		}
		line := " " + mark + num + "  " + label
		if g := m.git[e.Dir].String(); g != "" && (e.Kind == KindProject || e.Kind == KindConfig) {
			line += "  " + t.Dim.Render(g)
		}
		b.WriteString(line + "\n")
	}
	if len(ms) == 0 {
		b.WriteString("     " + t.Dim.Render("no matches") + "\n")
	}
	b.WriteString("\n")
	keys := []string{"Enter open", "↑↓ move", "Esc clear"}
	var parts []string
	for _, k := range keys {
		key, desc, _ := strings.Cut(k, " ")
		parts = append(parts, t.Num.Render(key)+" "+desc)
	}
	b.WriteString("    " + strings.Join(parts, "   ") + "\n")
	return b.String()
}

// highlight renders label with the runes at positions in hi and the rest in
// base, grouping runs so each style is applied once per run.
func highlight(label string, positions []int, base, hi func(...string) string) string {
	marked := make(map[int]bool, len(positions))
	for _, p := range positions {
		marked[p] = true
	}
	var b strings.Builder
	var run []rune
	cur := false
	flush := func() {
		if len(run) == 0 {
			return
		}
		if cur {
			b.WriteString(hi(string(run)))
		} else {
			b.WriteString(base(string(run)))
		}
		run = run[:0]
	}
	for i, r := range []rune(label) {
		if marked[i] != cur {
			flush()
			cur = marked[i]
		}
		run = append(run, r)
	}
	flush()
	return b.String()
}
