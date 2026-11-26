// Package app implements the ac command: argument dispatch, the menu loop,
// and the doctor/new/remove/setup/migrate subcommands. Everything external
// (stdio, PATH lookups, subprocesses, the TUI) comes in through Env so the
// flows are testable without a terminal.
package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ericfaris/anycli-launcher/internal/clis"
	"github.com/ericfaris/anycli-launcher/internal/config"
	"github.com/ericfaris/anycli-launcher/internal/frecency"
	"github.com/ericfaris/anycli-launcher/internal/launch"
	"github.com/ericfaris/anycli-launcher/internal/projects"
	"github.com/ericfaris/anycli-launcher/internal/shim"
	"github.com/ericfaris/anycli-launcher/internal/tui"
	"github.com/ericfaris/anycli-launcher/internal/worktree"
)

// Version is set at build time with -ldflags "-X .../internal/app.Version=...".
var Version = "4.0.0-dev"

// Env is the outside world.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	ConfigPath     string
	Home           string
	// IsTTY reports whether stdin is interactive (gates the first-run wizard).
	IsTTY    bool
	LookPath func(string) (string, error)
	// Run executes a helper command (git, gh) in dir and returns stdout.
	Run func(dir, name string, args ...string) (string, error)
	// NewBackend builds a launch backend by name (default launch.New).
	NewBackend func(name string, d launch.Deps) launch.Backend
	// LaunchEnv is what terminal detection reads (zero value: the real
	// environment, GOOS and /proc/version).
	LaunchEnv launch.Environ
	RunMenu   func(tui.Options) (tui.Result, error)
	// Editor opens path for editing (ac config).
	Editor func(path string) error
	// FrecencyPath is the launch-history file (default frecency.DefaultPath()).
	FrecencyPath string
	// Now is the clock for frecency (default time.Now).
	Now func() time.Time
	// TasksPath is the task-session registry (default $AC_TASKS_FILE, else
	// tasks.json next to the frecency history).
	TasksPath string
	// Probe checks task liveness (zero value: real tmux/zellij/process
	// checks, with LookPath from this Env).
	Probe worktree.Probe
}

// DefaultRun runs a command and returns trimmed stdout. A failure's error
// includes the command's stderr (git's "fatal: ..." reason).
func DefaultRun(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return strings.TrimSpace(string(out)), err
}

type app struct {
	env    *Env
	cdFile string
	// backend is the --backend override ("" = use config).
	backend   string
	cfg       config.Config
	cfgExists bool
	warns     []string
	warned    bool
	in        *bufio.Reader
}

// Main runs ac with args (without the program name) and returns the exit code.
func Main(args []string, env *Env) int {
	a := &app{env: env}
	if a.env.LookPath == nil {
		a.env.LookPath = exec.LookPath
	}
	if a.env.Run == nil {
		a.env.Run = DefaultRun
	}
	if a.env.ConfigPath == "" {
		a.env.ConfigPath = config.DefaultPath()
	}
	if a.env.Home == "" {
		a.env.Home, _ = os.UserHomeDir()
	}
	if a.env.FrecencyPath == "" {
		a.env.FrecencyPath = frecency.DefaultPath()
	}
	if a.env.Now == nil {
		a.env.Now = time.Now
	}
	if a.env.TasksPath == "" {
		a.env.TasksPath = os.Getenv("AC_TASKS_FILE")
		if a.env.TasksPath == "" {
			a.env.TasksPath = filepath.Join(filepath.Dir(a.env.FrecencyPath), "tasks.json")
		}
	}
	if a.env.NewBackend == nil {
		a.env.NewBackend = launch.New
	}
	if len(args) > 0 && args[0] == launch.TabExecCommand {
		// Trampoline inside a freshly opened tab: no config, no cd-file.
		return launch.TabExec(args[1:], a.env.Stdout, a.env.Stderr)
	}
	args, err := a.takeGlobalFlags(args)
	if err != nil {
		return a.fail(2, err)
	}
	if a.backend != "" && !launch.ValidBackend(a.backend) {
		return a.fail(2, fmt.Errorf("unknown backend %q (use %s)", a.backend, strings.Join(launch.Backends, ", ")))
	}
	return a.dispatch(args)
}

