# Handoff: anycli-launcher

**What:** `ac` picks a project, cds into it, and launches an AI CLI.
- v4: Go rewrite, all 5 phases (core, frecency, filter, backends, task worktrees, release tooling) merged to `main` (cutover done 2026-11-26). See `docs/V4-PLAN.md`.
- v3: the old 5k-line bash launcher lives only on branch `v3`. `tests/fixtures/` stays because the Go migrate tests use it.

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
  - `attach.go`: `PlanAttach`/`Attach` (attach-only, for opening a live task session). `Request.PIDFile`: Here writes the child's pid, the tab trampoline its own (just before exec).
- `internal/worktree` (phase 4): `slug.go` (Slug, SafeComponent, Unique, ValidBranch: only `ac/[a-z0-9-]`), `git.go` (argv-only git: TopLevel, ResolveBase, Add, List/Find by SameFile, Remove, DeleteBranch, Prune, Status, Version), `guard.go` (CheckPath: inside worktree_dir lexically and resolved, not a symlink; CheckRoot), `registry.go` (tasks.json: atomic, lock file, corrupt file set aside as `.corrupt-<unix>`, junk ids dropped), `live.go` + `proc_*.go` (Probe: tmux has-session + window_activity for idle, zellij list-sessions, pid alive + Linux /proc cwd check).
- `internal/app`: `task.go` (startTask), `running.go` (ac running, openTask, finish, prune), `doctor_tasks.go`. `launchDirWith` + `launchHook` record the backend actually used (fallback included) before the CLI starts.
- `internal/buildinfo` (phase 5): `Get()` = version/commit/date from goreleaser's `-X .../internal/buildinfo.{version,commit,date}`, else the `go install` module version + VCS stamp, else `4.0.0-dev`. `Detect(Host)` guesses the install method from the binary's resolved path (Cellar/Caskroom, scoop apps, GOBIN/GOPATH/bin, `~/.local/bin` or `AC_INSTALL_DIR`, `%LOCALAPPDATA%\Programs\anycli-launcher`); `UpdateHint` prints that method's command, or all of them. No network. Used by `ac --version` and the doctor's Version section (`Env.Install` is the test seam).
- Release tooling (phase 5): `.goreleaser.yaml`, `.github/workflows/release.yml` (tag `v*` only), `install.sh` (POSIX sh), `install.ps1`. `internal/release` has only tests: both scripts against a fake release on httptest (fake `uname` covers every OS/arch), plus a cross-file agreement test (ldflags symbols, archive names, skip_upload guards, workflow pins).
- `internal/tui/running.go`: the `w` view (`Options.TaskCount`, `Options.LoadTasks` runs async; `ActTaskOpen`/`ActTaskFinish` + `Result.Task`).

## Commands
- Test: `go test ./...`. Goldens: `go test ./internal/tui -update`.
- Shim e2e with required shells: `AC_REQUIRE_SHELLS=bash,zsh go test -run TestShimE2E ./cmd/ac`.
- tmux integration (auto-skips without tmux): `go test -run 'TestTmuxIntegration|TestTmuxLiveness' ./internal/launch ./internal/worktree`.
- Task e2e (real binary + real git): `go test -run TestTaskE2E ./cmd/ac`.
- Local Go: `PATH=$HOME/.local/go/bin:$PATH` (1.27.4). Docker fallback:
  `docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.27.4 go test ./...`
  (add `--user $(id -u):$(id -g) -e HOME=/tmp` to avoid root-owned files).
- Release dry run (goreleaser v2.20.3, not in the repo; get it from its GitHub release into a scratch dir): `goreleaser check` and `goreleaser release --snapshot --clean --skip=sign,sbom` (archives, cask and scoop manifest in `dist/`). Install from it: copy `dist/*.tar.gz dist/*.zip dist/checksums.txt` to `<d>/latest/download/`, then `AC_RELEASES_URL=file://<d> sh install.sh --dir <bin>`.
- Install-script tests: `go test ./internal/release` (sh on Unix, ps1 on Windows). `shellcheck -s sh install.sh` (0.11.0, same as CI).
- CI: `.github/workflows/go.yml` (go test on 3 OSes, the shim smoke job, and `release-check`: shellcheck, goreleaser check + snapshot, install.sh from the snapshot under dash and bash). It runs on pushes to `main`, `v4` and `v4-*`, and on PRs. The v3 workflow is gone.

