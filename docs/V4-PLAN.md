# anycli-launcher v4 — Go rewrite plan

Approved 2026-11-26. v3 (bash) lives on branch `v3` for fixes only.

## Goal
`ac`: pick a project, cd into it, launch an AI CLI. As a single static Go
binary plus a tiny shell shim. No bash-4, zsh-shim, BSD/GNU or Git Bash problems.

## Architecture
- `cmd/ac` — binary. `ac init <bash|zsh|fish|pwsh>` prints the shim; the shim
  defines `ac()` which runs `ac --cd-file <tmp> ...` and `cd`s to the path
  the binary writes there. Binary never needs to change parent cwd itself.
- `internal/config` — TOML (`~/.config/anycli-launcher/config.toml`),
  per-project `.ac.toml` overlay, `ac migrate` imports v3 `config.yaml`.
- `internal/projects` — discovery (roots, extra dirs), name resolution, aliases.
- `internal/frecency` — launch history -> score (zoxide-style), JSON store.
- `internal/clis` — declarative CLI profiles (claude, codex, aider, goose,
  kiro-cli, amp): launch args, resume flag, list-sessions cmd, prompt style.
  Flags verified against each tool's real `--help` where installed.
- `internal/launch` — backends: `here`, `tab` (wt, iterm, kitty, wezterm,
  gnome-terminal), `tmux`/`zellij` (session per project, reattach).
- `internal/worktree` — `ac <proj> --task "..."` -> git worktree + agent; a
  `running` view of live sessions.
- `internal/tui` — Bubble Tea: number keys preserved, type-to-filter, git
  status, 3-4 theme presets (no font/custom theme editor).
- Dropped from v3: self-update (use brew/scoop/go install/releases), theme
  system, changelog viewer, font config.
- Kept: doctor, new, remove (with all v3 safety guards: path under root, not
  `/` or `$HOME`, unsaved-work confirm, gh verified before delete).

## Phases
1. Core: config, projects, clis, launch(here), tui, shim, migrate, tests, CI.
2. D: filter + frecency + `ac <partial>`.
3. E: tab backends + tmux/zellij (branch `v4-phase3`; tab via the `ac __tab-exec` trampoline).
4. F: worktree agents + running view.
5. goreleaser, Homebrew tap, scoop, install.sh.

## Testing
Table tests per package; golden-file TUI frames; e2e shim smoke test per shell;
CI matrix linux/macos/windows. No mocks of the filesystem where t.TempDir works.

## Build
No local Go: `docker run --rm -v "$PWD":/src -w /src golang:1 go test ./...`