func (a *app) takeGlobalFlags(args []string) ([]string, error) {
	var out []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--cd-file":
			if i+1 >= len(args) {
				return nil, errors.New("--cd-file needs a path")
			}
			a.cdFile = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--cd-file="):
			a.cdFile = strings.TrimPrefix(args[i], "--cd-file=")
		case args[i] == "--backend":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--backend needs a name (%s)", strings.Join(launch.Backends, ", "))
			}
			a.backend = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--backend="):
			a.backend = strings.TrimPrefix(args[i], "--backend=")
		default:
			out = append(out, args[i])
		}
	}
	return out, nil
}

func (a *app) printf(format string, args ...any) { fmt.Fprintf(a.env.Stdout, format, args...) }
func (a *app) eprintf(format string, args ...any) {
	fmt.Fprintf(a.env.Stderr, format, args...)
}

func (a *app) fail(code int, err error) int {
	a.eprintf("ac: %v\n", err)
	return code
}

// load reads config.toml, auto-importing a v3 config the first time.
func (a *app) load() error {
	path := a.env.ConfigPath
	if !config.Exists(path) {
		if src := legacySource(path); src != "" {
			if err := a.migrate(src, path, false); err != nil {
				a.eprintf("ac: could not import v3 config %s: %v\n", src, err)
			}
		}
	}
	cfg, warns, err := config.Load(path)
	a.cfg, a.warns = cfg, warns
	a.cfgExists = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !a.warned {
		for _, w := range warns {
			a.eprintf("ac: %s (%s)\n", w, path)
		}
		a.warned = true
	}
	return nil
}

// legacySource finds a v3 config next to config.toml.
func legacySource(tomlPath string) string {
	dir := filepath.Dir(tomlPath)
	for _, name := range []string{"config.yaml", "config"} {
		if p := filepath.Join(dir, name); config.Exists(p) {
			return p
		}
	}
	return ""
}

// wordCommands are subcommands a same-named project overrides (v3 parity).
var wordCommands = map[string]bool{
	"help": true, "version": true, "list": true, "config": true,
	"doctor": true, "setup": true, "resume": true, "pick": true,
	"task": true, "running": true, "ls-tasks": true, "finish": true, "prune": true,
}

func (a *app) dispatch(args []string) int {
	mode := clis.Fresh
	var rest []string
	var task *string
	base, hasBase := "", false
	for i := 0; i < len(args); i++ {
		x := args[i]
		switch {
		case x == "--resume-picker":
			mode = clis.Picker
		case x == "--resume", x == "-r":
			mode = clis.Resume
		case x == "--task", x == "--base":
			if i+1 >= len(args) {
				return a.fail(2, fmt.Errorf("%s needs a value", x))
			}
			v := args[i+1]
			i++
			if x == "--task" {
				task = &v
			} else {
				base, hasBase = v, true
			}
		case strings.HasPrefix(x, "--task="):
			v := strings.TrimPrefix(x, "--task=")
			task = &v
		case strings.HasPrefix(x, "--base="):
			base, hasBase = strings.TrimPrefix(x, "--base="), true
		default:
			rest = append(rest, x)
		}
	}
	args = rest

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help":
			a.usage(a.env.Stdout)
			return 0
		case "-V", "--version":
			a.printf("anycli-launcher %s\n", Version)
			return 0
		case "init":
			return a.cmdInit(args[1:])
		}
	}
	if err := a.load(); err != nil {
		return a.fail(1, err)
	}
	if task != nil || hasBase {
		isTaskCmd := len(args) > 0 && args[0] == "task" && !projects.ExactExists(projects.Discover(a.cfg), "task")
		switch {
		case mode != clis.Fresh:
			return a.fail(2, errors.New("--task starts a new session; it can't be combined with --resume"))
		case task == nil && !isTaskCmd:
			return a.fail(2, errors.New("--base needs --task (usage: ac <project> --task \"...\" [--base <ref>])"))
		case task != nil && isTaskCmd:
			return a.fail(2, errors.New("use either 'ac task <project> \"...\"' or 'ac <project> --task \"...\"'"))
		case task != nil:
			if len(args) != 1 {
				return a.fail(2, errors.New("usage: ac <project> --task \"description\" [--base <ref>]"))
			}
			return a.startTask(args[0], *task, base)
		}
	}
	if len(args) == 0 {
		if mode != clis.Fresh {
			return a.fail(2, fmt.Errorf("--%s needs a project name", map[clis.Mode]string{clis.Resume: "resume", clis.Picker: "resume-picker"}[mode]))
		}
		return a.menu()
	}

	cmd := args[0]
	if wordCommands[cmd] && projects.ExactExists(projects.Discover(a.cfg), cmd) {
		return a.launchByName(cmd, mode)
	}
	switch cmd {
	case "help":
		a.usage(a.env.Stdout)
		return 0
	case "version":
		a.printf("anycli-launcher %s\n", Version)
		return 0
	case "-l", "--list", "list":
		return a.list()
	case "doctor":
		return a.doctor()
	case "setup":
		return a.setup()
	case "config":
		return a.editConfig()
	case "migrate":
		return a.cmdMigrate(args[1:])
	case "new":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		return a.newProject(name, true)
	case "remove", "rm":
		if len(args) < 2 {
			return a.fail(2, errors.New("remove needs a project name (usage: ac remove <name>)"))
		}
		return a.removeByName(args[1])
	case "resume", "pick":
		if len(args) < 2 {
			return a.fail(2, fmt.Errorf("%s needs a project name (usage: ac %s <name>)", cmd, cmd))
		}
		m := clis.Resume
		if cmd == "pick" {
			m = clis.Picker
		}
		return a.launchByName(args[1], m)
	case "--":
		if len(args) < 2 {
			return a.fail(2, errors.New("-- needs a project name"))
		}
		return a.launchByName(args[1], mode)
	case "task":
		if mode != clis.Fresh {
			return a.fail(2, errors.New("a task starts a new session; it can't be combined with --resume"))
		}
		if len(args) < 3 {
			return a.fail(2, errors.New("usage: ac task <project> \"description\" [--base <ref>]"))
		}
		return a.startTask(args[1], strings.Join(args[2:], " "), base)
	case "running", "ls-tasks":
		return a.cmdRunning()
	case "finish":
		return a.cmdFinish(args[1:])
	case "prune":
		return a.cmdPrune()
	}
	if strings.HasPrefix(cmd, "-") {
		a.eprintf("ac: unknown option: %s\n", cmd)
		a.usage(a.env.Stderr)
		return 2
	}
	return a.launchByName(cmd, mode)
}

