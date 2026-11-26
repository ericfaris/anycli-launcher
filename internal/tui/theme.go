package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Theme is the set of styles the menu uses.
type Theme struct {
	Name   string
	Title  lipgloss.Style // header box and app name
	Dim    lipgloss.Style // versions, hints, git status
	Num    lipgloss.Style // entry numbers and footer keys
	Accent lipgloss.Style // terminal/root/config entries
	Item   lipgloss.Style // project labels
	Cursor lipgloss.Style // the cursor marker and selected label
	Prompt lipgloss.Style // input prompt
	Error  lipgloss.Style // status messages
	Match  lipgloss.Style // filter: the characters that matched
}

// Presets are the built-in theme names, in display order.
var Presets = []string{"default", "mono", "dusk"}

// NewTheme builds a preset (unknown names fall back to "default") on r,
// which fixes the color profile (tests pass a renderer with a known one).
func NewTheme(name string, r *lipgloss.Renderer) Theme {
	if r == nil {
		r = lipgloss.DefaultRenderer()
	}
	s := r.NewStyle
	switch name {
	case "mono":
		return Theme{
			Name:   "mono",
			Title:  s().Bold(true),
			Dim:    s().Faint(true),
			Num:    s().Bold(true),
			Accent: s(),
			Item:   s(),
			Cursor: s().Bold(true).Underline(true),
			Prompt: s().Bold(true),
			Error:  s().Bold(true),
			Match:  s().Bold(true).Underline(true),
		}
	case "dusk":
		return Theme{
			Name:   "dusk",
			Title:  s().Foreground(lipgloss.Color("#c4a7e7")).Bold(true),
			Dim:    s().Foreground(lipgloss.Color("#6e6a86")),
			Num:    s().Foreground(lipgloss.Color("#f6c177")).Bold(true),
			Accent: s().Foreground(lipgloss.Color("#9ccfd8")),
			Item:   s().Foreground(lipgloss.Color("#e0def4")),
			Cursor: s().Foreground(lipgloss.Color("#eb6f92")).Bold(true),
			Prompt: s().Foreground(lipgloss.Color("#31748f")).Bold(true),
			Error:  s().Foreground(lipgloss.Color("#eb6f92")),
			Match:  s().Foreground(lipgloss.Color("#f6c177")).Bold(true).Underline(true),
		}
	default:
		// v3's palette: bold cyan title, yellow numbers, cyan accents.
		return Theme{
			Name:   "default",
			Title:  s().Foreground(lipgloss.Color("6")).Bold(true),
			Dim:    s().Faint(true),
			Num:    s().Foreground(lipgloss.Color("3")).Bold(true),
			Accent: s().Foreground(lipgloss.Color("6")),
			Item:   s().Foreground(lipgloss.Color("7")),
			Cursor: s().Foreground(lipgloss.Color("2")).Bold(true),
			Prompt: s().Foreground(lipgloss.Color("2")).Bold(true),
			Error:  s().Foreground(lipgloss.Color("1")),
			Match:  s().Foreground(lipgloss.Color("3")).Bold(true).Underline(true),
		}
	}
}

// ValidTheme reports whether name is a preset.
func ValidTheme(name string) bool {
	for _, p := range Presets {
		if p == name {
			return true
		}
	}
	return false
}
