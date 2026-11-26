# anycli-launcher

Keep your projects in one folder and use an AI CLI (Claude Code, Codex, Kiro,
aider, goose, amp) as your pair programmer? Run `ac`, press a number, and you
are in that project with your AI CLI running.

v4 is a single Go binary plus a few lines of shell integration. It works the
same in bash, zsh, fish and PowerShell on Linux, macOS and Windows. No bash 4,
no GNU/BSD tool differences. (v3, the bash version, lives on the `v3` branch.)

```
  ╭────────────────────────────────────╮
  │  anycli-launcher           v4.0.0  │
  │  claude                            │
  ╰────────────────────────────────────╯

 ▸  1  ● terminal
    2  ● projects (root)
    3  ● .claude (config)
    4  alpha  main* ↑2
    5  ML
    6  work/api

    c config   n new   x<N> remove   ? help   q quit

  ›
```

## Install

> Release builds (Homebrew, scoop, install script) come in a later release.
> Until then, build from source with Go 1.26+:

```sh
go install github.com/ericfaris/anycli-launcher/cmd/ac@latest
```

Then add the shell integration. It defines an `ac` function so the binary can
change your shell's directory:

| Shell | Add to | Line |
|---|---|---|
| bash | `~/.bashrc` | `eval "$(ac init bash)"` |
| zsh | `~/.zshrc` | `eval "$(ac init zsh)"` |
| fish | `~/.config/fish/config.fish` | `ac init fish \| source` |
| PowerShell | `$PROFILE` | `Invoke-Expression (& (Get-Command ac -CommandType Application | Select-Object -First 1).Source init pwsh \| Out-String)` |

How it works: the `ac` function runs the binary with `--cd-file <tmp>`. When
you pick a project, the binary writes its path to that file, runs your CLI
there, and the function `cd`s your shell to it when the CLI exits. Without the
integration, `ac` still launches the CLI in the right folder, but your shell
stays where it was.

`ac doctor` tells you whether the integration is active.

## First run

Run `ac`. With no config yet, three questions set you up:

1. **Where do your projects live?** It offers common roots that exist and have
   subfolders (`~/projects`, `~/code`, `~/dev`, `~/repos`, `~/src`,
   `~/workspace`). `~/projects` is recommended. On a fresh machine, Enter
   creates `~/projects`.
2. **Which AI CLI?** It lists the supported CLIs it finds on your PATH, or lets
   you type any command.
3. **Default flags?** Optional flags for every launch.

Then the menu opens. Run `ac setup` any time to revisit these with your
current values pre-filled. Enter keeps a value, and `-` clears the flags.
Per-project settings are never touched. With input piped (scripts, CI), the
wizard does not run.

**Coming from v3?** On first run, `ac` imports `~/.config/anycli-launcher/config.yaml`
(or the older flat `config`) into `config.toml` and leaves the v3 file in place.
You can also run `ac migrate [file] [--force]` yourself. It prints what it
dropped: tracked apps, show_sessions, and Windows Terminal theme names.

## Usage

