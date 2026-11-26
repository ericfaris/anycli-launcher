package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// OverlayFile is the per-project settings file name.
const OverlayFile = ".ac.toml"

// LoadOverlay reads dir/.ac.toml. A missing file returns (nil, nil).
//
// The overlay usually comes from a cloned repo, so it is treated as
// untrusted: `cli` may only name a binary (no path separators), so a repo
// cannot point the launcher at a script inside itself, and `backend` must
// be one of the fixed names in Backends.
func LoadOverlay(dir string) (*Project, error) {
	path := filepath.Join(dir, OverlayFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Project
	if _, err := toml.Decode(string(data), &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.ContainsAny(p.CLI, `/\`) {
		return nil, fmt.Errorf("%s: cli must be a command name, not a path (%q)", path, p.CLI)
	}
	if !ValidBackend(p.Backend) {
		return nil, fmt.Errorf("%s: unknown backend %q (use %s)", path, p.Backend, strings.Join(Backends, ", "))
	}
	return &p, nil
}