func (a *app) cmdInit(args []string) int {
	if len(args) != 1 {
		return a.fail(2, fmt.Errorf("usage: ac init <%s>", strings.Join(shim.Shells(), "|")))
	}
	s, err := shim.Script(args[0])
	if err != nil {
		return a.fail(2, err)
	}
	a.printf("%s", s)
	return 0
}

func (a *app) requireRoot() error {
	root := a.cfg.ProjectsRoot()
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return fmt.Errorf("projects directory not found: %s\n    run 'ac setup', set projects_dir in %s, or export AC_PROJECTS_DIR", root, a.env.ConfigPath)
	}
	return nil
}

func (a *app) launchByName(query string, mode clis.Mode) int {
	if err := a.requireRoot(); err != nil {
		return a.fail(1, err)
	}
	matches := projects.ResolveRanked(projects.Discover(a.cfg), query, a.scores())
	switch len(matches) {
	case 0:
		a.eprintf("ac: no project matching '%s'\n    run 'ac list' to see what is available\n", query)
		return 1
	case 1:
		return a.launchProject(matches[0], mode)
	}
	a.eprintf("ac: '%s' matches more than one project:\n", query)
	for _, m := range matches {
		a.eprintf("      %s\n", m.Label())
	}
	return 1
}

func (a *app) launchProject(p projects.Project, mode clis.Mode) int {
	if p.OverlayErr != nil {
		a.eprintf("ac: ignoring %s: %v\n", config.OverlayFile, p.OverlayErr)
	}
	session := p.Name
	if p.Group != "" {
		session = p.Group + "/" + p.Name
	}
	return a.launchDir(p.Dir, session, p.Settings, mode)
}

// cliFor is the CLI a project launches and its cli_launch override (a
// project-level cli drops the global override).
func (a *app) cliFor(s config.Project) (cli, launchOverride string) {
	cli, launchOverride = a.cfg.CLI, a.cfg.CLILaunch
	if s.CLI != "" && s.CLI != cli {
		cli, launchOverride = s.CLI, ""
	}
	return cli, launchOverride
}

// launchHook lets a task launch see which backend runs it and record the
// CLI's pid. The zero value is a plain launch.
type launchHook struct {
	pidFile string
	// onBackend is called with the concrete backend right before each
	// launch attempt (again with "here" on a fallback).
	onBackend func(name string)
}

