# Changelog

## 3.1.0

Renamed the project from anycli to **anycli-launcher**. Breaking change —
see "Upgrading from anycli?" in the README.

### Changed
- The project, entry file (`anycli.bash` → `anycli-launcher.bash`), and
  config directory (`~/.config/anycli/` → `~/.config/anycli-launcher/`) are
  renamed. The `ac` command and its `_ac_`/`_AC_` internal identifier prefix
  are **unchanged**.

## 3.0.0

Renamed the project from kiro-launcher to **anycli**. Breaking change — see
"Upgrading from kiro-launcher?" in the README.

### Changed
- The project, entry file (`kiro-launcher.bash` → `anycli.bash`), config
  directory (`~/.config/kiro-launcher/` → `~/.config/anycli/`), and shell
  command (`lk` → `ac`) are all renamed. Every internal identifier prefix
  (`_lk_`/`_LK_` → `_ac_`/`_AC_`) follows the same rename.
- **anycli is now CLI-agnostic.** There is no privileged default AI CLI
  tool — `kiro-cli` is still a supported, auto-detected choice alongside
  `claude`, `aider`, `goose`, `codex`, and `amp`, but the launcher no longer
  silently assumes it. The first-run wizard now asks you to type a command
  name by hand when nothing recognized is found on PATH, instead of quietly
  defaulting to `kiro-cli`.
- Zsh compatibility: this file can now be sourced correctly from `~/.zshrc`,
  not just `~/.bashrc`/Git Bash. Several genuine bash/zsh differences (regex
  parsing, indirect expansion, array indexing, case conversion) are shimmed;
  `ac()` scopes `emulate -L bash` for its whole call tree so the rest of your
  interactive zsh session is never affected.
- Verified across Linux bash, zsh, macOS, and Windows Git Bash (including
  real GitHub Actions runners for macOS and Windows, not just local testing).

## 2.2.1

Patch release. Fixes three new-tab launch regressions.

### Fixed
- **New-tab launch crashed with `exec: alias ...: not found` (exit 127) when the
  CLI tool name is shadowed by a shell alias** (e.g. a security wrapper aliasing
  `kiro-cli` to `kiro-guard`). The new-tab path resolved the CLI with
  `command -v`, which returns the alias-definition text in an interactive shell;
  exec-ing that literal failed. Now resolves with `type -P`, which only yields an
  executable path, and falls back to the current tab when the tool is not on
  PATH.
- **Per-project appearance settings (color/title/prompt/alias/flags) appeared to
  vanish** for projects whose directory names contain hyphens or dots. The 2.x
  injective `_lk_safe_name` encoding no longer matched keys written by older
  versions' collapsing sanitizer, so those settings failed to resolve at launch.
  A one-time in-memory migration now remaps orphaned legacy keys onto the real
  directory names (gap-fill only, never overwriting a current value) and re-emits
  the corrected key on next write.
- **WSL new-tab launch failed with `Wsl/ERROR_PATH_NOT_FOUND`.** The WSL branch
  pinned `wt.exe --startingDirectory` to a `\\wsl.localhost\...` UNC path, which
  WSL cannot set as a working directory. Now passes the Linux path to
  `wsl.exe --cd` instead and drops the UNC `--startingDirectory`.

### Tests
- Added regression coverage for all three fixes: CLI resolution under a shadowing
  alias, legacy project-key appearance migration, and the WSL `--cd` launch argv.

## 2.2.0

Minor release. Adds a zero-config first-run experience so a brand-new user
lands in a working menu in under a minute.

### Added
- **First-run guided setup wizard.** The first time `lk` runs with no config
  file, it walks through three questions instead of erroring out:
  1. **Projects root** — auto-detects common roots (`~/projects`, `~/code`,
     `~/dev`, `~/repos`, `~/src`, `~/workspace`) that exist and contain at least
     one subdirectory, and offers them as numbered choices. `~/projects` is the
     recommended convention: it is listed first with a *(recommended)* tag when
     present, and on a fresh machine with no candidates a blank Enter accepts
     and creates `~/projects`. An *other* option accepts a typed path. On
     Windows Git Bash a native `C:\...` path is converted to Unix form via
     `cygpath` automatically.
  2. **AI CLI** — lists the supported CLIs found on PATH (via `_lk_detect_clis`)
     and lets the user pick; defaults to `kiro-cli` when none are detected.
  3. **Default flags** — optional, Enter to skip.

  It writes the config and renders the menu in the same invocation. The wizard
  only runs on an interactive terminal — when input is piped (scripts, CI) and
  no config exists, the launcher keeps its previous plain-error behavior, so
  scripted and non-interactive callers are unaffected.
