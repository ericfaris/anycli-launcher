package launch

import "fmt"

// PlanAttach is the command that puts this terminal into an existing
// multiplexer session (a task session's "open"), never starting a CLI:
//
//   - tmux: attach-session, or switch-client when already inside tmux.
//   - zellij: attach; inside zellij there is no reliable CLI switch, so it
//     is unavailable and the caller says how to attach by hand.
//
// session is the final session name (SessionName output), not a project.
func PlanAttach(backend, session string, inside bool) ([]string, error) {
	switch backend {
	case BackendTmux:
		if inside {
			return []string{"switch-client", "-t", "=" + session}, nil
		}
		return []string{"attach-session", "-t", "=" + session}, nil
	case BackendZellij:
		if inside {
			return nil, fmt.Errorf("%w: already inside zellij; detach and run: zellij attach %s", ErrUnavailable, session)
		}
		return []string{"attach", session}, nil
	}
	return nil, fmt.Errorf("%w: %s sessions can't be attached", ErrUnavailable, backend)
}

// Attach runs PlanAttach's command in the foreground. Outside a multiplexer
// the cd-file gets dir first (the attach takes over this terminal, as a
// fresh tmux launch does).
func Attach(backend, session, dir, cdFile string, d Deps) (int, error) {
	d = d.withDefaults()
	bin, err := d.LookPath(backend)
	if err != nil {
		return 0, fmt.Errorf("%w: %s not found on PATH", ErrUnavailable, backend)
	}
	inside := false
	switch backend {
	case BackendTmux:
		inside = d.Env.Getenv("TMUX") != ""
	case BackendZellij:
		inside = d.Env.Getenv("ZELLIJ") != ""
	}
	args, err := PlanAttach(backend, session, inside)
	if err != nil {
		return 0, err
	}
	if backend == BackendTmux && d.TmuxSocket != "" {
		args = append([]string{"-L", d.TmuxSocket}, args...)
	}
	if !inside {
		if err := WriteCDFile(cdFile, dir); err != nil {
			return 1, err
		}
	}
	return d.Runner.Run(Command{Path: bin, Args: args})
}