// launchDir launches the CLI in dir with the given settings. session names
// the project for tmux/zellij.
func (a *app) launchDir(dir, session string, s config.Project, mode clis.Mode) int {
	return a.launchDirWith(dir, session, s, mode, launchHook{})
}

func (a *app) launchDirWith(dir, session string, s config.Project, mode clis.Mode, hook launchHook) int {
	cli, launchOverride := a.cliFor(s)
	if cli == "" {
		return a.fail(1, errors.New("no AI CLI configured yet; run 'ac setup'"))
	}
	if _, err := a.env.LookPath(cli); err == nil {
		a.record(dir)
	}
	prof, _ := clis.Lookup(cli)
	inv := prof.Build(launchOverride, config.ResolveFlags(a.cfg.DefaultFlags, s), s.Prompt, mode)
	for _, n := range inv.Notes {
		a.eprintf("  %s\n", n)
	}
	color := s.Color
	if !config.ValidColor(color) {
		color = "" // doctor reports it; never pass junk to a terminal
	}
	req := launch.Request{
		Dir: dir, Name: cli, Args: inv.Args, Title: s.Title, CDFile: a.cdFile,
		Color: color, Session: session, Resume: mode != clis.Fresh, PIDFile: hook.pidFile,
	}
	setting, explicit := a.backendSetting(s)
	name := launch.Choose(setting, s.Color != "" || s.Title != "")
	if hook.onBackend != nil {
		hook.onBackend(name)
	}
	code, err := a.env.NewBackend(name, a.launchDeps()).Launch(req)
	if errors.Is(err, launch.ErrUnavailable) && name != launch.BackendHere {
		// v3 parity: no usable terminal means the current tab. Say so only
		// when the backend was asked for by name.
		if explicit {
			a.eprintf("ac: %s backend unavailable (%s); launching here\n", name, unwrapUnavailable(err))
		}
		if hook.onBackend != nil {
			hook.onBackend(launch.BackendHere)
		}
		code, err = a.env.NewBackend(launch.BackendHere, a.launchDeps()).Launch(req)
	}
	if err != nil {
		if errors.Is(err, launch.ErrNotFound) {
			a.eprintf("ac: %v (configured cli %q); run 'ac doctor'\n", err, cli)
			return code
		}
		return a.fail(code, err)
	}
	return code
}

// backendSetting is the backend asked for: --backend, else the project's,
// else the global one. explicit is false when that resolves to auto.
// Unknown names (config typos) act as auto, with a note.
func (a *app) backendSetting(s config.Project) (setting string, explicit bool) {
	setting = a.backend
	if setting == "" {
		setting = s.Backend
	}
	if setting == "" {
		setting = a.cfg.Backend
	}
	if !launch.ValidBackend(setting) {
		a.eprintf("ac: unknown backend %q in config; using auto\n", setting)
		setting = launch.BackendAuto
	}
	if setting == "" {
		setting = launch.BackendAuto
	}
	return setting, setting != launch.BackendAuto
}

func unwrapUnavailable(err error) string {
	return strings.TrimPrefix(err.Error(), launch.ErrUnavailable.Error()+": ")
}

// launchDeps wires the outside world into the backends.
func (a *app) launchDeps() launch.Deps {
	term := a.cfg.Terminal
	if !launch.ValidTerminal(term) {
		term = ""
	}
	return launch.Deps{
		Stdin: a.env.Stdin, Stdout: a.env.Stdout, Stderr: a.env.Stderr,
		LookPath: a.env.LookPath, Env: a.env.LaunchEnv, Terminal: term, Now: a.env.Now,
	}
}

// detectTerminal is the tab terminal a launch would use, and its program.
func (a *app) detectTerminal() (launch.Detection, string) {
	d := a.launchDeps()
	wt := launch.FindWT(d.Env, d.LookPath)
	det := launch.DetectTerminal(d.Env, d.Terminal, wt != "")
	prog := wt
	if det.Terminal != launch.TermWT {
		prog = launch.FindTerminalProgram(det.Terminal, d.Env, d.LookPath)
	}
	return det, prog
}

// globalBackend is the menu-level setting: --backend, else config, else auto.
func (a *app) globalBackend() string {
	setting := a.backend
	if setting == "" {
		setting = a.cfg.Backend
	}
	if setting == "" || !launch.ValidBackend(setting) {
		setting = launch.BackendAuto
	}
	return setting
}

