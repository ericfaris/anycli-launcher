package launch

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// hostile values every quoting path must carry literally.
var hostile = []string{
	`plain`,
	`two words`,
	`it's`,
	`"double"`,
	`a;b`,
	`$(touch pwned)`,
	"`touch pwned`",
	`${HOME}`,
	"line1\nline2",
	`back\slash`,
	`trailing\`,
	`#{session_name}#(touch pwned)`,
	`%PATH%`,
	`--flag`,
	`-`,
	``,
	`'; rm -rf / #`,
}

func TestShQuoteRoundTripsThroughSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX sh")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	dir := t.TempDir()
	for _, s := range hostile {
		cmd := exec.Command(sh, "-c", "printf '%s' "+ShQuote(s))
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil || string(out) != s {
			t.Errorf("ShQuote(%q): got %q err %v", s, out, err)
		}
	}
}

func TestAppleScriptString(t *testing.T) {
	cases := map[string]string{
		`plain`:      `"plain"`,
		`say "hi"`:   `"say \"hi\""`,
		`back\slash`: `"back\\slash"`,
		"a\nb":       `"a\nb"`,
	}
	for in, want := range cases {
		if got := AppleScriptString(in); got != want {
			t.Errorf("%q: got %s want %s", in, got, want)
		}
	}
}

func TestKDLString(t *testing.T) {
	cases := map[string]string{
		`plain`:            `"plain"`,
		`say "hi"`:         `"say \"hi\""`,
		`c:\x`:             `"c:\\x"`,
		"a\nb\tc":          `"a\nb\tc"`,
		"bell\x07":         `"bell\u{7}"`,
		`$(x) ; #{y} 'z'`:  `"$(x) ; #{y} 'z'"`,
		"unicode ✓ ünï":    `"unicode ✓ ünï"`,
		"esc\x1b[31m":      `"esc\u{1b}[31m"`,
		"del\x7f":          `"del\u{7f}"`,
		"\"\\\"":           `"\"\\\""`,
		"form\fback\bfeed": `"form\fback\bfeed"`,
	}
	for in, want := range cases {
		if got := KDLString(in); got != want {
			t.Errorf("%q: got %s want %s", in, got, want)
		}
	}
}

func TestTmuxFormatEscape(t *testing.T) {
	if got := TmuxFormatEscape(`a#{b}#(c)##`); got != `a##{b}##(c)####` {
		t.Fatal(got)
	}
}

func TestWTTitleAndEscape(t *testing.T) {
	cases := map[string]string{
		`My Proj`:              `My Proj`,
		`a;b`:                  `a\;b`,
		`"q" \x\`:              `q x`,
		"nl\nx\x1b]0;evil\a":   `nlx]0\;evil`,
		`$(touch pwned) ; ls`:  `$(touch pwned) \; ls`,
		`new-tab -p "x" ; ...`: `new-tab -p x \; ...`,
	}
	for in, want := range cases {
		if got := WTTitle(in); got != want {
			t.Errorf("WTTitle(%q) = %q want %q", in, got, want)
		}
	}
	if WTEscape(`C:\a;b\c`) != `C:\a\;b\c` {
		t.Fatal("WTEscape")
	}
}

func TestSafePathAndWSLCd(t *testing.T) {
	for _, p := range []string{"/usr/local/bin/ac", "/tmp/ac-tab-123.json", "/var/folders/ab/x_y/T/ac-tab-1.json", "/home/e/go/bin/ac"} {
		if !SafePath(p) {
			t.Errorf("SafePath(%q) should be true", p)
		}
	}
	for _, p := range []string{"", "/a b/ac", "/x/it's", `/x/"q"`, "/x/a;b", "/x/$(y)", "/x/a\nb", `/x\y`} {
		if SafePath(p) {
			t.Errorf("SafePath(%q) should be false", p)
		}
	}
	keep := []string{"/home/e/projects/my proj", "/home/e/p/it's", "/home/e/p/a#b"}
	for _, d := range keep {
		if WSLCdArg(d) != d {
			t.Errorf("WSLCdArg(%q) should pass through", d)
		}
	}
	for _, d := range []string{"", `/p/"q"`, "/p/a;b", "/p/%PATH%", "/p/$(x)", "/p/a\nb", `/p/x\`, "/p/a&b", "/p/a|b"} {
		if WSLCdArg(d) != "~" {
			t.Errorf("WSLCdArg(%q) = %q, want ~", d, WSLCdArg(d))
		}
	}
}

func TestStripControl(t *testing.T) {
	if got := stripControl("a\x00b\x1bc\nd\x7fe✓"); got != "abcde✓" {
		t.Fatal(got)
	}
	if strings.ContainsAny(stripControl(strings.Repeat("\x01", 5)), "\x01") {
		t.Fatal("control kept")
	}
}
