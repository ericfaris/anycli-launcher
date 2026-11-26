package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/frecency"
	"github.com/ericfaris/anycli-launcher/internal/tui"
)

func (h *harness) history() map[string]float64 {
	h.t.Helper()
	s, err := frecency.Load(h.env.FrecencyPath)
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]float64{}
	for _, e := range s.Entries {
		out[e.Path] = e.Rank
	}
	return out
}

func TestLaunchRecordsHistory(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha", "beta")
	if h.run("alpha") != 0 || h.run("alpha") != 0 || h.run("beta", "-r") != 0 {
		t.Fatal(h.err.String())
	}
	got := h.history()
	want := map[string]float64{filepath.Join(h.root, "alpha"): 2, filepath.Join(h.root, "beta"): 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// A failed lookup records nothing.
	h.run("zzz")
	if len(h.history()) != 2 {
		t.Fatal("unmatched name recorded")
	}
}

func TestLaunchNotOnPathNotRecorded(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha")
	h.onPath["claude"] = false
	h.run("alpha")
	if _, err := os.Stat(h.env.FrecencyPath); !os.IsNotExist(err) {
		t.Fatalf("history written for a CLI that isn't installed: %v", err)
	}
}

func TestPartialResolvesByFrecency(t *testing.T) {
	h := newHarness(t)
	h.mkproj("my-app", "my-api", "api")

	// No history: ambiguous, as in phase 1.
	if h.run("my") != 1 || !strings.Contains(h.err.String(), "matches more than one project") {
		t.Fatalf("expected ambiguity: %s", h.err.String())
	}
	if len(h.launcher.reqs) != 0 {
		t.Fatal("ambiguous name launched")
	}

	h.run("my-api")
	if code := h.run("my"); code != 0 {
		t.Fatalf("frecency should resolve 'my': %d %s", code, h.err.String())
	}
	if got := h.launcher.reqs[len(h.launcher.reqs)-1].Dir; got != filepath.Join(h.root, "my-api") {
		t.Fatalf("launched %s", got)
	}

	// Exact names still win over a more frecent match.
	for i := 0; i < 5; i++ {
		h.run("my-api")
	}
	h.run("api")
	if got := h.launcher.reqs[len(h.launcher.reqs)-1].Dir; got != filepath.Join(h.root, "api") {
		t.Fatalf("exact lost to frecency: %s", got)
	}

	// Fuzzy (letters in order) works when nothing else matches.
	h.run("mapp")
	if got := h.launcher.reqs[len(h.launcher.reqs)-1].Dir; got != filepath.Join(h.root, "my-app") {
		t.Fatalf("fuzzy: %s", got)
	}

	// Equal history stays ambiguous; the list is ordered by frecency.
	h2 := newHarness(t)
	h2.mkproj("my-app", "my-api")
	h2.run("my-app")
	h2.run("my-api")
	if h2.run("my") != 1 {
		t.Fatal("equal scores must stay ambiguous")
	}
}

func TestSortOrderRecent(t *testing.T) {
	labels := func(o tui.Options) []string {
		var out []string
		for _, e := range o.Entries {
			if e.Kind == tui.KindProject {
				out = append(out, e.Label)
			}
		}
		return out
	}
	for _, order := range []string{"recent", "frecent"} {
		t.Run(order, func(t *testing.T) {
			h := newHarness(t)
			h.mkproj("alpha", "beta", "gamma", "delta")
			h.writeConfig(func(c *config.Config) { c.SortOrder = order })
			h.run() // no history: name order
			h.run("gamma")
			h.run("gamma")
			h.now = h.now.Add(2 * time.Hour)
			h.run("beta")
			h.run() // gamma: 2 launches 2h ago (4); beta: 1 just now (4) -> tie -> name order
			h.now = h.now.Add(time.Minute)
			h.run("gamma")
			h.run() // gamma: 3 just now (12) beats beta (4)
			if len(h.menuOpts) != 3 {
				t.Fatalf("menus %d", len(h.menuOpts))
			}
			want := [][]string{
				{"alpha", "beta", "delta", "gamma"},
				{"beta", "gamma", "alpha", "delta"},
				{"gamma", "beta", "alpha", "delta"},
			}
			for i, w := range want {
				if got := labels(h.menuOpts[i]); !reflect.DeepEqual(got, w) {
					t.Errorf("menu %d: got %v want %v", i, got, w)
				}
			}
			if h.menuOpts[2].Scores[filepath.Join(h.root, "gamma")] != 12 {
				t.Fatalf("scores not passed to the menu: %v", h.menuOpts[2].Scores)
			}
			// ac list keeps name order regardless.
			h.run("list")
			if h.out.String() != "alpha\nbeta\ndelta\ngamma\n" {
				t.Fatalf("list: %q", h.out.String())
			}
		})
	}
}

func TestSortOrderNameIgnoresHistory(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha", "beta")
	h.run("beta")
	h.run()
	es := h.menuOpts[0].Entries
	if es[len(es)-2].Label != "alpha" || es[len(es)-1].Label != "beta" {
		t.Fatalf("name order changed: %+v", es)
	}
	// The menu still gets scores for the filter.
	if len(h.menuOpts[0].Scores) != 1 {
		t.Fatal("scores missing")
	}
}

func TestMenuLaunchRecordsHistory(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha")
	h.menu = []tui.Result{{Action: tui.ActSelect, Entry: tui.Entry{Kind: tui.KindProject, Label: "alpha", Dir: filepath.Join(h.root, "alpha")}}}
	if h.run() != 0 {
		t.Fatal(h.err.String())
	}
	if h.history()[filepath.Join(h.root, "alpha")] != 1 {
		t.Fatalf("menu launch not recorded: %v", h.history())
	}
}

func TestBadHistoryNeverBlocksLaunch(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha")
	os.MkdirAll(filepath.Dir(h.env.FrecencyPath), 0o755)
	os.WriteFile(h.env.FrecencyPath, []byte("{garbage"), 0o644)
	if h.run("alpha") != 0 {
		t.Fatal("corrupt history blocked the launch")
	}
	if h.history()[filepath.Join(h.root, "alpha")] != 1 {
		t.Fatal("corrupt history should be replaced")
	}

	// Unwritable location: still launches, silently.
	blocker := filepath.Join(h.home, "blocker")
	os.WriteFile(blocker, []byte("x"), 0o644)
	h.env.FrecencyPath = filepath.Join(blocker, "frecency.json")
	if h.run("alpha") != 0 || h.err.Len() != 0 {
		t.Fatalf("unwritable history: %q", h.err.String())
	}
}

func TestDoctorFrecency(t *testing.T) {
	h := newHarness(t)
	h.mkproj("alpha")
	h.run("doctor")
	out := h.out.String()
	if !strings.Contains(out, "-- Launch history (frecency) --") || !strings.Contains(out, "no launches recorded yet") ||
		!strings.Contains(out, "menu sorted by name") {
		t.Fatalf("doctor:\n%s", out)
	}
	h.run("alpha")
	h.writeConfig(func(c *config.Config) { c.SortOrder = "recent" })
	h.run("doctor")
	out = h.out.String()
	if !strings.Contains(out, "1 directory in "+h.env.FrecencyPath) || !strings.Contains(out, "menu sorted by frecency") {
		t.Fatalf("doctor:\n%s", out)
	}

	os.WriteFile(h.env.FrecencyPath, []byte("nope"), 0o644)
	h.writeConfig(func(c *config.Config) { c.SortOrder = "newest" })
	h.run("doctor")
	out = h.out.String()
	if !strings.Contains(out, "history file is corrupt") || !strings.Contains(out, `unknown sort_order "newest"`) {
		t.Fatalf("doctor:\n%s", out)
	}
	// Warnings only: doctor stays healthy.
	if !strings.Contains(out, "✓ Healthy") {
		t.Fatalf("corrupt history must not fail doctor:\n%s", out)
	}

	h.env.FrecencyPath = filepath.Join(h.home, "blocker", "x", "frecency.json")
	os.WriteFile(filepath.Join(h.home, "blocker"), []byte("x"), 0o644)
	h.run("doctor")
	// Linux reports ENOTDIR on read (unreadable); others report not-exist,
	// then the write probe fails (not writable). Either way it's a warning.
	if out := h.out.String(); !strings.Contains(out, "history not writable") && !strings.Contains(out, "history file unreadable") {
		t.Fatalf("doctor:\n%s", h.out.String())
	}
}
