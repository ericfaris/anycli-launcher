package launch

import (
	"regexp"
	"strconv"
	"strings"
)

// Quoting helpers, one per target language. Project names, titles, prompts
// and flags are user data and may contain anything (quotes, ;, $(), newlines).
// Wherever possible a backend passes them as separate argv elements, which
// needs no quoting at all; these helpers cover the places where a value has
// to be embedded in another language's text.

// ShQuote quotes s as a single POSIX shell word: wrapped in single quotes,
// each embedded single quote closed, backslash-escaped and reopened.
// The same form is safe in fish, which only treats \' and \\ specially
// inside single quotes; callers that target fish must also reject
// backslashes (see SafePath).
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// AppleScriptString quotes s as an AppleScript string literal.
func AppleScriptString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// KDLString quotes s as a KDL (v1) string literal for zellij layouts.
func KDLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteString(`\u{` + strconv.FormatInt(int64(r), 16) + `}`)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TmuxFormatEscape escapes '#' for tmux options that are expanded as
// formats. tmux expands -c (start directory) and -n (window name) as
// formats, so a directory named "x#(cmd)" would run cmd and "#{...}" would
// be substituted. "##" is tmux's literal '#'. Verified against tmux 3.4.
func TmuxFormatEscape(s string) string { return strings.ReplaceAll(s, "#", "##") }

// stripControl removes C0 control characters and DEL.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// WTEscape prepares one argument for wt.exe: wt splits its command line
// into subcommands at every unescaped ';', so ';' becomes '\;'.
func WTEscape(s string) string { return strings.ReplaceAll(s, ";", `\;`) }

// WTTitle sanitizes a tab title for wt.exe. Besides ';', it drops control
// characters, double quotes and backslashes: from WSL the argument crosses
// the Linux-to-Windows interop layer, which re-quotes it for a Windows
// command line, and a quote or a trailing backslash there can end the
// argument early.
func WTTitle(s string) string {
	s = stripControl(s)
	s = strings.NewReplacer(`"`, "", `\`, "").Replace(s)
	return WTEscape(s)
}

var safePathRe = regexp.MustCompile(`^[A-Za-z0-9/._+@:~-]+$`)

// SafePath reports whether p can be placed on a terminal's command line
// (wt.exe via WSL interop, osascript "write text") without any quoting
// layer being able to change its meaning. The trampoline's own binary and
// spec file paths must pass; user data never goes through this route.
func SafePath(p string) bool { return safePathRe.MatchString(p) }

var unsafeWSLDir = regexp.MustCompile("[\"\\\\;%^&|<>`$\x00-\x1f\x7f]")

// WSLCdArg is the directory for `wsl.exe --cd`. A directory that could be
// misparsed on its way through wt.exe and the interop layer is replaced by
// "~"; the trampoline changes into the real directory itself either way.
func WSLCdArg(dir string) string {
	if dir == "" || unsafeWSLDir.MatchString(dir) {
		return "~"
	}
	return dir
}
