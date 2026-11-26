package buildinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Repo and module coordinates used in update hints.
const (
	Repo       = "ericfaris/anycli-launcher"
	ModulePath = "github.com/ericfaris/anycli-launcher/v4"
	// InstallShURL and InstallPs1URL are attached to every GitHub release.
	InstallShURL  = "https://github.com/" + Repo + "/releases/latest/download/install.sh"
	InstallPs1URL = "https://github.com/" + Repo + "/releases/latest/download/install.ps1"
)

// Method is how this copy of ac was installed, guessed from its path.
type Method string

const (
	Homebrew  Method = "homebrew"
	Scoop     Method = "scoop"
	GoInstall Method = "go install"
	Script    Method = "install script"
	Unknown   Method = "unknown"
)

// Host is what detection reads; the zero value means the real process.
type Host struct {
	Exe    string // resolved path of the running binary
	GOOS   string
	Home   string
	Getenv func(string) string
}

func (h Host) fill() Host {
	if h.Getenv == nil {
		h.Getenv = os.Getenv
	}
	if h.GOOS == "" {
		h.GOOS = runtime.GOOS
	}
	if h.Home == "" {
		h.Home, _ = os.UserHomeDir()
	}
	if h.Exe == "" {
		if exe, err := os.Executable(); err == nil {
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			h.Exe = exe
		}
	}
	return h
}

// Detect guesses the install method from the binary's location. It never
// touches the network.
func Detect(h Host) Method {
	h = h.fill()
	if h.Exe == "" {
		return Unknown
	}
	win := h.GOOS == "windows"
	norm := func(p string) string {
		p = strings.ReplaceAll(p, `\`, "/")
		if win {
			p = strings.ToLower(p)
		}
		return strings.TrimRight(p, "/")
	}
	exe := norm(h.Exe)
	dir := exe[:max(strings.LastIndex(exe, "/"), 0)]
	under := func(root string) bool {
		r := norm(root)
		return r != "" && strings.HasPrefix(exe, r+"/")
	}
	in := func(d string) bool { return d != "" && dir == norm(d) }
	low := strings.ToLower(exe)
	scoopApps := ""
	if s := h.Getenv("SCOOP"); s != "" {
		scoopApps = filepath.Join(s, "apps")
	}

	switch {
	// Formulae live in <prefix>/Cellar, casks in <prefix>/Caskroom; the
	// bin/ac symlink resolves into one of them.
	case strings.Contains(low, "/cellar/anycli-launcher/"), strings.Contains(low, "/caskroom/anycli-launcher/"):
		return Homebrew
	case strings.Contains(low, "/scoop/apps/anycli-launcher/"), under(scoopApps):
		return Scoop
	case in(h.Getenv("GOBIN")):
		return GoInstall
	case in(goPathBin(h)):
		return GoInstall
	case in(h.Getenv("AC_INSTALL_DIR")), in(defaultScriptDir(h)):
		return Script
	}
	return Unknown
}

func goPathBin(h Host) string {
	gp := h.Getenv("GOPATH")
	if gp == "" {
		if h.Home == "" {
			return ""
		}
		return filepath.Join(h.Home, "go", "bin")
	}
	sep := ":"
	if h.GOOS == "windows" {
		sep = ";"
	}
	first, _, _ := strings.Cut(gp, sep)
	return filepath.Join(first, "bin")
}

// defaultScriptDir is where install.sh / install.ps1 put ac by default.
func defaultScriptDir(h Host) string {
	if h.GOOS == "windows" {
		if la := h.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, "Programs", "anycli-launcher")
		}
		return ""
	}
	if h.Home == "" {
		return ""
	}
	return filepath.Join(h.Home, ".local", "bin")
}

// UpdateCommand is the command that updates a copy installed by m.
func UpdateCommand(m Method, goos string) string {
	switch m {
	case Homebrew:
		return "brew upgrade anycli-launcher"
	case Scoop:
		return "scoop update anycli-launcher"
	case GoInstall:
		return "go install " + ModulePath + "/cmd/ac@latest"
	case Script:
		if goos == "windows" {
			return "irm " + InstallPs1URL + " | iex"
		}
		return "curl -fsSL " + InstallShURL + " | sh"
	}
	return ""
}

// UpdateHint is the multi-line "how to update" text for m: the detected
// method's command, or every option when the method is unknown.
func UpdateHint(m Method, goos string) []string {
	if m != Unknown {
		return []string{"update (" + string(m) + "): " + UpdateCommand(m, goos)}
	}
	lines := []string{"update with whatever installed it:"}
	if goos != "windows" {
		lines = append(lines, "  brew:    "+UpdateCommand(Homebrew, goos))
	} else {
		lines = append(lines, "  scoop:   "+UpdateCommand(Scoop, goos))
	}
	lines = append(lines,
		"  script:  "+UpdateCommand(Script, goos),
		"  go:      "+UpdateCommand(GoInstall, goos),
		"  or download a release: https://github.com/"+Repo+"/releases/latest",
	)
	return lines
}