- **`lk setup`** — re-run the guided setup any time, non-destructively. It
  revisits the three basics (projects root, AI CLI, default flags) with the
  current value pre-filled, so pressing Enter keeps each. Per-project settings
  (flags/color/title/prompt/alias) and preferences (theme, sort order, git and
  session toggles) are preserved untouched. Type `-` at the flags prompt to
  clear them. A project literally named `setup` takes precedence over the
  subcommand.

## 2.1.0

Minor release. Surfaces three capabilities that were already in the codebase
but had no user-facing switch, plus a new self-diagnosis command. No behavior
changes for existing configs — every new option is off/default until you opt in.

### Added
- **Sort order** config option (`sort_order: name | recent`). The launch-recency
  sort (`_lk_sort_projects_by_recent`) was already implemented but unreachable;
  it now has a config-menu toggle (`c` → `s`). With `recent`, the projects you
  launched most recently float to the top; projects never launched keep
  alphabetical order at the end. Default remains `name` (alphabetical).
- **Show git status** config option (`show_git_status: true | false`). Exposes
  the existing `_lk_git_summary` per-project git indicator (branch, a `*` for a
  dirty tree, `↑`/`↓` ahead/behind) via a config-menu toggle (`c` → `g`). Off by
  default; when on it shells out to git per project on render and degrades
  cleanly (no summary, no error) for non-git directories.
- **`lk doctor`** — a first-class health check. Reports launcher configuration
  validity (config file present, projects directory exists and is non-empty,
  configured CLI on PATH, resume/picker/session capability), then runs the
  existing `tests/check-platform.sh` platform/dependency checks and folds the
  result into a single verdict. Exit code is non-zero when a required check
  fails, so its output pastes cleanly into a bug report. A project literally
  named `doctor` still takes precedence over the subcommand.

## 2.0.0

Major release. Headline: **session continuity** — the launcher can now resume
prior AI conversations, pick a specific past session, and surface each project's
last session in the menu. Also includes cross-platform hardening (verified on
Windows Git Bash + WSL2), a shellcheck lint gate, and menu/UX polish. The
1.12.0–1.15.x entries below capture the incremental history.

### Added
- **Session resume** — continue a project's most recent AI conversation (`r<N>` in the menu, `lk resume <name>`, `lk <name> --resume`/`-r`). See 1.13.0.
- **Session-aware menu** — opt-in `show_sessions` shows each project's last session title + age (`⏵ "…" · 2h`). See 1.14.0.
- **Session picker** — pick a specific past session (`p<N>`, `lk pick <name>`, `--resume-picker`). See 1.15.0.
- **Platform check** — `tests/check-platform.sh` reports runtime dependency/capability status for Linux, WSL2, Git Bash, and macOS.
- **shellcheck lint gate** and a documented `.shellcheckrc` (see 1.12.0).

### Changed
- **Decluttered the menu footer.** The `r<N>` / `p<N>` session-trigger hints no longer take two lines under the project list — the footer now shows a single `?  help` key (only when the configured CLI supports resume/picker). Press `?` for a key-reference screen that lists the session triggers and a short explanation. Standard users get a clean menu; the triggers stay discoverable for those who want them.

### Fixed
- Per-project **aliases** display in the menu again (legacy hyphen/underscore config keys are reconciled, collision-safe). See 1.12.0.
- **New-tab launch** no longer leaks stray terminal bytes (`2c/3434`) into the prompt, and pins `--startingDirectory` so a stale Windows Terminal profile default (e.g. `C:\code`) cannot fail the launch with `0x8007010b`.
- **Install docs** use Unix-style paths (bash cannot use `C:\...` backslash paths), preventing broken `source` lines on Windows.

## 1.15.1

### Changed
- **Decluttered the menu footer.** The `r<N>` / `p<N>` session-trigger hints no longer take two lines under the project list — the footer now shows a single `?  help` key (only when the configured CLI supports resume/picker). Press `?` for a key-reference screen that lists the session triggers and a short explanation. Standard users get a clean menu; the triggers stay discoverable for those who want them.

## 1.15.0

