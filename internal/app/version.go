package app

import (
	"runtime"

	"github.com/ericfaris/anycli-launcher/v4/internal/buildinfo"
)

// installGOOS is the OS the update hint is written for.
func (a *app) installGOOS() string {
	if g := a.env.Install.GOOS; g != "" {
		return g
	}
	return runtime.GOOS
}

// version prints the build and, offline, how to update this copy. v4 has
// no self-update: the package manager (or script) that installed ac does it.
func (a *app) version() int {
	info := buildinfo.Get()
	info.Version = Version
	a.printf("anycli-launcher %s\n", info)
	for _, l := range buildinfo.UpdateHint(buildinfo.Detect(a.env.Install), a.installGOOS()) {
		a.printf("%s\n", l)
	}
	return 0
}

// doctorInstall is the doctor's version/update section. It never checks
// for a newer release (no network).
func (a *app) doctorInstall(r *report) {
	info := buildinfo.Get()
	info.Version = Version
	r.info("version: " + info.String())
	m := buildinfo.Detect(a.env.Install)
	r.info("installed via: " + string(m))
	for _, l := range buildinfo.UpdateHint(m, a.installGOOS()) {
		r.info(l)
	}
}
