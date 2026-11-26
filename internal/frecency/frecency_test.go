package frecency

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func TestScoreDecay(t *testing.T) {
	e := Entry{Rank: 8, Last: t0.Unix()}
	cases := []struct {
		age  time.Duration
		want float64
	}{
		{0, 32},
		{59 * time.Minute, 32},
		{time.Hour, 16},
		{23 * time.Hour, 16},
		{24 * time.Hour, 4},
		{6 * 24 * time.Hour, 4},
		{7 * 24 * time.Hour, 2},
		{365 * 24 * time.Hour, 2},
		{-time.Hour, 32}, // clock skew: a future timestamp counts as recent
	}
	for _, c := range cases {
		if got := Score(e, t0.Add(c.age)); got != c.want {
			t.Errorf("age %v: got %v want %v", c.age, got, c.want)
		}
	}
	// Frequency beats recency once the rank gap is wide enough.
	often := Entry{Rank: 10, Last: t0.Add(-48 * time.Hour).Unix()} // 5
	once := Entry{Rank: 1, Last: t0.Unix()}                        // 4
	if Score(often, t0) <= Score(once, t0) {
		t.Fatal("10 launches two days ago should outrank 1 launch now")
	}
	// ...but a single recent launch beats a few old ones.
	old := Entry{Rank: 3, Last: t0.Add(-30 * 24 * time.Hour).Unix()} // 0.75
	if Score(old, t0) >= Score(once, t0) {
		t.Fatal("recent launch should outrank 3 old ones")
	}
}

func TestAddAndScores(t *testing.T) {
	s := &Store{}
	s.Add("/p/a", t0)
	s.Add("/p/b/", t0) // cleaned
	s.Add("/p/a", t0.Add(time.Minute))
	if len(s.Entries) != 2 {
		t.Fatalf("entries %+v", s.Entries)
	}
	sc := s.Scores(t0.Add(time.Minute))
	if Lookup(sc, "/p/a") != 8 || Lookup(sc, "/p/b") != 4 || Lookup(sc, "/p/zzz") != 0 {
		t.Fatalf("scores %v", sc)
	}
	if s.Entries[0].Last != t0.Add(time.Minute).Unix() {
		t.Fatal("last not updated")
	}
}

func TestAging(t *testing.T) {
	s := &Store{}
	for i := 0; i < MaxRank; i++ {
		s.Add("/p/hot", t0)
	}
	s.Add("/p/cold", t0)
	var total float64
	for _, e := range s.Entries {
		total += e.Rank
	}
	if total > MaxRank {
		t.Fatalf("total rank %v not aged", total)
	}
	// /p/cold's rank (1) scaled by ~0.9 falls below 1 and is dropped.
	if len(s.Entries) != 1 || s.Entries[0].Path != filepath.Clean("/p/hot") {
		t.Fatalf("entries %+v", s.Entries)
	}
}

func TestEntryCap(t *testing.T) {
	s := &Store{}
	for i := 0; i < MaxEntries+20; i++ {
		s.Add(fmt.Sprintf("/p/%d", i), t0.Add(time.Duration(i)*time.Hour))
	}
	if len(s.Entries) != MaxEntries {
		t.Fatalf("len %d", len(s.Entries))
	}
	// The most recent launch survives the trim.
	last := fmt.Sprintf("/p/%d", MaxEntries+19)
	found := false
	for _, e := range s.Entries {
		found = found || e.Path == filepath.Clean(last)
	}
	if !found {
		t.Fatal("newest entry was trimmed")
	}
}

func TestRoundTripAndRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "anycli-launcher", "frecency.json")
	s, err := Load(path)
	if err != nil || len(s.Entries) != 0 {
		t.Fatalf("missing file: %v %+v", err, s)
	}
	if err := Record(path, "/p/a", t0); err != nil {
		t.Fatal(err)
	}
	if err := Record(path, "/p/a", t0); err != nil {
		t.Fatal(err)
	}
	s, err = Load(path)
	if err != nil || len(s.Entries) != 1 || s.Entries[0].Rank != 2 {
		t.Fatalf("got %v %+v", err, s)
	}
	// No temp files left behind.
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".frecency-*"))
	if len(left) != 0 {
		t.Fatalf("temp files: %v", left)
	}
}

func TestCorruptFile(t *testing.T) {
	for name, body := range map[string]string{
		"garbage":     "{not json",
		"empty":       "",
		"version":     `{"version": 99, "entries": [{"path": "/p/a", "rank": 5, "last": 1}]}`,
		"wrong shape": `[1, 2, 3]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "frecency.json")
			os.WriteFile(path, []byte(body), 0o644)
			s, err := Load(path)
			if !errors.Is(err, ErrCorrupt) || len(s.Entries) != 0 {
				t.Fatalf("got %v %+v", err, s)
			}
			// Recording replaces the corrupt file with a fresh history.
			if err := Record(path, "/p/b", t0); err != nil {
				t.Fatal(err)
			}
			s, err = Load(path)
			if err != nil || len(s.Entries) != 1 || s.Entries[0].Path != filepath.Clean("/p/b") {
				t.Fatalf("after record: %v %+v", err, s)
			}
		})
	}
}

func TestJunkEntriesDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frecency.json")
	os.WriteFile(path, []byte(`{"version":1,"entries":[
		{"path":"","rank":3,"last":1},
		{"path":"/p/neg","rank":-1,"last":1},
		{"path":"/p/ok","rank":2,"last":1}]}`), 0o644)
	s, err := Load(path)
	if err != nil || len(s.Entries) != 1 || s.Entries[0].Path != "/p/ok" {
		t.Fatalf("got %v %+v", err, s)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("AC_FRECENCY_FILE", "/x/f.json")
	if DefaultPath() != "/x/f.json" {
		t.Fatal("override")
	}
	t.Setenv("AC_FRECENCY_FILE", "")
	t.Setenv("XDG_STATE_HOME", filepath.FromSlash("/state"))
	if got := DefaultPath(); got != filepath.Join(filepath.FromSlash("/state"), "anycli-launcher", "frecency.json") {
		t.Fatalf("xdg: %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	if got := filepath.ToSlash(DefaultPath()); !strings.HasSuffix(got, ".local/state/anycli-launcher/frecency.json") {
		t.Fatalf("default: %s", got)
	}
}

func TestSaveUnwritable(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	// A path under a regular file can't be created on any OS.
	if err := Record(filepath.Join(blocker, "sub", "frecency.json"), "/p/a", t0); err == nil {
		t.Fatal("expected an error")
	}
}
