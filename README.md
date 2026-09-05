# anycli

If you keep all your coding projects in one folder and use AI CLI tools as your pair programmer, this tool saves you a few steps every time you sit down to work. Instead of manually cd-ing into a project and typing your CLI command, you just run `ac` and type a number — the launcher drops you into the right directory and opens your AI assistant automatically.

**Technically:** a shell function that presents a styled, numbered menu of your projects directory and launches your configured AI CLI in the selected one. Supports per-project tab colors, titles, and initial prompts.

**Supported platforms:** Linux, macOS, Windows (WSL2, Git Bash) — anywhere bash runs.

## Screenshot

```
  ╭─────────────────────────────────────╮
  │  anycli                     v3.0.0  │
  ╰─────────────────────────────────────╯

   1  ● terminal
   2  ● projects (root)
   3  my-app
   4  dotfiles
   5  scripts

   c  config    q  quit

  ›
```

## Requirements

### 1. Shell

bash 4+ (source from `~/.bashrc` or `~/.zshrc`)

| Platform | Source from |
|----------|-------------|
| Linux / macOS / WSL2 (bash) | `~/.bashrc` |
| Linux / macOS / WSL2 (zsh) | `~/.zshrc` |
| Windows (Git Bash) | `~/.bashrc` |

### 2. AI CLI Tool

At least one AI CLI tool on your PATH. anycli is CLI-agnostic — there's no
privileged default; pick whichever tool you use. These are auto-detected on
the first-run wizard, and you can type any other command name manually:

- `claude`
- `kiro-cli`
- `aider`
- `goose`
- `codex`
- `amp`

Configure which tool to use via `ac` → `c` → `4`.

### 3. A projects root directory

The launcher scans one level deep inside a root folder and lists each subdirectory as a menu option.

## Installation

Pick the shell you actually use. **Paths in `source`/`cd` must be Unix-style**
even on Windows — bash cannot use `C:\...` backslash paths. On Git Bash,
`C:\Users\you` is written `/c/Users/you` (and `~` already points there).

1. Clone the repo into your projects folder:

```bash
# WSL2 / Linux / macOS
git clone https://github.com/ericfaris/anycli.git ~/projects/anycli

# Windows Git Bash (~ is your Windows home, e.g. C:\Users\you)
git clone https://github.com/ericfaris/anycli.git ~/projects/anycli
```

2. Source it from your shell config (use the entry file `anycli.bash`):

```bash
# ~/.bashrc (bash on WSL2 / Linux / Git Bash) or ~/.zshrc (zsh)
source ~/projects/anycli/anycli.bash
```

   If you cloned somewhere else, use that path in Unix form — e.g. a repo at
   `C:\code\anycli` is sourced as `source /c/code/anycli/anycli.bash`.

3. Reload your shell (`source ~/.bashrc`) or open a new terminal, then run `ac`.

> **WSL2 and Git Bash are separate environments** with separate configs
> (`~/.config/anycli/config.yaml` in each home). If you use both, install
> and configure the launcher in each — clone into each environment's filesystem
> rather than sourcing a WSL clone from Git Bash (the `//wsl.localhost/...` path
> is slow and unreliable).

> **Upgrading from kiro-launcher?** This project was renamed from
> kiro-launcher to anycli, and the `lk` command is now `ac`. Re-clone under
> the new name, update your shell config's `source .../anycli.bash` line, and
> move `~/.config/kiro-launcher/` to `~/.config/anycli/` (or just re-run
> `ac setup`).

## First run

The very first time you run `ac` with no config file yet, a short guided setup
walks you through three questions and drops you straight into your menu — no
manual config editing required:

1. **Where do your projects live?** — the wizard auto-detects common roots
   (`~/projects`, `~/code`, `~/dev`, `~/repos`, `~/src`, `~/workspace`) that
   exist and contain at least one subdirectory, and offers them as numbered
   choices. `~/projects` is the recommended convention and is listed first when
   present. Pick one, or choose *other* to type a path. On a fresh machine with
   no candidates, pressing Enter accepts and creates `~/projects` for you.
   (On Windows Git Bash you can paste a native `C:\...` path — it is converted
   to the Unix form automatically.)
2. **Which AI CLI should I launch?** — the wizard lists the supported CLIs it
   finds on your PATH and lets you pick. If none are found, it asks you to
   type the command to run.
3. **Default flags?** — optional flags passed to every launch (e.g.
   `--trust-all-tools`). Press Enter to skip.

It writes `~/.config/anycli/config.yaml` and opens the menu in the same
invocation. You can change any of these later via `ac` → `c`.

You can re-run the guided setup any time with `ac setup`. It revisits the three
basics with your current values pre-filled — press Enter to keep each — and
leaves your per-project settings and preferences (theme, sort order, git/session
toggles) untouched. Type `-` at the flags prompt to clear default flags.

The wizard only runs on the first `ac` interactively. If `ac` is invoked with
input piped in (scripts, CI) and no config exists, it falls back to a plain
error instead of prompting.

## Updating

The launcher checks for updates automatically (once per 24 hours) and shows an indicator in the menu header when a new version is available. Press `u` to update from the menu.

To update manually on Windows (Git Bash or WSL2):

```bash
cd ~/projects/anycli
git pull
```

Then open a new terminal (or run `source ~/.bashrc`) and run `ac`. Verify the header shows `v3.0.0`.

## Configuration

Configuration is stored in YAML at:

```
~/.config/anycli/config.yaml
```

You can edit this file directly or use the built-in config menu (`ac` → `c`).

### Example config

