package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Task is one task session.
type Task struct {
	// ID is "<project key>/<slug>", unique, and what `ac finish` takes.
	ID      string `json:"id"`
	Project string `json:"project"` // the project's menu label
	Repo    string `json:"repo"`    // the main repository's top level
	// Worktree is the worktree root; Dir is where the CLI runs (the same,
	// or the matching subdirectory when the project is inside a repo).
	Worktree string `json:"worktree"`
	Dir      string `json:"dir"`
	Branch   string `json:"branch"`
	Base     string `json:"base"` // base commit id
	Task     string `json:"task"` // the description, sent as the prompt
	CLI      string `json:"cli"`
	Backend  string `json:"backend"` // here, tab, tmux, zellij (as launched)
	// Session is the tmux/zellij session name (only for those backends).
	Session string `json:"session,omitempty"`
	// PIDFile is where here/tab launches record the CLI's pid.
	PIDFile string    `json:"pid_file,omitempty"`
	Started time.Time `json:"started"`
}

// Slug is the ID's last part.
func (t Task) Slug() string {
	if i := strings.LastIndexByte(t.ID, '/'); i >= 0 {
		return t.ID[i+1:]
	}
	return t.ID
}

// Registry is the in-memory task list.
type Registry struct {
	Tasks []Task
}

type regFile struct {
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}

const regVersion = 1

// ErrCorrupt wraps a registry file that exists but can't be used.
var ErrCorrupt = errors.New("task registry unreadable")

// maxRegistrySize bounds what Load reads.
const maxRegistrySize = 4 << 20

// Load reads the registry. A missing file is empty and no error; a corrupt
// one (bad JSON, other version, too big) is empty plus an error wrapping
// ErrCorrupt. Entries without an id, repo or worktree are dropped.
func Load(path string) (*Registry, error) {
	r := &Registry{}
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if st.Size() > maxRegistrySize {
		return r, fmt.Errorf("%w: %s: too large", ErrCorrupt, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	var f regFile
	if err := json.Unmarshal(data, &f); err != nil {
		return r, fmt.Errorf("%w: %s: %v", ErrCorrupt, path, err)
	}
	if f.Version != regVersion {
		return r, fmt.Errorf("%w: %s: unsupported version %d", ErrCorrupt, path, f.Version)
	}
	seen := map[string]bool{}
	for _, t := range f.Tasks {
		if !ValidID(t.ID) || t.Repo == "" || t.Worktree == "" || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		if t.Dir == "" {
			t.Dir = t.Worktree
		}
		r.Tasks = append(r.Tasks, t)
	}
	return r, nil
}

// Save writes the registry atomically (temp file + rename), oldest first.
func (r *Registry) Save(path string) error {
	sort.SliceStable(r.Tasks, func(i, j int) bool { return r.Tasks[i].Started.Before(r.Tasks[j].Started) })
	data, err := json.MarshalIndent(regFile{Version: regVersion, Tasks: r.Tasks}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tasks-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Get returns the task with id.
func (r *Registry) Get(id string) (Task, bool) {
	for _, t := range r.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// Put adds or replaces t (by ID).
func (r *Registry) Put(t Task) {
	for i := range r.Tasks {
		if r.Tasks[i].ID == t.ID {
			r.Tasks[i] = t
			return
		}
	}
	r.Tasks = append(r.Tasks, t)
}

// Delete removes id; it reports whether it was there.
func (r *Registry) Delete(id string) bool {
	for i := range r.Tasks {
		if r.Tasks[i].ID == id {
			r.Tasks = append(r.Tasks[:i], r.Tasks[i+1:]...)
			return true
		}
	}
	return false
}

// Find resolves what the user typed: the exact id, else the exact branch,
// else the exact slug, else ids containing the text. More than one result
// is ambiguous.
func (r *Registry) Find(q string) []Task {
	if q == "" {
		return nil
	}
	tiers := []func(Task) bool{
		func(t Task) bool { return t.ID == q },
		func(t Task) bool { return t.Branch == q },
		func(t Task) bool { return t.Slug() == q },
		func(t Task) bool { return strings.Contains(strings.ToLower(t.ID), strings.ToLower(q)) },
	}
	for _, match := range tiers {
		var out []Task
		for _, t := range r.Tasks {
			if match(t) {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// Update loads the registry under a lock, applies fn and saves the result.
// A corrupt file is kept as <path>.corrupt-<unix time> (so nothing is
// silently lost) and replaced by a fresh registry. When fn returns an error
// nothing is written.
func Update(path string, now time.Time, fn func(*Registry) error) error {
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	r, err := Load(path)
	if errors.Is(err, ErrCorrupt) {
		backup := fmt.Sprintf("%s.corrupt-%d", path, now.Unix())
		if rerr := os.Rename(path, backup); rerr != nil {
			return fmt.Errorf("%v (and could not set it aside: %v)", err, rerr)
		}
		r = &Registry{}
	} else if err != nil {
		return err
	}
	if err := fn(r); err != nil {
		return err
	}
	return r.Save(path)
}

// lockStale is how old a lock file may get before it is presumed left by a
// crashed ac and broken.
const lockStale = 10 * time.Second

var lockWait = 3 * time.Second

// lock takes <path>.lock with O_EXCL, waiting a little for another ac.
func lock(path string) (func(), error) {
	lp := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(lp) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(lp); serr == nil && time.Since(st.ModTime()) > lockStale {
			os.Remove(lp)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("task registry is locked (%s); another ac is writing it, or remove the lock file", lp)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// ValidID reports whether id has the shape this package generates:
// "<SafeComponent>/<slug>". Anything else in the file is dropped, so a
// hand-edited id can't steer a pid-file path out of the state dir.
func ValidID(id string) bool {
	proj, slug, ok := strings.Cut(id, "/")
	return ok && proj != "" && SafeComponent(proj) == proj && ValidBranch(BranchPrefix+slug)
}

// PIDPath is where a task's pid file lives: <registry dir>/tasks/<id>.pid.
func PIDPath(registry, id string) string {
	parts := strings.Split(id, "/")
	return filepath.Join(append([]string{filepath.Dir(registry), "tasks"}, parts...)...) + ".pid"
}