| Command | What it does |
|---|---|
| `ac` | Open the menu |
| `ac <name>` | Launch in the matching project (see [Name matching](#name-matching)) |
| `ac <name> -r` / `--resume`, `ac resume <name>` | Resume the most recent session |
| `ac <name> --resume-picker`, `ac pick <name>` | Open the CLI's own session picker |
| `ac list` | List projects (`name` or `name<TAB>label`) |
| `ac new [name]` | Create a folder in the root, `git init` it, optionally set alias/color/title/prompt, then cd there |
| `ac remove <name>` | Delete a project folder (see below). Needs the exact name or label. |
| `ac config` | Open `config.toml` in `$VISUAL` / `$EDITOR` |
| `ac setup` | Re-run guided setup |
| `ac doctor` | Health check. Exits non-zero when a required check fails. |
| `ac migrate` | Import a v3 config |
| `ac init <shell>` | Print the shell integration |

A project named `help`, `version`, `list`, `config`, `doctor`, `setup`, `resume`
or `pick` wins over the subcommand, as in v3. `init`, `new`, `remove` and
`migrate` are always subcommands.

### Name matching

`ac <name>` tries, in order: exact, case-insensitive exact, prefix, substring,
then fuzzy (the letters in order, so `ac acl` finds `anycli-launcher`). The
first tier with a match wins. When that tier has several matches, the one
you launch most (frecency, below) wins. If they tie, `ac` lists them and
exits 1. Exact and case-insensitive exact matches always win outright, so
history never hijacks a real name. `ac remove` still needs an exact name.

### Frecency

Every launch is recorded in
`~/.local/state/anycli-launcher/frecency.json` (or
`$XDG_STATE_HOME/anycli-launcher/`; `$AC_FRECENCY_FILE` overrides the file).
Scoring is zoxide's: each launch adds 1 to a folder's rank, weighted x4 if the
last launch was within the hour, x2 within the day, x0.5 within the week,
x0.25 after that. When the ranks add up past 1000, they are all scaled down
and the faded ones dropped, so the file stays small (at most 500 folders). A
missing or corrupt file is treated as empty and never blocks a launch;
`ac doctor` reports it. Delete the file to reset your history.

Frecency ranks `ac <partial>` and the menu filter. With
`sort_order = "recent"` (or `"frecent"`), it sorts the menu too, with ties in
name order. `ac list` is always in name order.

### Menu keys

| Key | Action |
|---|---|
| `1` | cd to the projects root (no CLI) |
| `2` | cd to the root and launch the CLI |
| `3` (when it exists) | the CLI's config folder (`~/.claude`, `~/.kiro`, ...): cd + launch |
| `N` | open entry N. Selects as soon as no longer number could match (`4` in a 9-entry menu); otherwise press Enter (`1` then Enter in a 12-entry menu). |
| `r<N>` / `p<N>` | resume / pick a session for project N |
| `x<N>` | remove project N |
| ↑ ↓ / `j` `k`, Enter | move and open |
| `n` | new project |
| `c` | edit config |
| `/` | filter by name |
| any other letter | start the filter with that letter |
| `?` | key help |
| `q`, Esc, Ctrl-C | quit |

### Filtering

Type part of a name to filter the menu. Matching is fuzzy and ignores case
(`mlp` finds `ml-project`). The matched letters are highlighted, and results
are ranked by match quality, then frecency. Enter opens the top match, ↑ ↓
(or Tab, Ctrl-N/P) move, Backspace edits, and Esc clears the filter.

To keep the number keys and single-letter commands working, a letter starts
the filter only when it isn't a menu key at an empty prompt:

- `n`, `c`, `q` and `?` keep their meaning, and `j`/`k` still move the cursor.
- `r`, `p` and `x` still wait for a number (`r4`). If a letter follows
  instead, both letters become the filter (`ru` filters for `rust`).
- To filter for a name that starts with `n`, `c`, `q`, `j` or `k`, press `/`
  first (`/kiro`).
- Once the filter is open, every key you type is text, digits and command
  letters included, until Esc or until you backspace it empty.

### Removing a project

`ac remove <name>` or `x<N>` in the menu:

- Refuses anything that is not a folder strictly inside a configured root.
  That includes `/`, your home folder, a root itself, and a folder that contains
  a root. A project that is a symlink is unlinked; its target is never deleted.
- Asks before deleting. The default is **no**. If the repo has uncommitted
  changes or commits on no remote, you must type the project name to confirm.
- If `origin` is on GitHub, it offers to delete that repo too, via `gh`. If you
  say yes, `gh` must be installed and authenticated **before** anything is
  deleted; otherwise the whole removal is cancelled. Non-GitHub remotes are
  left alone.
- Drops the project's entry from `config.toml`.

## Configuration

`~/.config/anycli-launcher/config.toml` (or `$XDG_CONFIG_HOME/anycli-launcher/`;
`$AC_CONFIG` overrides the path, `$AC_PROJECTS_DIR` overrides `projects_dir`).

```toml
projects_dir = "~/projects"
extra_dirs = ["~/work"]        # more roots, listed after projects_dir
scan_depth = 1                 # 2: non-git folders with subfolders are groups (work/api)
cli = "claude"
cli_launch = ""                # subcommand override, e.g. "chat" for kiro-cli
cli_config_dir = ""            # override the CLI's config folder shortcut
default_flags = "--model opus"
show_git_status = true         # branch, * when dirty, ↑ahead ↓behind
theme = "default"              # default | mono | dusk
sort_order = "name"            # name | recent (frecency, alias "frecent")

[projects.my-app]
alias = "App"                  # menu label
flags = "--agent reviewer"     # added to default_flags; "none" = no flags at all
title = "My App"               # terminal title (set via OSC 0)
color = "#00cc66"              # tab color (used by tab backends, coming later)
prompt = "pick up where we left off"
cli = "codex"                  # use a different CLI for this project
```

Flags are split like a shell would split them (quotes and backslashes), but
nothing is evaluated. `flags = ""` means "defaults only".

### Per-project `.ac.toml`

A project can carry its own `.ac.toml` with the same keys as a
`[projects.<name>]` table. Your `config.toml` entry overrides it field by
field. Because it usually comes from a cloned repo, `cli` there must be a plain
command name, not a path.

### CLI profiles

| CLI | Resume | Picker | Prompt | Verified |
|---|---|---|---|---|
| claude | `--continue` | `--resume` | positional | `claude --help` (2.1.332) |
| kiro-cli | `chat --resume` | `chat --resume-picker` | positional | from v3 |
| codex | `resume --last` | `resume` | positional | not yet |
| goose | `session --resume` | none | none | not yet |
| aider | none | none | none (its positional args are files) | not yet |
| amp | none | none | none | not yet |

Any other command works too. It gets the prompt as a positional argument and
no resume support. `ac doctor` warns when a profile is unverified.

## Development

```sh
go test ./...                            # unit + golden + shim e2e (skips missing shells)
go test ./internal/tui -update           # regenerate TUI golden files
AC_REQUIRE_SHELLS=bash,zsh go test -run TestShimE2E ./cmd/ac
```

No local Go? Use Docker:
`docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.27.4 go test ./...`

CI (`.github/workflows/go.yml`) runs `go test` on Linux, macOS and Windows, and
the shim smoke test in bash and zsh (Linux, plus macOS's bash 3.2) and pwsh
(Windows). The plan for later releases (new-tab and tmux
backends, worktree agents, packaging) is in `docs/V4-PLAN.md`.

Tests never touch your real history: the app tests pass
`Env.FrecencyPath`, and the shim e2e test sets `AC_FRECENCY_FILE`.

## License

MIT
