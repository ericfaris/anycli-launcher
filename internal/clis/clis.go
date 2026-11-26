// Package clis holds declarative profiles for the AI CLIs the launcher
// knows, and builds their argv.
package clis

import (
	"fmt"
	"os/exec"
)

// PromptStyle says how an initial prompt is passed.
type PromptStyle int

const (
	// PromptPositional appends the prompt as the last argument.
	PromptPositional PromptStyle = iota
	// PromptNone means the CLI has no interactive initial-prompt argument;
	// the prompt is dropped with a note.
	PromptNone
)

// Mode selects a fresh session, resume-most-recent, or the session picker.
type Mode int

const (
	Fresh Mode = iota
	Resume
	Picker
)

func (m Mode) String() string {
	switch m {
	case Resume:
		return "resume"
	case Picker:
		return "picker"
	}
	return "fresh"
}

// Profile describes one CLI.
type Profile struct {
	Name string
	// Launch is the default subcommand (e.g. kiro-cli "chat").
	Launch []string
	// Resume continues the most recent session; Picker opens the CLI's own
	// session picker. Empty means unsupported.
	Resume []string
	Picker []string
	// ListSessions lists sessions as JSON (used by a later phase).
	ListSessions []string
	// ConfigDir is the CLI's settings folder ("~" unexpanded), shown as a
	// menu shortcut when it exists.
	ConfigDir string
	Prompt    PromptStyle
	// Verified records where the flags above were checked. Empty means they
	// come from docs/memory and have not been checked against a real --help.
	Verified string
}

// Profiles is the built-in table, in detection/offer order.
var Profiles = []Profile{
	{
		Name: "claude", Resume: []string{"--continue"}, Picker: []string{"--resume"},
		ConfigDir: "~/.claude", Prompt: PromptPositional,
		// `claude --help` (2.1.332): "-c, --continue  Continue the most recent
		// conversation in the current directory"; "-r, --resume [value] ...
		// or pick from a list"; "Arguments: prompt  Your prompt".
		Verified: "claude --help, 2.1.332",
	},
	{
		// From v3's capability table (kiro-cli was v3's original target);
		// not re-checked: no local install.
		Name: "kiro-cli", Launch: []string{"chat"}, Resume: []string{"--resume"}, Picker: []string{"--resume-picker"},
		ListSessions: []string{"chat", "--list-sessions", "--format", "json"},
		ConfigDir:    "~/.kiro", Prompt: PromptPositional,
	},
	{
		// UNVERIFIED: `codex resume --last` / `codex resume` from codex docs;
		// the local install is a Windows npm shim that does not run in WSL.
		Name: "codex", Resume: []string{"resume", "--last"}, Picker: []string{"resume"},
		ConfigDir: "~/.codex", Prompt: PromptPositional,
	},
	{
		// UNVERIFIED (no local install). aider's positional args are files, so
		// a prompt must not be passed positionally.
		Name: "aider", Prompt: PromptNone,
	},
	{
		// UNVERIFIED: subcommand and --resume from v3's table.
		Name: "goose", Launch: []string{"session"}, Resume: []string{"--resume"}, Prompt: PromptNone,
	},
	{
		// UNVERIFIED (no local install).
		Name: "amp", Prompt: PromptNone,
	},
}

// Lookup returns the profile for name. Unknown CLIs get a generic profile
// that passes the prompt positionally (v3 behavior for every tool).
func Lookup(name string) (Profile, bool) {
	for _, p := range Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{Name: name, Prompt: PromptPositional}, false
}

// Known reports whether name is a built-in profile.
func Known(name string) bool {
	_, ok := Lookup(name)
	return ok
}

// LookPathFunc matches exec.LookPath; injectable for tests.
type LookPathFunc func(string) (string, error)

// Detect returns the built-in CLIs found on PATH, in table order.
func Detect(look LookPathFunc) []string {
	if look == nil {
		look = exec.LookPath
	}
	var found []string
	for _, p := range Profiles {
		if _, err := look(p.Name); err == nil {
			found = append(found, p.Name)
		}
	}
	return found
}

// Invocation is what to run, minus the binary lookup.
type Invocation struct {
	Name string
	Args []string
	// Notes are user-facing messages (e.g. "no resume support").
	Notes []string
}

// Build assembles the argv. Order (as v3): launch subcommand, resume/picker
// args, flags, prompt. launch overrides the profile's subcommand when
// non-empty. Resuming drops the prompt so a session is not re-seeded.
func (p Profile) Build(launch, flags, prompt string, mode Mode) Invocation {
	inv := Invocation{Name: p.Name}
	if launch != "" {
		inv.Args = append(inv.Args, SplitArgs(launch)...)
	} else {
		inv.Args = append(inv.Args, p.Launch...)
	}
	if mode != Fresh {
		extra := p.Resume
		if mode == Picker {
			extra = p.Picker
		}
		if len(extra) > 0 {
			inv.Args = append(inv.Args, extra...)
			prompt = ""
		} else {
			inv.Notes = append(inv.Notes, fmt.Sprintf("%s has no session %s support; starting a fresh session", p.Name, mode))
		}
	}
	inv.Args = append(inv.Args, SplitArgs(flags)...)
	if prompt != "" {
		if p.Prompt == PromptPositional {
			inv.Args = append(inv.Args, prompt)
		} else {
			inv.Notes = append(inv.Notes, fmt.Sprintf("%s takes no initial prompt argument; prompt not sent", p.Name))
		}
	}
	return inv
}

// SplitArgs splits a flag string honoring single quotes, double quotes
// (with backslash escapes) and bare backslash escapes, without any shell
// evaluation (port of v3 _ac_split_flags).
func SplitArgs(s string) []string {
	var out []string
	var cur []rune
	inTok := false
	var quote rune
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if quote != 0 {
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && quote == '"' && i+1 < len(rs):
				i++
				cur = append(cur, rs[i])
			default:
				cur = append(cur, c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote, inTok = c, true
		case '\\':
			if i+1 < len(rs) {
				i++
				cur = append(cur, rs[i])
			}
			inTok = true
		case ' ', '\t', '\n':
			if inTok {
				out = append(out, string(cur))
				cur, inTok = cur[:0], false
			}
		default:
			cur = append(cur, c)
			inTok = true
		}
	}
	if inTok {
		out = append(out, string(cur))
	}
	return out
}