### Added
- **Session picker.** Pick a *specific* past session to resume, not just the most recent one, when the CLI provides an interactive picker (kiro-cli via `--resume-picker`).
  - In the menu: type `p` before a project number (e.g. `p3`). The `p<N>` hint shows only when the configured CLI supports a picker.
  - From the shell: `lk pick <name>` or `lk <name> --resume-picker`.
  - Delegates selection to the CLI's own session UI rather than reimplementing it — no duplication of the CLI's session schema. Like resume, it drops the per-project initial prompt and starts fresh (with a note) on CLIs without picker support.
  - The per-CLI capability map (`_LK_CLI_CAPS`) gained a picker-flag field.
- **Platform check script** (`tests/check-platform.sh`). Reports pass/warn/fail for the launcher's runtime dependencies and capabilities — required commands, optional ones with fallbacks (`jq`, `timeout`), Windows Terminal helpers (`cygpath`/`wslpath`/`wt.exe`), ISO-timestamp date conversion, and the configured CLI's resume/picker support. Run it on Git Bash, WSL2, or macOS to confirm the session features will work before relying on them.

## 1.14.0

### Added
- **Session-aware menu.** With the new **Show last session** config option (`show_sessions`, off by default), each project row shows its most recent AI session — a marker, the session title (truncated), and a relative age, e.g. `myapp  ⏵ "fix the alias bug" · 2h`. Answers "what was I doing here?" at a glance. Pairs with the `r<N>` resume trigger from 1.13.0.
  - Opt-in and cross-platform: when off (the default), the launcher makes zero session lookups — no latency cost. When on, each project's sessions are fetched once per menu invocation and cached; lookups are timeout-guarded and fail quiet.
  - Uses `jq` when available for robust JSON parsing, with a best-effort `grep`/`sed` fallback when `jq` is absent (degrades to a title-less marker rather than breaking). Zero-message sessions are treated as "no session".
  - Only offered for CLIs that can list sessions (kiro-cli via `--list-sessions --format json`); the config option is hidden for CLIs without that capability.

## 1.13.0

### Added
- **Session resume.** Continue your most recent AI CLI conversation in a project instead of starting fresh, when the configured CLI supports it (kiro-cli via `--resume`).
  - In the menu: type `r` before a project number (e.g. `r3`). The `r<N>` hint shows in the footer only when the configured CLI supports resume.
  - From the shell: `lk resume <name>` or `lk <name> --resume` (also `-r`).
  - Resuming skips the per-project initial `prompt` so an ongoing session is not re-seeded. Works in both current-tab and new-tab launches. CLIs without resume support start fresh and say so — an unsupported flag is never emitted.
  - A per-CLI capability map (`_LK_CLI_CAPS`) centralizes each tool's resume flag; `claude` is included as `--continue` (to be verified against a local install).

## 1.12.0

### Added
- **Per-project appearance editor** (`lk` → `c` → `8`). Color, title, and prompt were documented but previously editable only by hand-editing the YAML. The new menu entry lists what's configured, lets you pick a project, and set or clear each field with validation (hex color must be `#rrggbb`).
- **shellcheck lint gate** in the test suite. `tests/run-tests.sh` now runs `shellcheck` against `kiro-launcher.bash` before the unit tests (skipped gracefully when shellcheck is not installed). A documented `.shellcheckrc` suppresses only intentional patterns and known false positives, so real new issues still fail the gate.

### Fixed
- Per-project **aliases** now display in the menu again. Configs written by an older version stored the project key in a sanitized form (hyphens turned into underscores, e.g. `public-knowledge-network` → `public_knowledge_network`), so the menu — which looks aliases up by the on-disk directory name — never matched and the alias silently disappeared. Alias resolution now reconciles this legacy key shape (case-insensitive exact match, plus the precise one-directional hyphen→underscore legacy form), so existing configs work without re-entry. The match is collision-safe: a real `my_app` directory will not inherit an alias set for a distinct `my-app`.
- New-tab launches no longer leak a stray control-sequence artifact (e.g. `2c/3434`) into the CLI prompt. The generated tab script now drains pending terminal-reply bytes from the `wt.exe` tab handshake in a loop (raw + no-echo TTY, a short settle window, then read-until-empty) before `exec`, instead of a single fixed-timeout read that missed late-arriving bytes. An `INT`/`TERM` trap restores the terminal even if the drain is interrupted, and the TTY state is always restored before the CLI starts.
- CLI help parsing now collects short options. A line like `-a, --trust-all-tools` still contributes the long form, but a short-only option such as `-v` is no longer dropped.

### Changed
- The three config editors (flags, aliases, appearance) share one project picker (`_lk_config_pick_project`), removing duplicated selection logic.

## 1.11.0

