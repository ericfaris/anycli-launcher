package launch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestZellijLayout(t *testing.T) {
	got := ZellijLayout(`/p/my "app"`, "T\n1", []string{"/bin/claude", "--x", `say "hi"; $(rm)`})
	want := `layout {
    default_tab_template {
        pane size=1 borderless=true {
            plugin location="zellij:tab-bar"
        }
        children
        pane size=2 borderless=true {
            plugin location="zellij:status-bar"
        }
    }
    tab name="T1" cwd="/p/my \"app\"" {
        pane command="/bin/claude" cwd="/p/my \"app\"" {
            args "--x" "say \"hi\"; $(rm)"
        }
    }
}
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	bare := ZellijLayout("/p", "", []string{"/bin/c"})
	if strings.Contains(bare, "args") || strings.Contains(bare, "name=") || !strings.Contains(bare, `pane command="/bin/c" cwd="/p"`+"\n") {
		t.Fatal(bare)
	}
}

func TestPlanZellij(t *testing.T) {
	cases := []struct {
		name                   string
		exists, inside, resume bool
		want                   ZellijPlan
	}{
		{"new outside", false, false, false, ZellijPlan{Attach: []string{"--session", "s", "--new-session-with-layout", "L"}}},
		{"exists fresh", true, false, false, ZellijPlan{Setup: [][]string{{"--session", "s", "action", "new-tab", "--layout", "L"}}, Attach: []string{"attach", "s"}}},
		{"exists resume", true, false, true, ZellijPlan{Attach: []string{"attach", "s"}}},
		{"new resume", false, false, true, ZellijPlan{Attach: []string{"--session", "s", "--new-session-with-layout", "L"}}},
	}
	for _, c := range cases {
		if got := PlanZellij("s", "L", c.exists, c.inside, c.resume); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	in := PlanZellij("s", "L", true, true, false)
	if in.Attach != nil || in.Note == "" || !reflect.DeepEqual(in.Setup, [][]string{{"action", "new-tab", "--layout", "L"}}) {
		t.Fatalf("inside: %+v", in)
	}
}

func TestZellijHasSession(t *testing.T) {
	out := "\x1b[32;1mother\x1b[m [Created 1h ago]\napp-1\nmy-app (EXITED)\n"
	if !ZellijHasSession(out, "other") || !ZellijHasSession(out, "my-app") || ZellijHasSession(out, "app") || ZellijHasSession("", "app") {
		t.Fatal("parse")
	}
}

func TestZellijLaunch(t *testing.T) {
	tmp := t.TempDir()
	var layoutSeen string
	r := &fakeRunner{out: func(c Command) (string, int, error) {
		if c.Args[0] == "list-sessions" {
			return "app\n", 0, nil
		}
		layoutSeen = c.Args[len(c.Args)-1]
		b, _ := os.ReadFile(layoutSeen)
		if !strings.Contains(string(b), `pane command="/bin/claude"`) {
			t.Errorf("layout %s", b)
		}
		return "", 0, nil
	}}
	d := Deps{
		LookPath: func(n string) (string, error) { return "/bin/" + n, nil },
		Runner:   r, Env: envOf("linux", nil, ""), TempDir: tmp,
	}
	cd := filepath.Join(t.TempDir(), "cd")
	if _, err := (&Zellij{Deps: d}).Launch(Request{Dir: "/p/app", Name: "claude", Session: "app", CDFile: cd}); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 1 || !reflect.DeepEqual(r.runs[0].Args, []string{"attach", "app"}) {
		t.Fatalf("runs %v", r.runs)
	}
	if _, err := os.Stat(layoutSeen); err == nil {
		t.Fatal("layout not removed")
	}
	if b, _ := os.ReadFile(cd); string(b) != "/p/app" {
		t.Fatal("cd-file")
	}
	// Missing zellij: unavailable.
	d.LookPath = func(n string) (string, error) { return "", errors.New("no") }
	if _, err := (&Zellij{Deps: d}).Launch(Request{Dir: "/p", Name: "claude"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
