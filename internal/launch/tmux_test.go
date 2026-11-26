package launch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSessionName(t *testing.T) {
	cases := map[string]string{
		"app":      "app",
		"my-app_2": "my-app_2",
	}
	for in, want := range cases {
		if got := SessionName(in); got != want {
			t.Errorf("SessionName(%q)=%q want %q", in, got, want)
		}
	}
	seen := map[string]string{}
	for _, in := range append([]string{"work/api", "work.api", "work:api", "work api", "", "...", "über"}, hostile...) {
		got := SessionName(in)
		for _, r := range got {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				t.Fatalf("SessionName(%q)=%q has %q", in, got, r)
			}
		}
		if got == "" || len(got) > 60 {
			t.Fatalf("SessionName(%q)=%q", in, got)
		}
		if prev, ok := seen[got]; ok && prev != in {
			t.Fatalf("collision: %q and %q -> %q", prev, in, got)
		}
		seen[got] = in
	}
	if !strings.HasPrefix(SessionName("work/api"), "work_api-") {
		t.Fatal(SessionName("work/api"))
	}
	if SessionName(strings.Repeat("a", 100)) == strings.Repeat("a", 100) {
		t.Fatal("not capped")
	}
}

func TestPlanTmux(t *testing.T) {
	argv := []string{"/bin/claude", "--continue"}
	win := []string{"-c", "/p/a##b", "-n", "T##x", "--", "/bin/claude", "--continue"}
	cases := []struct {
		name                   string
		exists, inside, resume bool
		setup                  [][]string
		attach                 []string
	}{
		{"new, outside", false, false, false, [][]string{append([]string{"new-session", "-d", "-s", "s"}, win...)}, []string{"attach-session", "-t", "=s"}},
		{"new, inside: no nesting", false, true, false, [][]string{append([]string{"new-session", "-d", "-s", "s"}, win...)}, []string{"switch-client", "-t", "=s"}},
		{"new, resume still creates", false, false, true, [][]string{append([]string{"new-session", "-d", "-s", "s"}, win...)}, []string{"attach-session", "-t", "=s"}},
		{"exists, fresh: new window", true, false, false, [][]string{append([]string{"new-window", "-t", "=s:"}, win...)}, []string{"attach-session", "-t", "=s"}},
		{"exists, fresh, inside", true, true, false, [][]string{append([]string{"new-window", "-t", "=s:"}, win...)}, []string{"switch-client", "-t", "=s"}},
		{"exists, resume: re-attach only", true, false, true, nil, []string{"attach-session", "-t", "=s"}},
		{"exists, resume, inside", true, true, true, nil, []string{"switch-client", "-t", "=s"}},
	}
	for _, c := range cases {
		p := PlanTmux("s", "/p/a#b", "T#x", argv, c.exists, c.inside, c.resume)
		if !reflect.DeepEqual(p.Setup, c.setup) || !reflect.DeepEqual(p.Attach, c.attach) {
			t.Errorf("%s:\n got %q %q\nwant %q %q", c.name, p.Setup, p.Attach, c.setup, c.attach)
		}
	}
}