```yaml
projects_dir: ~/projects
default_flags: --trust-all-tools
cli_tool: claude
cli_launch: ""
sort_order: name          # name (alphabetical, default) or recent
show_git_status: false    # show per-project git branch/dirty/ahead-behind

projects:
  my-app:
    flags: --no-interactive
    color: "#00cc66"
    title: My App
  dotfiles:
    flags: --agent mechanic
    title: Dotfiles
  ml-project:
    flags: --model=claude --effort=high
    color: "#00cc66"
    title: ML Project
    prompt: help me with ML
    alias: ML
  isolated:
    flags: none
```

### Flag inheritance

Flags are **additive** — children inherit from parents automatically:

```
default_flags          →  applied to ALL projects
  + project flags      →  added on top of defaults
```

**Example:** With `default_flags: --trust-all-tools` and `my-app` having `flags: --no-interactive`:

```
Resolved for my-app: --trust-all-tools --no-interactive
```

You don't need to repeat `--trust-all-tools` in every project — it cascades.

**To suppress inheritance**, use `flags: none`:

```yaml
projects:
  isolated:
    flags: none    # gets NO flags, not even defaults
```

### Per-project color, title, prompt, and alias

| Key | Description |
|-----|-------------|
| `alias` | Display name shown in the menu instead of the directory name |
| `color` | Hex color (e.g., `"#00cc66"`) applied to the terminal tab |
| `title` | Text shown in the terminal tab title bar |
| `prompt` | Initial prompt passed to the AI CLI on launch |

When `color` or `title` is set, the project opens in a **new terminal tab** (via Windows Terminal's `wt.exe`) with those styles applied. The menu stays open after launching, allowing quick multi-project launches.

These work in Windows Terminal, iTerm2, and most modern terminals that support OSC escape sequences.

### Config menu

Press `c` from the main menu to configure:

1. **Projects directory** — root folder to scan
2. **Default flags** — flags passed to every AI CLI invocation
3. **Per-project flags** — override flags for specific projects
4. **CLI tool** — which AI CLI to use (auto-detects available tools)
9. **Show last session** — when your CLI can list saved sessions (kiro-cli), display each project's most recent session title and age in the menu (e.g. `⏵ "fix the alias bug" · 2h`). Off by default; when off, the launcher makes no session lookups. Uses `jq` when available and falls back to a best-effort parser otherwise.
- **s — Sort order** — how the project list is ordered: `name` (alphabetical, the default) or `recent` (most-recently-launched projects float to the top). Recency uses each project's last-launch time; projects never launched keep alphabetical order at the end.
- **g — Show git status** — show a per-project git summary on each menu row (current branch, a `*` when the tree is dirty, `↑`/`↓` ahead/behind counts). Off by default. When on it shells out to git per project on every render; non-git directories render cleanly with no summary and no error.

## Usage

| Key | Action |
|-----|--------|
| 1 | cd to the root folder (no AI CLI) |
| 2 | cd to the root folder and launch AI CLI |
| 3+ | cd to that project and launch AI CLI |
| r&lt;N&gt; | Resume project N's most recent AI session (when the CLI supports it) |
| p&lt;N&gt; | Pick a session to resume for project N (interactive; when the CLI supports it) |
| c | Open configuration menu |
| u | Update kiro-cli (if that's your configured CLI) and/or the launcher itself (shown when updates available) |
| v | Show changelog |
| ? | Show key help (lists the r&lt;N&gt;/p&lt;N&gt; session triggers when supported) |
| q | Quit |

When a project launches in a new tab (has `color` or `title` set), the menu stays open for additional launches.

### Resuming a session

If your AI CLI can resume a prior conversation (kiro-cli via `--resume`), the launcher can pick up where you left off instead of starting fresh:

- **In the menu**: type `r` before a project number — e.g. `r3` resumes project 3's most recent session. Press `?` in the menu for a reminder of the session keys (shown only when your CLI supports resume).
- **From the shell**: `ac resume <name>` or `ac <name> --resume` (also `-r`).

Resuming continues the CLI's most recent conversation in that project directory and skips the per-project initial `prompt` (so an ongoing session isn't re-seeded). If the configured CLI has no resume support, the launcher starts a fresh session and says so.

### Picking a session

To choose a *specific* past session rather than the most recent one, use the picker (kiro-cli via `--resume-picker`), which delegates to the CLI's own interactive session selector:

- **In the menu**: type `p` before a project number — e.g. `p3`. The `p<N>` hint appears in the `?` key help when your CLI supports a session picker.
- **From the shell**: `ac pick <name>` or `ac <name> --resume-picker`.

## Testing

```bash
./tests/run-tests.sh
```

### Verifying your platform

To confirm the launcher's runtime dependencies and capabilities on a specific machine (handy on Windows Git Bash, WSL2, or macOS before relying on the session features), run:

```bash
./tests/check-platform.sh
```

It reports pass/warn/fail for required commands, optional commands with graceful fallbacks (`jq`, `timeout`), the new-tab `wt.exe` helpers, ISO-timestamp date conversion, and your configured AI CLI's resume/picker support. Exit code is 0 when all *required* checks pass (warnings are allowed).

For a health check that also inspects your **launcher configuration** (config file present, projects directory exists and is non-empty, configured CLI on PATH, resume/picker capability), run the built-in command:

```bash
ac doctor
```

`ac doctor` prints the launcher-specific checks, then runs the platform check above and folds its result into a single verdict. Exit code is non-zero when a required check fails — paste its output into a bug report to skip the back-and-forth.

## License

MIT
