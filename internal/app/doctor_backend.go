package app

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ericfaris/anycli-launcher/v4/internal/config"
	"github.com/ericfaris/anycli-launcher/v4/internal/launch"
)

// doctorBackends reports the backend setting, the detected tab terminal,
// tmux/zellij, and which command shapes are unverified. Nothing here fails
// doctor: every backend falls back to "here".
func (a *app) doctorBackends(r *report) {
	setting := a.cfg.Backend
	switch {
	case a.backend != "":
		r.pass("backend: " + a.backend + " (--backend)")
	case setting == "":
		r.pass("backend: auto (default: a new tab for projects with a color or title, else here)")
	case !launch.ValidBackend(setting):
		r.warn(fmt.Sprintf("unknown backend %q", setting), "use "+strings.Join(launch.Backends, ", ")+"; using auto")
	default:
		r.pass("backend: " + setting)
	}
	for _, name := range sortedKeys(a.cfg.Projects) {
		p := a.cfg.Projects[name]
		if !launch.ValidBackend(p.Backend) {
			r.warn(fmt.Sprintf("%s: unknown backend %q", name, p.Backend), "using auto")
		}
		if p.Color != "" && !config.ValidColor(p.Color) {
			r.warn(fmt.Sprintf("%s: color %q is not #rrggbb", name, p.Color), "ignored")
		}
	}
	if t := a.cfg.Terminal; t != "" && !launch.ValidTerminal(t) {
		names := make([]string, len(launch.Terminals))
		for i, x := range launch.Terminals {
			names[i] = string(x)
		}
		r.warn(fmt.Sprintf("unknown terminal %q", t), "use "+strings.Join(names, ", ")+"; detecting instead")
	}
	active := a.globalBackend()

	det, prog := a.detectTerminal()
	switch {
	case det.Terminal == launch.TermNone:
		r.info("tab terminal: none detected (" + det.Reason + "); tab launches run here")
	case prog == "":
		r.warn("tab terminal: "+string(det.Terminal)+" ("+det.Reason+") but "+launch.TerminalProgram(det.Terminal)+" not found", "tab launches run here")
	default:
		r.pass("tab terminal: " + string(det.Terminal) + " (" + det.Reason + ") -> " + prog)
		if note, ok := launch.Unverified[string(det.Terminal)]; ok {
			if active == launch.BackendTab || active == launch.BackendAuto {
				r.warn("unverified: "+string(det.Terminal), note)
			} else {
				r.info("unverified: " + string(det.Terminal) + ": " + note)
			}
		}
		if det.Terminal == launch.TermKitty {
			r.info("kitty needs allow_remote_control (or listen_on) for tabs")
		}
	}
	if a.env.LaunchEnv.Platform() == launch.PlatformWSL {
		r.info("WSL: tabs open via wt.exe -> wsl.exe -d $WSL_DISTRO_NAME --cd <dir> --exec ac __tab-exec")
	}

	a.doctorMux(r, "tmux", "TMUX", active)
	a.doctorMux(r, "zellij", "ZELLIJ", active)

	r.info("not yet checked on a real terminal: " + strings.Join(sortedKeys(launch.Unverified), ", "))
}

func (a *app) doctorMux(r *report, name, envVar, active string) {
	bin, err := a.env.LookPath(name)
	if err != nil {
		if active == name {
			r.warn(name+" not on PATH", "backend = "+name+" falls back to here")
		} else {
			r.info(name + ": not installed")
		}
		return
	}
	flag := "-V"
	if name == "zellij" {
		flag = "--version"
	}
	ver, _ := a.env.Run("", bin, flag)
	if ver == "" {
		ver = "version unknown"
	}
	r.pass(name + " -> " + bin + " (" + ver + ")")
	if note, ok := launch.Unverified[name]; ok {
		if active == name {
			r.warn("unverified: "+name, note)
		} else {
			r.info("unverified: " + name + ": " + note)
		}
	}
	if a.launchEnvGet(envVar) != "" {
		r.info("inside " + name + ": launches open a new window/tab in it instead of nesting")
	}
}

func (a *app) launchEnvGet(k string) string {
	if g := a.env.LaunchEnv.Getenv; g != nil {
		return g(k)
	}
	return os.Getenv(k)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