func TestPlanTmuxQuoting(t *testing.T) {
	// A lone argv element would go to sh -c: it must be one quoted word.
	p := PlanTmux("s", "/p", "", []string{"/opt/my tools/it's"}, false, false, false)
	got := p.Setup[0]
	if want := []string{"new-session", "-d", "-s", "s", "-c", "/p", "--", `'/opt/my tools/it'\''s'`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	// Hostile args are separate argv elements, untouched; hostile titles
	// lose control characters and get '#' escaped.
	for _, h := range hostile {
		p := PlanTmux("s", "/p/"+h, h, []string{"/bin/cli", h}, false, false, false)
		s := p.Setup[0]
		if s[len(s)-1] != h || s[len(s)-2] != "/bin/cli" || s[len(s)-3] != "--" {
			t.Fatalf("%q: %q", h, s)
		}
		if s[5] != TmuxFormatEscape("/p/"+h) {
			t.Fatalf("dir %q", s[5])
		}
		for i, a := range s[:len(s)-2] {
			if i != 5 && strings.ContainsAny(a, "\n\x1b") { // s[5] is the real dir
				t.Fatalf("control char in %q", a)
			}
		}
	}
}

func tmuxDeps(vars map[string]string, onPath map[string]string, r *fakeRunner) Deps {
	return Deps{
		LookPath: func(n string) (string, error) {
			if p, ok := onPath[n]; ok {
				return p, nil
			}
			return "", errors.New("no")
		},
		Runner: r, Env: envOf("linux", vars, ""), TmuxSocket: "test",
	}
}

func TestTmuxLaunch(t *testing.T) {
	path := map[string]string{"tmux": "/usr/bin/tmux", "claude": "/usr/bin/claude"}
	for _, exists := range []bool{false, true} {
		for _, inside := range []bool{false, true} {
			r := &fakeRunner{runCode: 4, out: func(c Command) (string, int, error) {
				if c.Args[2] == "has-session" {
					if exists {
						return "", 0, nil
					}
					return "can't find session", 1, nil
				}
				return "", 0, nil
			}}
			vars := map[string]string{}
			if inside {
				vars["TMUX"] = "/tmp/tmux-1000/default,1,0"
			}
			cd := filepath.Join(t.TempDir(), "cd")
			code, err := (&Tmux{Deps: tmuxDeps(vars, path, r)}).Launch(Request{Dir: "/p/app", Name: "claude", Args: []string{"x"}, Session: "work/app", CDFile: cd})
			if err != nil || code != 4 {
				t.Fatalf("exists=%v inside=%v: code %d err %v", exists, inside, code, err)
			}
			sess := SessionName("work/app")
			if want := []string{"-L", "test", "has-session", "-t", "=" + sess}; !reflect.DeepEqual(r.outputs[0].Args, want) {
				t.Fatalf("%q", r.outputs[0].Args)
			}
			plan := PlanTmux(sess, "/p/app", "", []string{"/usr/bin/claude", "x"}, exists, inside, false)
			if len(r.outputs) != 2 || !reflect.DeepEqual(r.outputs[1].Args, append([]string{"-L", "test"}, plan.Setup[0]...)) {
				t.Fatalf("setup %v", r.outputs)
			}
			if len(r.runs) != 1 || !reflect.DeepEqual(r.runs[0].Args, append([]string{"-L", "test"}, plan.Attach...)) {
				t.Fatalf("attach %v", r.runs)
			}
			_, statErr := os.Stat(cd)
			if inside == (statErr == nil) {
				t.Fatalf("inside=%v: cd-file written=%v", inside, statErr == nil)
			}
		}
	}
}

func TestTmuxLaunchErrors(t *testing.T) {
	r := &fakeRunner{}
	if _, err := (&Tmux{Deps: tmuxDeps(nil, map[string]string{"claude": "/c"}, r)}).Launch(Request{Dir: "/p", Name: "claude"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if code, err := (&Tmux{Deps: tmuxDeps(nil, map[string]string{"tmux": "/t"}, r)}).Launch(Request{Dir: "/p", Name: "claude"}); !errors.Is(err, ErrNotFound) || code != 127 {
		t.Fatal(err)
	}
	if len(r.outputs)+len(r.runs) != 0 {
		t.Fatal("nothing should run")
	}
	r.out = func(c Command) (string, int, error) {
		if c.Args[2] == "has-session" {
			return "", 1, nil
		}
		return "duplicate session", 1, nil
	}
	_, err := (&Tmux{Deps: tmuxDeps(nil, map[string]string{"tmux": "/t", "claude": "/c"}, r)}).Launch(Request{Dir: "/p", Name: "claude"})
	if err == nil || !strings.Contains(err.Error(), "duplicate session") || len(r.runs) != 0 {
		t.Fatalf("err %v runs %v", err, r.runs)
	}
}
