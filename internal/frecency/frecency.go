// Package frecency records project launches and scores them zoxide-style:
// each launch adds 1 to a directory's rank, and the score is the rank
// weighted by how long ago the last launch was. The store is a small JSON
// file that is rewritten atomically; a missing or corrupt file reads as an
// empty history so a bad file never blocks a launch.
package frecency

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// MaxRank is the total rank the store keeps. Past it every rank is scaled
	// down (zoxide's aging), so old favourites fade instead of piling up.
	MaxRank = 1000
	// MaxEntries caps the number of directories kept (lowest scores go).
	MaxEntries = 500
	// formatVersion is written to the file; other versions read as empty.
	formatVersion = 1
)

// Entry is one directory's history.
type Entry struct {
	Path string  `json:"path"`
	Rank float64 `json:"rank"`
	// Last is the most recent launch, in Unix seconds.
	Last int64 `json:"last"`
}

// Store is the in-memory history.
type Store struct {
	Entries []Entry `json:"entries"`
}

type file struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// DefaultPath is where the history lives: $AC_FRECENCY_FILE, else
// $XDG_STATE_HOME/anycli-launcher/frecency.json, else
// ~/.local/state/anycli-launcher/frecency.json (on every OS, like the config).
func DefaultPath() string {
	if p := os.Getenv("AC_FRECENCY_FILE"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "anycli-launcher", "frecency.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "anycli-launcher", "frecency.json")
}

// ErrCorrupt wraps a history file that exists but can't be used.
var ErrCorrupt = errors.New("frecency history unreadable")

// Load reads the store. A missing file is an empty store and no error. A
// corrupt file (bad JSON, unknown version) is an empty store plus an error
// wrapping ErrCorrupt, so callers can carry on and doctor can report it.
func Load(path string) (*Store, error) {
	s := &Store{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return s, fmt.Errorf("%w: %s: %v", ErrCorrupt, path, err)
	}
	if f.Version != formatVersion {
		return s, fmt.Errorf("%w: %s: unsupported version %d", ErrCorrupt, path, f.Version)
	}
	for _, e := range f.Entries {
		if e.Path == "" || e.Rank <= 0 || e.Rank != e.Rank { // drop junk, NaN
			continue
		}
		s.Entries = append(s.Entries, e)
	}
	return s, nil
}

// Save writes the store atomically (temp file + rename).
func (s *Store) Save(path string) error {
	data, err := json.MarshalIndent(file{Version: formatVersion, Entries: s.Entries}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".frecency-*.json")
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

func key(dir string) string { return filepath.Clean(dir) }

// Add records one launch of dir at now, then ages and trims the store.
func (s *Store) Add(dir string, now time.Time) {
	k := key(dir)
	found := false
	for i := range s.Entries {
		if s.Entries[i].Path == k {
			s.Entries[i].Rank++
			s.Entries[i].Last = now.Unix()
			found = true
			break
		}
	}
	if !found {
		s.Entries = append(s.Entries, Entry{Path: k, Rank: 1, Last: now.Unix()})
	}
	s.age(now)
}

// age applies zoxide's aging: when the total rank passes MaxRank, scale
// every rank so the total is 90% of MaxRank and drop entries below 1. Then
// keep at most MaxEntries, preferring higher scores.
func (s *Store) age(now time.Time) {
	var total float64
	for _, e := range s.Entries {
		total += e.Rank
	}
	if total > MaxRank {
		f := 0.9 * MaxRank / total
		kept := s.Entries[:0]
		for _, e := range s.Entries {
			e.Rank *= f
			if e.Rank >= 1 {
				kept = append(kept, e)
			}
		}
		s.Entries = kept
	}
	if len(s.Entries) > MaxEntries {
		sort.SliceStable(s.Entries, func(i, j int) bool {
			return Score(s.Entries[i], now) > Score(s.Entries[j], now)
		})
		s.Entries = s.Entries[:MaxEntries]
	}
}

// Score is zoxide's frecency: rank x4 within the hour, x2 within the day,
// x0.5 within the week, x0.25 after that.
func Score(e Entry, now time.Time) float64 {
	age := now.Sub(time.Unix(e.Last, 0))
	switch {
	case age < time.Hour:
		return e.Rank * 4
	case age < 24*time.Hour:
		return e.Rank * 2
	case age < 7*24*time.Hour:
		return e.Rank / 2
	default:
		return e.Rank / 4
	}
}

// Scores maps each recorded directory to its score at now.
func (s *Store) Scores(now time.Time) map[string]float64 {
	out := make(map[string]float64, len(s.Entries))
	for _, e := range s.Entries {
		out[e.Path] = Score(e, now)
	}
	return out
}

// Lookup returns dir's score in a Scores map (path-cleaned; 0 when absent).
func Lookup(scores map[string]float64, dir string) float64 {
	return scores[key(dir)]
}

// Record loads the store at path, adds dir, and saves it. A corrupt file is
// replaced with a fresh history.
func Record(path, dir string, now time.Time) error {
	s, err := Load(path)
	if err != nil && !errors.Is(err, ErrCorrupt) {
		return err
	}
	s.Add(dir, now)
	return s.Save(path)
}