## Open items (priority)
1. Merge `v4-phase5` into `v4` once CI is green.
2. **One-time manual setup before the first release (owner only; nothing here was created by automation):**
   1. Create the public repo `ericfaris/homebrew-tap` with a `main` branch (an initial README commit is enough; goreleaser writes `Casks/anycli-launcher.rb`).
   2. Create the public repo `ericfaris/scoop-bucket` with a `main` branch (goreleaser writes `anycli-launcher.json` at the root).
   3. Mint a fine-grained PAT: resource owner `ericfaris`, only those two repos, permission Contents: read and write (nothing else), an expiry you'll remember.
   4. Add it to `ericfaris/anycli-launcher` as the Actions secret `TAP_GITHUB_TOKEN` (`gh secret set TAP_GITHUB_TOKEN -R ericfaris/anycli-launcher`, paste at the prompt). Without it, releases still publish to GitHub; the cask/scoop steps are skipped with a notice.
   5. Check Settings -> Actions -> General -> Workflow permissions doesn't block `contents: write` for the workflow (the job requests it explicitly).
3. **Released:** v4.0.0 tagged and published 2026-11-26 (signed checksums + SBOMs). `ericfaris/homebrew-tap` and `ericfaris/scoop-bucket` exist (public, empty). Remaining:
   - **The launcher repo is PRIVATE**, so anonymous `install.sh`, brew, scoop and `go install` all 404. Either make the repo public (owner's call) or accept authenticated-only (`gh release download`).
   - `TAP_GITHUB_TOKEN` is not set (fine-grained PATs can only be minted in the web UI), so the cask/scoop manifests were not published. After setting it, re-run the release job or tag v4.0.1.
   - Verify each install method + `cosign verify-blob` once public. Issue #10 is obsolete (v3 retired).
4. Manual checks for phase 4 on a real terminal:
   - `ac <p> --task "..."` with backend tmux: attach, `w` shows running, leave it quiet 10 min -> idle, Enter from the view re-attaches (and switch-client from inside tmux).
   - zellij task: `list-sessions` parsing (EXITED marker), attach from the view.
   - tab task (WT from WSL, iTerm2, kitty): the pid file appears and status goes running -> finished when the CLI exits. On native Windows the pid is the trampoline's.
   - `ac finish` on a dirty worktree with a live tmux session (prints the kill-session hint, does not kill it).
5. Manual checks on real terminals (only unit/e2e-tested with fakes):
   - Windows Terminal from WSL: `wsl.exe ... --cd <dir> --exec ac __tab-exec <spec>` (v3 used `-- bash -l script`), title, tab color, the TTY drain (no `2c/3434` junk), `-w 0` landing in the current window, dirs with spaces.
   - Windows Terminal native/Git Bash: `-d <dir> -- ac.exe __tab-exec <spec>` with spaces in the user profile path; a `.cmd` CLI shim (npm `claude.cmd`) run by the trampoline.
   - iTerm2 (new tab + `write text`, OSC 6 tab color), Terminal.app (opens a window), kitty (`allow_remote_control`), WezTerm, GNOME Terminal (`--tab` against the running server).
   - zellij: every flag (not installed when written).
   - tmux: real attach/switch-client in a terminal (the integration test stubs only those two calls).
6. Verify the codex/goose/aider/amp profiles against a real `--help` (only claude is verified).
7. Not testable without a real release: the cask on Linuxbrew, the scoop shim (`os.Executable` should resolve to `scoop\apps\...` so the update hint says scoop), cosign verification of a real bundle, and `go install .../v4/cmd/ac@v4.0.0` reporting `4.0.0` from the module version.

## Decisions that may be revisited
- Go over modular bash; TOML over YAML; self-update, theme editor and changelog viewer dropped.
- Phase 5 release: Homebrew **cask** (goreleaser deprecated `brews` formulae; the cask covers macOS and Linux and strips quarantine in `postflight_steps` since the binaries aren't notarized). Archive names carry no version (`anycli-launcher_<os>_<arch>`), so the scripts and `releases/latest/download/` need no API call. `install.sh`/`install.ps1` ship as release assets; update hints point at `releases/latest/download/install.sh`, not at `main`. One `TAP_GITHUB_TOKEN` for both tap and bucket. Release notes come from CHANGELOG.md's `## <version>` section when present, else goreleaser's grouped changelog. Module path has the `/v4` suffix (required for `go install ...@latest` to see v4 tags).
- User `config.toml` overrides `.ac.toml` field by field. Overlay `cli` must be a bare command name, and overlay `backend` one of the fixed names (an invalid one rejects the whole overlay, like a bad `cli`). `terminal` is global only.
- `auto` (default) = tab when the project has color or title, else here (v3). An unavailable backend falls back to here: silently for auto, with a note when named.
- Tabs run `ac __tab-exec <spec>` instead of v3's generated bash script: user data travels as JSON in a 0600 temp file, never on a terminal command line. Only the ac path and spec path go there, and on Unix they must match `SafePath` (else tab is unavailable and ac runs here). Stale specs (>24h) are swept.
- Tabs don't write the cd-file (the calling shell stays put, as in v3). tmux/zellij write it when they attach in this terminal, not when switching inside tmux.
- On Unix, tab trampolines re-exec through `bash -l` (or `/bin/sh -l`) so the CLI gets the login env, except iTerm2/Terminal.app (they type into an interactive shell). Native Windows runs the CLI as a child.
- tmux: one session per project (`SessionName`: `[A-Za-z0-9_-]`, plus a hash when anything was replaced). Fresh launch on an existing session = new window; resume = attach only. Always `new-session -d` then attach, so all paths are testable. No `-e` env passing (needs tmux 3.2+); the CLI path is absolute.
- Terminal.app gets a new window, not a tab (tabs need System Events + Accessibility).
- kitty/WezTerm/GNOME get no tab color (no CLI/escape support used); titles come from the trampoline's OSC 0.
- Phase 2 decisions (filter keys, frecency tiers, recording before launch) are unchanged.
- Goldens (phase 3): two new (`menu_backend_auto`, `menu_backend_tmux`).
- Phase 4: `w` opens the running view; the first key after it, if a letter outside `jkfrqw`, falls back to the filter with `w`+letter (so `web` still filters). `W` still filters. The footer gets `w tasks(N)` only when tasks exist, so menus without tasks are byte-identical. Goldens: `help` and `help_no_sessions` changed on purpose (new `w` row); new `menu_tasks`, `running*`.
- Task ids are `<SafeComponent(label)>/<slug>`; worktree at `<worktree_dir>/<id>`; tmux session `SessionName(label + "/" + slug)` (separate from the project's own session).
- `ac finish` asks for the typed branch name when dirty, unpushed, git state unreadable, or the CLI is still running; keeps the branch by default even though unpushed commits aren't lost then (spec). It never kills the tmux/zellij session; it prints the command.
- `--task` text may not start with `-` (the CLI would parse it as a flag) or contain NUL. `--base` may not start with `-` and is resolved to a SHA before `worktree add`.
- New word commands (`task running ls-tasks finish prune`) yield to same-named projects, like the v3 ones.
- Registry lives next to the frecency file (`AC_TASKS_FILE` overrides); writes take a `tasks.json.lock` (O_EXCL, 3s wait, broken after 10s).

## Gotchas
- tmux expands `-c` and `-n` as formats: `#(cmd)` in a directory or title runs cmd. `TmuxFormatEscape` doubles `#`. The integration test fails without it.
- `gofmt` rewrites `''` in doc comments to a typographic quote. Don't write `'\''` in a comment.
- Bubble Tea v1's `init()` queries the terminal background (OSC 11) in every `ac` run with a TTY stdout. A fake pty that never replies stalls about 5s.
- PowerShell has a built-in `ac` alias (Add-Content); macOS has `/usr/sbin/ac`. Tests never call a bare `ac`.
- Tests must never write the real history (`Env.FrecencyPath`, `AC_FRECENCY_FILE`). Windows ignores `HOME` for `os.UserHomeDir`.
- `Env.LaunchEnv` must be stubbed in app tests: this dev box is WSL with wt.exe on PATH, so real detection says "wt".
- Never run real wt.exe/osascript from tests; the e2e uses a fake `kitty`.
- Compare paths git prints with `os.SameFile`, not strings: git resolves symlinks (macOS `/private/var`) and Windows 8.3 names, and uses `/`.
- Git tests must isolate config: `GIT_CONFIG_GLOBAL=<temp file>`, `GIT_CONFIG_NOSYSTEM=1` (a dev box with `commit.gpgsign` breaks them otherwise).
- This agent sandbox refuses Bash commands that mention git alongside other complex shell (heredocs, `&&` chains with python); run git commands on their own.
- `-X` with a wrong symbol path is a silent no-op: `TestReleaseLdflags` (cmd/ac) builds with goreleaser's flags and checks the output.
- `install.ps1` must `throw`, never `exit`: under `irm | iex` exit closes the user's PowerShell.
- goreleaser `skip_upload`/`token` use `{{ index .Env "TAP_GITHUB_TOKEN" }}`: `.Env.X` errors when unset; `index` yields "".
- See `LESSONS.md` for CI history.
