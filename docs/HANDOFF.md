# Handoff: anycli-launcher

**What:** `ac` picks a project, cds into it, and launches an AI CLI.
- v4: Go rewrite on branch `v4` (phases 1-2: core, frecency, filter). Phase 3 (pluggable backends: tab, tmux, zellij) is on branch `v4-phase3`, pushed and not yet merged into `v4`. See `docs/V4-PLAN.md`.
- v3: 5k-line bash (`anycli-launcher.bash`), on branch `v3` and still on `main` until v4 lands. The v3 files are still in the v4 tree; delete them at cutover.

## Layout (v4)
- `cmd/ac`: main and real wiring (TTY detection, Bubble Tea program, $EDITOR). `shim_e2e_test.go` evals the real shim in bash/zsh/fish/pwsh. `tab_e2e_test.go` runs a whole tab launch through a fake `kitty` (the test binary) and the real trampoline.
- `internal/app`: dispatch, menu loop, doctor/new/remove/setup/wizard/migrate, backend selection (`backendSetting`, `launchDeps`, `backendLabel`). All I/O goes through `Env` (`NewBackend`, `LaunchEnv` are the launch seams), so tests need no terminal.
- `internal/config`: TOML load/save (atomic), `.ac.toml` overlay, v3 YAML/flat import plus the legacy key repair. `backend` (global + per project) and `terminal` (global) keys.
- `internal/projects`: discovery, labels, resolve tiers, `SortByFrecency`, git status.
- `internal/frecency`, `internal/fuzzy`, `internal/clis`, `internal/shim`, `internal/tui`: as in phase 2. The TUI header shows `Options.Backend`.
- `internal/launch`:
  - `launch.go`: `Request`, `Backend`, `Here` (unchanged from phase 2).
  - `registry.go`: names, `Choose` (auto rule), `Deps`, `New`, `ErrUnavailable`, `Unverified`.
  - `runner.go`: `Command` + `Runner` (Run = foreground with stdio, Output = captured). Every backend builds commands as data.
  - `detect.go`: platform (WSL via env or /proc/version), terminal detection, `FindWT`.
  - `tab.go`: `BuildTab` (pure argv per terminal), `Tab` backend, spec files.
  - `tabexec.go` + `exec_unix.go`/`exec_other.go`: the `ac __tab-exec <spec>` trampoline (login re-exec through `bash -l`, cd, OSC title, iTerm2 color, TTY drain, exec).
  - `tmux.go`, `zellij.go`: `PlanTmux`/`PlanZellij` (pure) + backends. `quote.go`: ShQuote, AppleScript, KDL, tmux-format and wt escaping.

## Commands
- Test: `go test ./...`. Goldens: `go test ./internal/tui -update`.
- Shim e2e with required shells: `AC_REQUIRE_SHELLS=bash,zsh go test -run TestShimE2E ./cmd/ac`.
- tmux integration (auto-skips without tmux): `go test -run TestTmuxIntegration ./internal/launch`.
- Local Go: `PATH=$HOME/.local/go/bin:$PATH` (1.27.4). Docker fallback:
  `docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.27.4 go test ./...`
  (add `--user $(id -u):$(id -g) -e HOME=/tmp` to avoid root-owned files).
- CI: `.github/workflows/go.yml` (go test on 3 OSes, plus the shim smoke job). It runs on pushes to `main`, `v4` and `v4-*`, and on PRs. The v3 `cross-platform-tests.yml` still runs on main.

## Open items (priority)
1. Merge `v4-phase3` into `v4` once CI is green.
2. Manual checks on real terminals (only unit/e2e-tested with fakes):
   - Windows Terminal from WSL: `wsl.exe ... --cd <dir> --exec ac __tab-exec <spec>` (v3 used `-- bash -l script`), title, tab color, the TTY drain (no `2c/3434` junk), `-w 0` landing in the current window, dirs with spaces.
   - Windows Terminal native/Git Bash: `-d <dir> -- ac.exe __tab-exec <spec>` with spaces in the user profile path; a `.cmd` CLI shim (npm `claude.cmd`) run by the trampoline.
   - iTerm2 (new tab + `write text`, OSC 6 tab color), Terminal.app (opens a window), kitty (`allow_remote_control`), WezTerm, GNOME Terminal (`--tab` against the running server).
   - zellij: every flag (not installed when written).
   - tmux: real attach/switch-client in a terminal (the integration test stubs only those two calls).
3. Verify the codex/goose/aider/amp profiles against a real `--help` (only claude is verified).
4. Phases 4-5 per plan. Delete the v3 bash files at cutover.
5. Issue #10 (macOS v3 failures): fix only on `v3`, if at all.

## Decisions that may be revisited
- Go over modular bash; TOML over YAML; self-update, theme editor and changelog viewer dropped.
- User `config.toml` overrides `.ac.toml` field by field. Overlay `cli` must be a bare command name, and overlay `backend` one of the fixed names (an invalid one rejects the whole overlay, like a bad `cli`). `terminal` is global only.
- `auto` (default) = tab when the project has color or title, else here (v3). An unavailable backend falls back to here: silently for auto, with a note when named.
- Tabs run `ac __tab-exec <spec>` instead of v3's generated bash script: user data travels as JSON in a 0600 temp file, never on a terminal command line. Only the ac path and spec path go there, and on Unix they must match `SafePath` (else tab is unavailable and ac runs here). Stale specs (>24h) are swept.
- Tabs don't write the cd-file (the calling shell stays put, as in v3). tmux/zellij write it when they attach in this terminal, not when switching inside tmux.
- On Unix, tab trampolines re-exec through `bash -l` (or `/bin/sh -l`) so the CLI gets the login env, except iTerm2/Terminal.app (they type into an interactive shell). Native Windows runs the CLI as a child.
- tmux: one session per project (`SessionName`: `[A-Za-z0-9_-]`, plus a hash when anything was replaced). Fresh launch on an existing session = new window; resume = attach only. Always `new-session -d` then attach, so all paths are testable. No `-e` env passing (needs tmux 3.2+); the CLI path is absolute.
- Terminal.app gets a new window, not a tab (tabs need System Events + Accessibility).
- kitty/WezTerm/GNOME get no tab color (no CLI/escape support used); titles come from the trampoline's OSC 0.
- Phase 2 decisions (filter keys, frecency tiers, recording before launch) are unchanged.
- Goldens: two new (`menu_backend_auto`, `menu_backend_tmux`); every existing golden is byte-identical.

## Gotchas
- tmux expands `-c` and `-n` as formats: `#(cmd)` in a directory or title runs cmd. `TmuxFormatEscape` doubles `#`. The integration test fails without it.
- `gofmt` rewrites `''` in doc comments to a typographic quote. Don't write `'\''` in a comment.
- Bubble Tea v1's `init()` queries the terminal background (OSC 11) in every `ac` run with a TTY stdout. A fake pty that never replies stalls about 5s.
- PowerShell has a built-in `ac` alias (Add-Content); macOS has `/usr/sbin/ac`. Tests never call a bare `ac`.
- Tests must never write the real history (`Env.FrecencyPath`, `AC_FRECENCY_FILE`). Windows ignores `HOME` for `os.UserHomeDir`.
- `Env.LaunchEnv` must be stubbed in app tests: this dev box is WSL with wt.exe on PATH, so real detection says "wt".
- Never run real wt.exe/osascript from tests; the e2e uses a fake `kitty`.
- See `LESSONS.md` for CI history.
