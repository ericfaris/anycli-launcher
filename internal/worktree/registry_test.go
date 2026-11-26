package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func task(id string, started time.Time) Task {
	return Task{
		ID: id, Project: "api", Repo: filepath.Join("r", "api"), Worktree: filepath.Join("w", id),
		Branch: BranchPrefix + id[strings.IndexByte(id, '/')+1:], Backend: "here", Started: started,
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "tasks.json")
	r, err := Load(path)
	if err != nil || len(r.Tasks) != 0 {
		t.Fatalf("missing file: %+v %v", r, err)
	}
	r.Put(task("api/b", t0.Add(time.Hour)))
	r.Put(task("api/a", t0))
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || len(got.Tasks) != 2 || got.Tasks[0].ID != "api/a" {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Tasks[0].Dir != got.Tasks[0].Worktree {
		t.Fatal("Dir defaults to the worktree")
	}
	upd := got.Tasks[1]
	upd.Backend = "tmux"
	got.Put(upd)
	if len(got.Tasks) != 2 || got.Tasks[1].Backend != "tmux" {
		t.Fatal("Put replaces by id")
	}
	if !got.Delete("api/a") || got.Delete("api/a") || len(got.Tasks) != 1 {
		t.Fatal("Delete")
	}
	if _, ok := got.Get("api/b"); !ok {
		t.Fatal("Get")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".tasks-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestRegistryCorruptAndJunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	for name, content := range map[string]string{
		"bad json":    "{not json",
		"truncated":   `{"version":1,"tasks":[{"id":"api/a"`,
		"version":     `{"version":9,"tasks":[]}`,
		"wrong types": `{"version":1,"tasks":"nope"}`,
	} {
		os.WriteFile(path, []byte(content), 0o600)
		r, err := Load(path)
		if !errors.Is(err, ErrCorrupt) || len(r.Tasks) != 0 {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
	// Junk entries are dropped, good ones kept: bad ids (path tricks),
	// missing repo/worktree, duplicates.
	os.WriteFile(path, []byte(`{"version":1,"tasks":[
		{"id":"api/ok","repo":"/r","worktree":"/w/ok"},
		{"id":"api/ok","repo":"/r2","worktree":"/w/dup"},
		{"id":"../../etc/x","repo":"/r","worktree":"/w"},
		{"id":"api/../../x","repo":"/r","worktree":"/w"},
		{"id":"api","repo":"/r","worktree":"/w"},
		{"id":"api/BAD","repo":"/r","worktree":"/w"},
		{"id":"api/norepo","worktree":"/w"},
		{"id":"api/nowt","repo":"/r"}
	]}`), 0o600)
	r, err := Load(path)
	if err != nil || len(r.Tasks) != 1 || r.Tasks[0].Worktree != "/w/ok" {
		t.Fatalf("%+v %v", r, err)
	}
	// Oversized file: corrupt, not read into memory.
	big := make([]byte, maxRegistrySize+1)
	os.WriteFile(path, big, 0o600)
	if _, err := Load(path); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}

// Update sets a corrupt file aside instead of silently dropping it, and a
// failing fn writes nothing.
func TestUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	os.WriteFile(path, []byte("garbage"), 0o600)
	err := Update(path, t0, func(r *Registry) error {
		r.Put(task("api/a", t0))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(path + ".corrupt-" + "1795694400")
	if string(backup) != "garbage" {
		t.Fatalf("backup %q", backup)
	}
	r, _ := Load(path)
	if len(r.Tasks) != 1 {
		t.Fatal(r)
	}
	boom := errors.New("boom")
	if err := Update(path, t0, func(r *Registry) error { r.Tasks = nil; return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if r, _ := Load(path); len(r.Tasks) != 1 {
		t.Fatal("failed fn must not write")
	}
	if _, err := os.Stat(path + ".lock"); err == nil {
		t.Fatal("lock left behind")
	}
}

func TestUpdateLock(t *testing.T) {
	old := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	path := filepath.Join(t.TempDir(), "tasks.json")
	os.WriteFile(path+".lock", nil, 0o600)
	err := Update(path, t0, func(*Registry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("held lock: %v", err)
	}
	// A stale lock (crashed ac) is broken.
	old2 := time.Now().Add(-time.Minute)
	os.Chtimes(path+".lock", old2, old2)
	if err := Update(path, t0, func(*Registry) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	r := &Registry{Tasks: []Task{task("api/fix-login", t0), task("web/fix-login", t0), task("api/add-tests", t0)}}
	ids := func(ts []Task) []string {
		var out []string
		for _, x := range ts {
			out = append(out, x.ID)
		}
		return out
	}
	cases := map[string][]string{
		"api/fix-login": {"api/fix-login"},
		"ac/add-tests":  {"api/add-tests"},
		"add-tests":     {"api/add-tests"},
		"fix-login":     {"api/fix-login", "web/fix-login"}, // ambiguous slug
		"WEB":           {"web/fix-login"},
		"nope":          nil,
		"":              nil,
	}
	for q, want := range cases {
		if got := ids(r.Find(q)); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v want %v", q, got, want)
		}
	}
}

func TestPIDPathAndValidID(t *testing.T) {
	reg := filepath.Join("state", "tasks.json")
	if got := PIDPath(reg, "api/fix"); got != filepath.Join("state", "tasks", "api", "fix.pid") {
		t.Fatal(got)
	}
	for id, want := range map[string]bool{
		"api/fix": true, "work-api/fix-2": true, "a.b/x": true,
		"api": false, "/fix": false, "api/": false, "../x": false, "api/../x": false, ".a/x": false, "a/b/c": false,
	} {
		if ValidID(id) != want {
			t.Errorf("%q want %v", id, want)
		}
	}
}
