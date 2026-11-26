# Handoff: anycli-launcher

**What:** `ac` picks a project, cds into it, and launches an AI CLI.
- v4: Go rewrite on branch `v4` (phase 1, core). Phase 2 (frecency, type-to-filter, frecent `ac <partial>`) is on branch `v4-phase2`, pushed and not yet merged into `v4`. See `docs/V4-PLAN.md`.
- v3: 5k-line bash (`anycli-launcher.bash`), on branch `v3` and still on `main` until v4 lands. The v3 files are still in the v4 tree; delete them at cutover.

## Layout (v4)
- `cmd/ac`: main and real wiring (TTY detection, Bubble Tea program, $EDITOR). `shim_e2e_test.go` evals the real shim in bash/zsh/fish/pwsh.
- `internal/app`: dispatch, menu loop, doctor/new/remove/setup/wizard/migrate. All I/O goes through `Env`, so tests need no terminal.
- `internal/config`: TOML load/save (atomic), `.ac.toml` overlay, v3 YAML/flat import plus the legacy key repair.
- `internal/projects`: discovery (roots, extra dirs, scan_depth 2 groups), labels, resolve (exact > ci-exact > prefix > substring > fuzzy, frecency breaks ties in the last three), `SortByFrecency`, git status.
- `internal/frecency`: zoxide-style history in `~/.local/state/anycli-launcher/frecency.json` (`$XDG_STATE_HOME`, `$AC_FRECENCY_FILE`). Atomic save, corrupt file reads as empty, aging at total rank 1000, cap of 500 entries.
- `internal/fuzzy`: subsequence matcher with fzf-like scoring (exact DP, rune positions for highlighting).
- `internal/clis`: profile table, argv builder, quote-aware flag splitting.
- `internal/launch`: `here` backend. Writes the cd-file first, then runs the CLI as a child.
- `internal/tui`: Bubble Tea model, 3 themes, golden tests in `testdata/`. `filter.go` is type-to-filter.
- `internal/shim`: `ac init` scripts.

## Commands
- Test: `go test ./...`. Goldens: `go test ./internal/tui -update`.
- Shim e2e with required shells: `AC_REQUIRE_SHELLS=bash,zsh go test -run TestShimE2E ./cmd/ac`.
- Local Go: `PATH=$HOME/.local/go/bin:$PATH` (1.27.4, works again). Docker fallback:
  `docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.27.4 go test ./...`
  (add `--user $(id -u):$(id -g) -e HOME=/tmp` to avoid root-owned files).
- CI: `.github/workflows/go.yml` (go test on 3 OSes, plus the shim smoke job). It runs on pushes to `main`, `v4` and `v4-*`, and on PRs. The v3 `cross-platform-tests.yml` still runs on main.

## Open items (priority)
1. Merge `v4-phase2` into `v4` once its CI is green (open a PR or fast-forward).
2. Phase 3: tab backends. They should use `color`/`title`. In phase 1 every launch is "here"; v3 opened a new wt.exe tab when color or title was set.
3. Verify the codex/goose/aider/amp profiles against a real `--help` (only claude is verified).
4. Phases 4-5 per plan. Delete the v3 bash files at cutover.
5. Issue #10 (macOS v3 failures): fix only on `v3`, if at all.

## Decisions that may be revisited
- Go over modular bash; TOML over YAML; self-update, theme editor and changelog viewer dropped.
- User `config.toml` overrides `.ac.toml` field by field. The overlay `cli` must be a bare command name, because the file is untrusted repo content.
- `ac remove` requires an exact or case-insensitive-exact name/label, never a fuzzy match. It refuses a folder that *contains* a root (new vs v3).
- A repo with `.git` but no `git` on PATH counts as "unsaved work", so you must type the name (new vs v3).
- From the menu, `n`/`x`/`c` run line-based prompts outside Bubble Tea, then the menu reopens.
- `show_sessions` (session listing in the menu) is not ported yet; `ListSessions` is kept in the profile table.
- Filter keys: `/` opens the filter; any letter that isn't a menu key at an empty prompt opens it implicitly; `r`/`p`/`x` + letter becomes filter text. `n c q ? j k` keep their phase 1 meaning, so names starting with those need `/`. Inside the filter every key is text until Esc.
- The filter matches every menu row (terminal/root/config too), not only projects.
- Resolution keeps tiers before frecency: a never-used prefix match beats a frecent substring match. Frecency only picks within a tier (prefix, substring, fuzzy), and only on a strict win; ties stay ambiguous.
- A launch is recorded before the CLI runs, and only when the CLI is on PATH. Record errors are silent; doctor reports them.
- Zoxide's step buckets (x4/x2/x0.5/x0.25), not a smooth decay. Ties are common, and they fall back to name order.
- The `?` help screen gained a `/` row, so `help.golden` and `help_no_sessions.golden` changed. Every other phase 1 golden is byte-identical.

## Gotchas
- Bubble Tea v1's `init()` queries the terminal background (OSC 11) in every `ac` run with a TTY stdout. Real terminals reply instantly. A fake pty that never replies stalls about 5s, so wait at least 6s when driving the binary from a pty in tests.
- PowerShell has a built-in `ac` alias (Add-Content). The pwsh shim removes it.
- Tests must never write the real history. `Env.FrecencyPath` (harness) and `AC_FRECENCY_FILE` (shim e2e) handle that. Windows ignores `HOME` for `os.UserHomeDir`, so setting `HOME` alone is not enough.
- See `LESSONS.md` for CI history.