### Security
- **Fix**: Command injection in the new-tab launch path. The project directory, tab title, and prompt were interpolated into single-quoted shell strings without escaping, so a directory name containing a quote executed arbitrary commands. All values are now shell-quoted with `printf %q`.
- **Fix**: Tab launch scripts are created with `mktemp` (mode 700) instead of a predictable `$$`-based name, and stale scripts from failed launches are swept.
- **Fix**: Tracked-app version definitions are no longer shell snippets run through `eval`. They are declarative (`version_file`, `version_key`, `version_pattern`), so the config file cannot execute code.

### Fixed
- Project names no longer collide. `my-app`, `my_app`, and `my.app` mapped to the same internal key, silently sharing flags; a config rewrite then deleted all but one of them.
- Config files are written as valid YAML. Titles and prompts containing `#`, `: `, quotes, or backslashes now round-trip instead of corrupting the file.
- Windows Terminal `settings.json` is parsed as JSONC (it ships with `//` comments, which previously made every theme apply fail), written atomically, and backed up to `settings.json.lk-backup` before the first change.
- Resetting a theme restores each profile's previous `colorScheme` instead of deleting the field, so manual customisation survives.
- `_lk_load_config` returns 0. It returned 1 whenever `LK_PROJECTS_DIR` was unset, breaking `&&` chaining.
- The launcher self-update re-sources itself, so the running shell gets the new code rather than showing the new version while executing the old functions. It also refuses to pull over a dirty tree or a non-`main` branch.
- The kiro-cli version in the header is refreshed after an update instead of staying cached for the shell's lifetime.
- The changelog viewer renders each change type with its colour; the type was parsed and discarded.
- `_lk_find_python` no longer errors under `set -u` when `LOCALAPPDATA`/`APPDATA` are unset.

### Changed
- Update checks run in the background. The menu renders from cache, so it never waits on the network.
- Tracked apps are user-configured under `tracked_apps:`. The list was previously hardcoded to three of the author's own repositories.
- The menu footer advertises the `v` (changelog) key, which was implemented but never shown.
- An idle timeout now prints a message instead of exiting silently.

### Added
- `LICENSE` file (MIT), which the README already claimed.

## 1.10.1
- Version bump.

## 1.10.0
- **Feature**: Per-project alias for the menu display name, with interactive editing.
- **Fix**: Header border alignment.

## 1.9.4
- **Feature**: Show the CLI tool version in the menu header.
- **Change**: kiro-launcher itself is no longer excluded from the menu.

## 1.9.3
- **Feature**: Progress spinners (`_lk_spin`, `_lk_spin_verbose`) for update operations.

## 1.9.2

- **Fix**: Python detection now verifies `python3 --version` actually succeeds (Windows Store stub passes `command -v` but fails at runtime)
- **Fix**: Theme error display no longer shows literal `\e[31m` escape codes

## 1.9.1

- **Fix**: Remove unsupported `-y` flag from `kiro-cli update` command
- **Fix**: Add Python detection for theme functionality — falls back from `python3` to `python`, rejects Windows Store stub, shows clear error message
- **Feature**: Add "Default" theme option to restore Windows Terminal's built-in color scheme
- **Tests**: Add regression tests for update flag, python detection, and default theme entry

## 1.6.2

- **Tests**: Add 38 new test cases (YAML parser corners, flags without defaults, safe name conversion, write_config edge cases, CLI launch subcommands, prompt argument passing, config state clearing)

## 1.6.1

- **Cleanup**: Remove dead `_lk_apply_tab_style` stub
- **Tests**: Add 20 new tests (self-exclusion, prompt, tilde expansion, CLI detection, changelog key, new-tab fallback, menu loop)

## 1.6.0

- **Self-exclude from menu**: Launcher auto-hides its own directory from the project list when cloned inside the projects root

## 1.5.0

- **Keep menu open after launch**: Menu stays open after launching a project in a new tab, allowing quick multi-project launches

## 0.5.0

- **Remove sessions**: Removed multi-tab session feature (too complex, unreliable on WSL)
- **Per-project color/title**: Set `color` and `title` per project in config — tab updates on launch
- **Simpler config**: projects section now supports `flags`, `color`, and `title`

## 0.4.0

- **Sessions**: Multi-tab session profiles with auto-send keystrokes
- **Flag inheritance**: default → project (additive, use `none` to suppress)
- **CLI detection**: Auto-detects available AI CLI tools on PATH
- **Config menu**: Interactive configuration editor
- **Per-project flags**: Override default flags per project
