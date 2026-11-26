package clis

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		``:                          nil,
		`   `:                       nil,
		`--a --b`:                   {"--a", "--b"},
		`--a   --b`:                 {"--a", "--b"},
		`--model=claude --effort=x`: {"--model=claude", "--effort=x"},
		`--agent "my agent"`:        {"--agent", "my agent"},
		`--x 'it is'`:               {"--x", "it is"},
		`"a\"b"`:                    {`a"b`},
		`'a\b'`:                     {`a\b`},
		`a\ b`:                      {"a b"},
		`""`:                        {""},
		`--p "$(rm -rf /)"`:         {"--p", "$(rm -rf /)"},
		"tab\tsep":                  {"tab", "sep"},
	}
	for in, want := range cases {
		if got := SplitArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	claude, _ := Lookup("claude")
	kiro, _ := Lookup("kiro-cli")
	aider, _ := Lookup("aider")
	unknown, known := Lookup("mycli")
	if known {
		t.Fatal("mycli should be unknown")
	}
	cases := []struct {
		name    string
		p       Profile
		launch  string
		flags   string
		prompt  string
		mode    Mode
		want    []string
		noteHas string
	}{
		{"claude fresh", claude, "", "--model opus", "hi there", Fresh, []string{"--model", "opus", "hi there"}, ""},
		{"claude resume drops prompt", claude, "", "--x", "hi", Resume, []string{"--continue", "--x"}, ""},
		{"claude picker", claude, "", "", "hi", Picker, []string{"--resume"}, ""},
		{"kiro default subcmd", kiro, "", "--trust-all-tools", "", Fresh, []string{"chat", "--trust-all-tools"}, ""},
		{"kiro launch override", kiro, "chat --v", "", "", Resume, []string{"chat", "--v", "--resume"}, ""},
		{"aider no prompt", aider, "", "", "hi", Fresh, nil, "no initial prompt"},
		{"aider no resume keeps prompt rule", aider, "", "", "", Resume, nil, "no session resume support"},
		{"unknown positional", unknown, "", "", "hi", Fresh, []string{"hi"}, ""},
		{"unknown picker", unknown, "", "", "hi", Picker, []string{"hi"}, "picker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := c.p.Build(c.launch, c.flags, c.prompt, c.mode)
			if !reflect.DeepEqual(inv.Args, c.want) {
				t.Fatalf("args %q want %q", inv.Args, c.want)
			}
			notes := strings.Join(inv.Notes, "\n")
			if c.noteHas == "" && notes != "" || c.noteHas != "" && !strings.Contains(notes, c.noteHas) {
				t.Fatalf("notes %q want %q", notes, c.noteHas)
			}
		})
	}
}

func TestProfilesTable(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Profiles {
		if p.Name == "" || seen[p.Name] {
			t.Fatalf("bad or duplicate profile %q", p.Name)
		}
		seen[p.Name] = true
	}
	for _, n := range []string{"claude", "kiro-cli", "aider", "goose", "codex", "amp"} {
		if !Known(n) {
			t.Errorf("v3 CLI %s missing", n)
		}
	}
	if c, _ := Lookup("claude"); c.Verified == "" {
		t.Error("claude flags were verified; keep the note")
	}
}

func TestDetect(t *testing.T) {
	look := func(n string) (string, error) {
		if n == "codex" || n == "claude" {
			return "/bin/" + n, nil
		}
		return "", errors.New("nope")
	}
	if got := Detect(look); !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Fatalf("got %v", got)
	}
}