// backendLabel is the header's backend: "here", "tmux", "auto: wt" (tabs
// for projects with a color/title go to Windows Terminal), "tab: none".
func (a *app) backendLabel() string {
	setting := a.globalBackend()
	switch setting {
	case launch.BackendAuto, launch.BackendTab:
		det, prog := a.detectTerminal()
		target := det.Terminal.Label()
		if det.Terminal == launch.TermNone || prog == "" {
			target = "here"
			if setting == launch.BackendTab {
				target = "none"
			}
		}
		return setting + ": " + target
	}
	return setting
}

// scores loads frecency scores; a missing or unreadable history is empty.
func (a *app) scores() map[string]float64 {
	s, _ := frecency.Load(a.env.FrecencyPath)
	return s.Scores(a.env.Now())
}

// record adds a launch of dir to the history. It runs before the CLI starts
// (the CLI may run for hours, and other ac's should see it). Failures are
// ignored here; 'ac doctor' reports an unwritable or corrupt history.
func (a *app) record(dir string) {
	_ = frecency.Record(a.env.FrecencyPath, dir, a.env.Now())
}

// sortProjects applies sort_order: "recent"/"frecent" by frecency (ties in
// name order), anything else by name (Discover's order).
func (a *app) sortProjects(list []projects.Project, scores map[string]float64) []projects.Project {
	if config.FrecencySort(a.cfg.SortOrder) {
		return projects.SortByFrecency(list, scores)
	}
	return list
}

// cdOnly hands dir to the shim without launching anything.
func (a *app) cdOnly(dir string) int {
	if a.cdFile == "" {
		a.eprintf("ac: shell integration not active, so ac can't change your directory.\n    Add to your shell rc: eval \"$(ac init %s)\"\n    Then: cd %s\n", guessShell(), dir)
		return 0
	}
	if err := launch.WriteCDFile(a.cdFile, dir); err != nil {
		return a.fail(1, err)
	}
	return 0
}

func guessShell() string {
	sh := filepath.Base(os.Getenv("SHELL"))
	switch sh {
	case "zsh", "fish", "bash":
		return sh
	}
	if os.Getenv("PSModulePath") != "" && os.Getenv("SHELL") == "" {
		return "pwsh"
	}
	return "bash"
}

func (a *app) list() int {
	if err := a.requireRoot(); err != nil {
		return a.fail(1, err)
	}
	for _, p := range projects.Discover(a.cfg) {
		if l := p.Label(); l != p.Name {
			a.printf("%s\t%s\n", p.Name, l)
		} else {
			a.printf("%s\n", p.Name)
		}
	}
	return 0
}

// configDir is the CLI's settings folder when it exists (menu shortcut).
func (a *app) configDir() string {
	d := a.cfg.CLIConfigDir
	if d == "" {
		prof, _ := clis.Lookup(a.cfg.CLI)
		d = prof.ConfigDir
	}
	if d == "" {
		return ""
	}
	d = config.ExpandHome(d)
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d
	}
	return ""
}

// entries builds the numbered menu: terminal, root, [config], projects.
func (a *app) entries(list []projects.Project) []tui.Entry {
	root := a.cfg.ProjectsRoot()
	es := []tui.Entry{
		{Kind: tui.KindTerminal, Label: "terminal", Dir: root},
		{Kind: tui.KindRoot, Label: filepath.Base(root), Dir: root},
	}
	if d := a.configDir(); d != "" {
		es = append(es, tui.Entry{Kind: tui.KindConfig, Label: filepath.Base(d), Dir: d})
	}
	for _, p := range list {
		es = append(es, tui.Entry{Kind: tui.KindProject, Label: p.Label(), Dir: p.Dir})
	}
	return es
}

func (a *app) shouldRunWizard() bool {
	if a.cfgExists || !a.env.IsTTY {
		return false
	}
	if d := os.Getenv("AC_PROJECTS_DIR"); d != "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return false
		}
	}
	return true
}

