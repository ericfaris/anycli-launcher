# Design — Session Resume & Session-Aware Menu

## Overview

Two capabilities, one shared foundation (a per-CLI capability map):

1. **Resume launch** — thread a `resume` mode through the launch path so the CLI's resume flag is inserted after the launch subcommand.
2. **Session-aware menu** — behind a toggle, fetch and cache per-project session summaries from `kiro-cli chat --list-sessions --format json` and render a marker + last-title + age.

## Component 1: Capability Map

New declarative array near the existing `_LK_KNOWN_CLIS`:

```bash
# tool : resume-flag : list-sessions-argv (pipe-separated tokens; empty = unsupported)
_LK_CLI_CAPS=(
  "kiro-cli:--resume:chat|--list-sessions|--format|json"
  "claude:--continue:"          # TO-VERIFY: run `claude --help`; list cmd unknown
  "aider::"
  "goose:--resume:"
  "codex::"
  "amp::"
)
```

Accessors:
- `_lk_cli_resume_flag <tool>` → prints the resume flag or empty.
- `_lk_cli_list_argv <tool>` → prints the list-sessions argv (space-joined) or empty.

Empty resume flag → resume is unsupported: the menu does not advertise it, and a resume request degrades to a normal launch with an informational note (AC-1.3, AC-4.1).

## Component 2: Resume Launch (argv threading)

The launch path already assembles argv in `_lk_run_cli` (current tab) and `_lk_launch_project` → generated script (new tab). Add a `resume` boolean parameter threaded from the caller.

**`_lk_run_cli`** (current signature `flags prompt`) → add a trailing `resume` arg:
```
_lk_run_cli "$flags" "$prompt" "$resume"
```
Assembly order becomes: `TOOL [launch-subcmd] [resume-flag?] [flags] [prompt?]`
- resume flag inserted immediately after the launch subcommand.
- when `resume=1`: prepend `$(_lk_cli_resume_flag "$TOOL")` if non-empty; **omit the prompt** (AC-1.4). If the resume flag is empty, print an informational line ("<tool> has no resume support — starting fresh") and proceed without it (AC-1.3).

**`_lk_launch_project`** → accept `resume` and pass it into both branches:
- current-tab branch: `_lk_run_cli "$flags" "$prompt" "$resume"`.
- new-tab branch: include the resume flag in `launch_argv` (after the subcommand) and drop the prompt when resuming, so the generated script carries it (AC-1.5).

**Menu trigger** (AC-1.1/1.2): extend the main-menu input handler. Current input is a bare number. Add: an input matching `^[rR][0-9]+$` splits into resume-mode + index; launch that project with `resume=1`. Legend gains `r<N>  resume`.

**CLI path** (US-2): in the `lk()` argument dispatch:
- `lk resume <name>`: if a project named `resume` exists, it wins (AC-2.3, mirrors `help`/`config`); else resolve `<name>` and launch with `resume=1`.
- `lk <name> --resume|-r`: strip the flag, resolve `<name>`, launch with `resume=1`.

## Component 3: Session-Aware Menu

### Config toggle
Add `show_sessions` (default `false`) alongside `show_git_status`:
- parsed in `_lk_parse_yaml` top-level scalars,
- written by `_lk_write_config`,
- a config-menu toggle entry.

### Fetch + cache
`kiro-cli chat --list-sessions --format json` returns **all** sessions for the *current* directory — it is per-cwd, so the launcher must call it with each project's dir as cwd. To bound cost:

- Cache in memory for the lifetime of one `lk` invocation: an associative array `_LK_SESSION_CACHE[<safe-dirname>]="<title>\t<epoch>"` (most-recent session only — that's all the menu shows).
- Populate lazily: on first menu render with `show_sessions=true`, iterate visible projects once, calling the list command with `cd` into each (subshell, timeout-guarded), parse the newest session, store. Subsequent re-renders (filter/redraw) reuse the cache (AC-3.3).
- Timeout: `timeout 3 (cd "$dir" && <list-argv>)` per project; on non-zero/empty, cache an empty marker so it isn't retried (AC-3.5).
- Only runs when `show_sessions=true` (AC-3.2).

### Parsing without jq
The JSON is single-line. Prefer `jq` when present; otherwise a constrained fallback: the sessions are ordered newest-first in observed output, but do NOT rely on order — extract the object with the max `updatedAt`. Fallback parse strategy:
- If `jq` available: `jq -r '.[0].sessions | sort_by(.updatedAt) | last | "\(.title)\t\(.updatedAt)"'`.
- Else: `grep -oE` the `sessionId`/`title`/`updatedAt` triples, pick max `updatedAt` in bash. Title may contain quotes/commas — extract with a tolerant `sed` capturing between `"title":"` and the next unescaped `","`. If parsing is ambiguous, degrade to a plain "⏵ session" marker without the title (still satisfies AC-3.1's marker; title is best-effort).

### Render
In the project row printer, when `show_sessions=true` and the cache has a non-empty entry:
```
  3  myapp   ⏵ "fix the alias bug" · 2h ago
```
- Title truncated to a fixed budget (e.g. 32 chars, ellipsis).
- Age: relative from `updatedAt` epoch vs now (reuse/extend a relative-time helper; add one if absent).
- No entry / empty → no marker (AC-3.4).

## Data / State

| State | Location | Lifetime |
|-------|----------|----------|
| `show_sessions` | config.yaml | persistent |
| `_LK_CLI_CAPS` | source constant | static |
| `_LK_SESSION_CACHE` | in-memory assoc array | one `lk` invocation |

## Error Handling

- kiro-cli not runnable / list command fails / times out → treat as "no sessions"; never error the menu.
- Unsupported CLI resume → informational note, fresh launch.
- Malformed JSON → marker without title; log nothing to stdout that corrupts the menu.

## Testing Strategy

Mock `kiro-cli` (the suite already mocks it) to:
- emit a known `--list-sessions --format json` payload for cache/parse/render tests,
- record argv so resume-flag insertion and prompt-suppression are assertable,
- emit no sessions for the no-marker case.

Key tests:
1. `_lk_cli_resume_flag`: kiro-cli→`--resume`; unknown→empty.
2. argv assembly: resume inserts flag after subcommand; prompt omitted when resuming.
3. `r3` menu input → resume launch of project 3.
4. `lk resume <name>` and `lk <name> --resume` → resume launch; project named `resume` wins.
5. Unsupported CLI + resume → no flag emitted, informational note.
6. new-tab generated script contains the resume flag when resuming (extend the existing generated-script assertions).
7. session cache/parse: mocked JSON → correct newest title+age; malformed → marker-only; empty → no marker.
8. `show_sessions=false` → zero list-command invocations (assert the mock's call log is empty).
9. config round-trip for `show_sessions`.
10. shellcheck clean.

## Design Review Notes (self-flagged)

- **Shared launch path** is touched (argv assembly) → design-review-worthy. The resume flag must land in the same position for both tabs; tests assert both.
- **Config contract**: `show_sessions` must be parsed, written, and defaulted consistently (three sites) — mirror `show_git_status` exactly to avoid a phantom/missing key.
- **jq assumption**: the constraint says no guaranteed `jq`; the fallback parse is the risk area. Keep the title best-effort so a parse miss never breaks the marker.
