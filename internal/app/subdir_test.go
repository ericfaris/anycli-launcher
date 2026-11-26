package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSubdirOf(t *testing.T) {
	top := t.TempDir()
	deep := filepath.Join(top, "pkg", "svc")
	os.MkdirAll(deep, 0o755)
	if got, err := subdirOf(top, top); err != nil || got != "." {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := subdirOf(deep, top); err != nil || got != filepath.Join("pkg", "svc") {
		t.Fatalf("%q %v", got, err)
	}
	// git's spelling of the top level may differ (resolved symlink): the
	// match is by file identity.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(top, link); err == nil {
		if got, err := subdirOf(filepath.Join(link, "pkg"), top); err != nil || got != "pkg" {
			t.Fatalf("via link: %q %v", got, err)
		}
	}
	if _, err := subdirOf(t.TempDir(), top); err == nil {
		t.Fatal("unrelated dir")
	}
	if _, err := subdirOf(deep, filepath.Join(top, "missing")); err == nil {
		t.Fatal("missing top")
	}
}
