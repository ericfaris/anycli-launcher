// Package buildinfo reports what ac was built from and, without any network
// call, how the running copy is updated.
//
// Release builds (goreleaser) set version, commit and date with
//
//	-ldflags "-X github.com/ericfaris/anycli-launcher/v4/internal/buildinfo.version=..."
//
// A `go install ...@v4.x.y` build has no ldflags; its module version and VCS
// stamp come from runtime/debug.ReadBuildInfo instead.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// DevVersion is reported by a plain `go build` without VCS information.
const DevVersion = "4.0.0-dev"

// Set by -ldflags -X in release builds. Must stay uninitialized string vars.
var (
	version string
	commit  string
	date    string
)

// Info describes a build.
type Info struct {
	Version string // without a leading "v"
	Commit  string // full or short revision, "" when unknown
	Date    string // RFC 3339 build or commit time, "" when unknown
	Dirty   bool   // built from a modified tree (VCS stamp only)
}

// Get returns this binary's build info.
func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	return resolve(version, commit, date, bi, ok)
}

// resolve prefers ldflags values, then the Go module version and VCS stamp.
func resolve(ldVersion, ldCommit, ldDate string, bi *debug.BuildInfo, ok bool) Info {
	info := Info{Version: ldVersion, Commit: ldCommit, Date: ldDate}
	if ok && bi != nil {
		if info.Version == "" {
			if v := bi.Main.Version; v != "" && v != "(devel)" {
				info.Version = v
			}
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			case "vcs.modified":
				info.Dirty = ldCommit == "" && s.Value == "true"
			}
		}
	}
	if info.Version == "" {
		info.Version = DevVersion
	}
	info.Version = strings.TrimPrefix(info.Version, "v")
	return info
}

// String is the one-line form: "4.0.0 (abc1234, 2026-11-26)".
func (i Info) String() string {
	var extra []string
	if c := i.Commit; c != "" {
		if len(c) > 7 {
			c = c[:7]
		}
		if i.Dirty {
			c += "-dirty"
		}
		extra = append(extra, c)
	}
	if d := i.Date; d != "" {
		if len(d) >= 10 {
			d = d[:10]
		}
		extra = append(extra, d)
	}
	if len(extra) == 0 {
		return i.Version
	}
	return i.Version + " (" + strings.Join(extra, ", ") + ")"
}
