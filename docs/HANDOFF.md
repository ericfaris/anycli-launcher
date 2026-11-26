# Handoff: anycli-launcher

**What:** `ac` picks a project, cds into it, and launches an AI CLI.
- v4: Go rewrite on branch `v4`. Phase 1 (core) is done and unpushed. See `docs/V4-PLAN.md`.
- v3: 5k-line bash (`anycli-launcher.bash`), on branch `v3` and still on `main` until v4 lands. The v3 files are still in the v4 tree; delete them at cutover.

## Layout (v4)
- `cmd/ac`: main and real wiring (TTY detection, Bubble Tea program, $EDITOR). `shim_e2e_test.go` evals the real shim in bash/zsh/fish/pwsh.
- `internal/app`: dispatch, menu loop, doctor/new/remove/setup/wizard/migrate. All I/O goes through `Env`, so tests need no terminal.
- `internal/config`: TOML load/save (atomic), `.ac.toml` overlay, v3 YAML/flat import plus the legacy key repair.
- `internal/projects`: discovery (roots, extra dirs, scan_depth 2 groups), labels, resolve, git status.
- `internal/clis`: profile table, argv builder, quote-aware flag splitting.
- `internal/launch`: `here` backend. Writes the cd-file first, then runs the CLI as a child.
- `internal/tui`: Bubble Tea model, 3 themes, golden tests in `testdata/`.
- `internal/shim`: `ac init` scripts.

## Commands
- Test: `go test ./...`. Goldens: `go test ./internal/tui -update`.
- Shim e2e with required shells: `AC_REQUIRE_SHELLS=bash,zsh go test -run TestShimE2E ./cmd/ac`.
- **Local Go is broken** (`~/.local/go`, 1.27.4 extracted over an older tree, so stale files in `src/internal/strconv` break every build). Fix it with `rm -rf ~/.local/go`, then re-extract. Until then use Docker:
  `docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.27.4 go test ./...`
  (add `--user $(id -u):$(id -g) -e HOME=/tmp` to avoid root-owned files).
- CI: `.github/workflows/go.yml` (go test on 3 OSes, plus the shim smoke job). The v3 `cross-platform-tests.yml` still runs on main.

## Open items (priority)
1. Push `v4`, watch the first CI run. Windows and macOS have only been checked locally via cross-compiled `go vet`, not executed.
2. Phase 2: frecency, type-to-filter, `ac <partial>` ranking. `sort_order = "recent"` is accepted but sorts by name until then.
3. Phase 3: tab backends. They should use `color`/`title`. In phase 1 every launch is "here"; v3 opened a new wt.exe tab when color or title was set.
4. Verify the codex/goose/aider/amp profiles against a real `--help` (only claude is verified).
5. Phases 4-5 per plan. Delete the v3 bash files at cutover.
6. Issue #10 (macOS v3 failures): fix only on `v3`, if at all.

## Decisions that may be revisited
- Go over modular bash; TOML over YAML; self-update, theme editor and changelog viewer dropped.
- User `config.toml` overrides `.ac.toml` field by field. The overlay `cli` must be a bare command name, because the file is untrusted repo content.
- `ac remove` requires an exact or case-insensitive-exact name/label, never a fuzzy match. It refuses a folder that *contains* a root (new vs v3).
- A repo with `.git` but no `git` on PATH counts as "unsaved work", so you must type the name (new vs v3).
- From the menu, `n`/`x`/`c` run line-based prompts outside Bubble Tea, then the menu reopens.
- `show_sessions` (session listing in the menu) is not ported yet; `ListSessions` is kept in the profile table.

## Gotchas
- Bubble Tea v1's `init()` queries the terminal background (OSC 11) in every `ac` run with a TTY stdout. Real terminals reply instantly. A fake pty that never replies stalls about 5s, so wait at least 6s when driving the binary from a pty in tests.
- PowerShell has a built-in `ac` alias (Add-Content). The pwsh shim removes it.
- See `LESSONS.md` for CI history.
