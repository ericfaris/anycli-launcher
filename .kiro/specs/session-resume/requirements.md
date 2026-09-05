# Requirements — Session Resume & Session-Aware Menu

**Feature**: Let users resume a prior AI CLI conversation in a project ("continue where I left off") and see, at a glance, which projects have live sessions and what the last one was about.

**Primary CLI**: kiro-cli (verified capabilities). claude: to-verify placeholder.

## Problem

The launcher gets a user *into* a project but the AI agent starts with no memory of prior work. The user re-explains context every time. kiro-cli already persists per-directory chat sessions and exposes them via `chat --resume`, `--resume-id`, `--resume-picker`, and `--list-sessions --format json`. The launcher does not use any of this. This is the highest-frequency daily friction in the core launch loop.

## Verified kiro-cli capabilities (2026-09-04, kiro-cli chat --help)

| Capability | Flag |
|-----------|------|
| Resume most recent conversation in cwd | `-r, --resume` |
| Resume a specific session | `--resume-id <SESSION_ID>` |
| Interactive session picker | `--resume-picker` |
| List sessions for cwd as JSON | `--list-sessions --format json` |
| Delete a session | `-d, --delete-session <SESSION_ID>` |

`--list-sessions --format json` output shape (verified):
```json
[{"cwd":"/abs/path","sessions":[
  {"sessionId":"uuid","source":"v2","title":"first prompt text","updatedAt":"ISO8601","messageCount":368}
], "complete":true}]
```

## User Stories

### US-1 — Resume launch (interactive)
As a user, I select a project in the menu with a resume modifier so the AI CLI continues my most recent conversation there instead of starting fresh.
- **AC-1.1**: In the menu, a resume trigger (`r` followed by a project number, e.g. `r3`) launches project 3 with the CLI's resume flag.
- **AC-1.2**: The menu legend shows the resume trigger.
- **AC-1.3**: When the configured CLI has no resume capability, the resume trigger is not advertised and, if attempted, launches fresh with an informational note (never emits an unsupported flag).
- **AC-1.4**: Resume mode suppresses the per-project configured `prompt` (an ongoing session must not be re-seeded).
- **AC-1.5**: Resume works in both the current-tab launch and the `wt.exe` new-tab launch.

### US-2 — Resume launch (non-interactive CLI)
As a power user, I resume from the shell without the menu.
- **AC-2.1**: `lk resume <name>` resolves the project (same fuzzy resolver as `lk <name>`) and launches it with the resume flag.
- **AC-2.2**: `lk <name> --resume` (or `-r`) behaves identically.
- **AC-2.3**: A project literally named `resume` still launches (name takes precedence over the subcommand, matching existing `help`/`config` handling).

### US-3 — Session-aware menu (glanceable state)
As a user, I see which projects have resumable sessions and what the last one was about.
- **AC-3.1**: Behind a config toggle `show_sessions` (default `false`), each project row shows a marker when it has ≥1 session, plus the most-recent session's title (truncated) and relative age (e.g. `⏵ "fix the alias bug" · 2h ago`).
- **AC-3.2**: When `show_sessions` is `false`, no session lookups run and there is no latency cost.
- **AC-3.3**: Session data is fetched at most once per `lk` invocation and cached; re-renders (filtering, redraw) reuse the cache.
- **AC-3.4**: A project with no sessions shows no marker (not an empty/`(none)` label).
- **AC-3.5**: Session lookups are timeout-guarded; a slow or failing lookup degrades to "no marker", never blocks the menu.

### US-4 — Capability map
As a maintainer, I add or correct a CLI's resume behavior in one place.
- **AC-4.1**: A single declarative map defines each CLI's resume flag and list-sessions command. Empty resume flag = resume unsupported for that CLI.
- **AC-4.2**: kiro-cli entry: resume `--resume`, list `chat --list-sessions --format json`. claude entry: `--continue` marked to-verify. Others: empty (unsupported until filled).

## Non-Goals (this spec)

- Session picker UI inside the launcher (`--resume-id` selection) — deferred to a later phase; `--resume-picker` delegation may be considered but is not required here.
- Session deletion management from the launcher.
- Cross-CLI session detection for CLIs other than kiro-cli.
- claude live verification (no local install); claude entry ships as to-verify.

## Constraints

- No new runtime dependencies. JSON parsing must work without `jq` guaranteed (the repo cannot assume `jq`); use a bash/`sed`/`grep` parse or gate richer parsing on `jq` presence with a plain fallback.
- Must not regress the 588-test suite or the shellcheck-clean state.
- Follow existing patterns: config toggle like `show_git_status`, cache like the `.recent` file / CLI-version cache, argv assembly in `_lk_run_cli`/`_lk_launch_project`.
- Latency: with `show_sessions=false` (default), zero added cost. With it on, at most one batch/cached lookup per invocation.

## Rubric (done = all true)

- [ ] `r<N>` launches project N with `--resume` for kiro-cli.
- [ ] `lk resume <name>` and `lk <name> --resume` both work.
- [ ] Resume suppresses the per-project prompt.
- [ ] Unsupported CLI never receives a resume flag.
- [ ] `show_sessions` toggle gates all session lookups; default off = no latency.
- [ ] Session marker + last-title + age render when toggle on and sessions exist.
- [ ] New-tab generated script carries the resume flag.
- [ ] shellcheck clean; full suite green with new tests.