func (a *app) menu() int {
	if a.shouldRunWizard() {
		if code := a.wizard(); code != 0 {
			return code
		}
	}
	if a.env.RunMenu == nil {
		return a.fail(1, errors.New("interactive menu unavailable"))
	}
	for {
		if err := a.requireRoot(); err != nil {
			return a.fail(1, err)
		}
		scores := a.scores()
		list := a.sortProjects(projects.Discover(a.cfg), scores)
		prof, _ := clis.Lookup(a.cfg.CLI)
		res, err := a.env.RunMenu(tui.Options{
			Scores:    scores,
			Version:   Version,
			CLI:       a.cfg.CLI,
			Entries:   a.entries(list),
			ShowGit:   a.cfg.ShowGitStatus,
			CanResume: len(prof.Resume) > 0,
			CanPick:   len(prof.Picker) > 0,
			Backend:   a.backendLabel(),
			Theme:     tui.NewTheme(a.cfg.Theme, nil),
			TaskCount: a.taskCount(),
			LoadTasks: a.taskRows,
		})
		if err != nil {
			return a.fail(1, err)
		}
		switch res.Action {
		case tui.ActQuit:
			return 0
		case tui.ActNew:
			a.newProject("", false)
		case tui.ActConfig:
			a.editConfig()
		case tui.ActRemove:
			if p, ok := byDir(list, res.Entry.Dir); ok {
				a.removeProject(p)
			}
		case tui.ActTaskOpen:
			return a.openTask(res.Task)
		case tui.ActTaskFinish:
			if t, ok := a.findTask(res.Task); ok {
				a.finishTask(t, false)
			}
		case tui.ActSelect:
			switch res.Entry.Kind {
			case tui.KindTerminal:
				return a.cdOnly(res.Entry.Dir)
			case tui.KindRoot, tui.KindConfig:
				base := filepath.Base(res.Entry.Dir)
				return a.launchDir(res.Entry.Dir, base, a.cfg.Settings(base, nil), res.Mode)
			default:
				if p, ok := byDir(list, res.Entry.Dir); ok {
					return a.launchProject(p, res.Mode)
				}
				return 1
			}
		}
		// Submenus may have changed the config or the project list.
		if err := a.load(); err != nil {
			return a.fail(1, err)
		}
	}
}

func byDir(list []projects.Project, dir string) (projects.Project, bool) {
	for _, p := range list {
		if p.Dir == dir {
			return p, true
		}
	}
	return projects.Project{}, false
}

func (a *app) editConfig() int {
	path := a.env.ConfigPath
	if !config.Exists(path) {
		if err := config.Save(path, a.cfg); err != nil {
			return a.fail(1, err)
		}
	}
	if a.env.Editor == nil {
		a.printf("Config file: %s\n(set $EDITOR to open it with 'ac config')\n", path)
		return 0
	}
	if err := a.env.Editor(path); err != nil {
		return a.fail(1, err)
	}
	return 0
}

func (a *app) usage(w io.Writer) {
	fmt.Fprintf(w, `anycli-launcher %s - launch your AI CLI in a project directory

Usage:
  ac                       open the interactive menu
  ac <name>                launch directly in the matching project
  ac <name> -r|--resume    resume the project's most recent session
  ac <name> --resume-picker  pick a session to resume
  ac --backend <b> <name>  launch via b: here, tab, tmux, zellij or auto
                           (auto: a new tab when the project sets a color
                           or title, else here)
  ac <name> --task "..."   new git worktree on branch ac/<slug>, CLI started
                           there with the task as its prompt (--base <ref>)
  ac task <name> "..."     same as --task
  ac running | ls-tasks    list task sessions (status, branch, git state)
  ac finish <task> [--delete-branch]
                           remove a task's worktree (asks; keeps the branch)
  ac prune                 forget tasks whose worktree is gone
  ac resume <name>         same as --resume
  ac pick <name>           same as --resume-picker
  ac list                  list projects (name, or name<TAB>label)
  ac new [name]            create a project folder (+ git init) in the root
  ac remove <name>         delete a project folder (asks first; exact name)
  ac config                open the config file in $EDITOR
  ac setup                 re-run guided setup (keeps existing values)
  ac doctor                check configuration and dependencies
  ac migrate [file]        import a v3 config.yaml into config.toml
  ac init <bash|zsh|fish|pwsh>  print shell integration
  ac help | version

<name> matches a directory name or alias: exact, case-insensitive, prefix,
substring, then fuzzy (letters in order). When several prefix/substring/fuzzy
matches remain, the one you launch most (frecency) wins. A project named like help/list/config/doctor/setup/resume/
pick/version/task/running/ls-tasks/finish/prune takes precedence over that
subcommand.

Shell integration (needed for ac to cd your shell):
  bash/zsh: eval "$(ac init bash)"     fish: ac init fish | source
  pwsh:     Invoke-Expression (& (Get-Command ac -CommandType Application | Select-Object -First 1).Source init pwsh | Out-String)

Config: %s
`, Version, a.env.ConfigPath)
}
