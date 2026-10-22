# Tasks — Session Resume & Session-Aware Menu

Phased so each phase is independently shippable and testable. TDD: write the test alongside each task.

## Phase 1 — Resume Launch (core value, low risk)

- [ ] **T1.1** Add `_LK_CLI_CAPS` map + accessors `_lk_cli_resume_flag`, `_lk_cli_list_argv`.
  - Test: kiro-cli→`--resume`; unknown tool→empty.
- [ ] **T1.2** Thread a `resume` param through `_lk_run_cli` (current-tab): insert resume flag after the launch subcommand, omit prompt when resuming, informational note when unsupported.
  - Test: argv order; prompt suppressed on resume; no flag for unsupported CLI.
- [ ] **T1.3** Thread `resume` through `_lk_launch_project` new-tab branch: resume flag in `launch_argv`, prompt dropped.
  - Test: extend generated-script assertions to check the resume flag is present when resuming and absent otherwise.
- [ ] **T1.4** Menu input handler: accept `^[rR][0-9]+$` → resume-launch that index. Add legend entry `r<N>  resume`.
  - Test: piped `r3` launches project 3 with resume; bare `3` unchanged (no resume flag).
- [ ] **T1.5** CLI dispatch: `lk resume <name>` and `lk <name> --resume|-r`; project named `resume` wins.
  - Test: both forms resume; `resume` project name precedence.
- [ ] **T1.6** Docs: README "Usage" table + CHANGELOG entry for resume.
- [ ] **T1.7** Verify: shellcheck clean, full suite green.

## Phase 2 — Session-Aware Menu (differentiator, behind toggle)

- [ ] **T2.1** Add `show_sessions` config (parse, write, default `false`) mirroring `show_git_status`.
  - Test: config round-trip; default off.
- [ ] **T2.2** Config-menu toggle entry for `show_sessions`.
  - Test: menu renders the option.
- [ ] **T2.3** `_lk_session_summary <dir>`: timeout-guarded `cd`+list-argv, parse newest session (jq path + no-jq fallback), print `title\tepoch` or empty.
  - Test: mocked JSON → correct newest title+age; malformed → empty/marker-only; empty list → empty.
- [ ] **T2.4** In-invocation cache `_LK_SESSION_CACHE`; populate lazily on first render when toggle on; reuse on re-render.
  - Test: with toggle off, zero list-command calls (assert mock call log empty); with on, one call per project.
- [ ] **T2.5** Relative-age helper (reuse if present, else add) + row rendering: marker + truncated title + age; no marker when empty.
  - Test: render contains marker+title+age; no marker for session-less project.
- [ ] **T2.6** Docs: README config section (`show_sessions`) + CHANGELOG.
- [ ] **T2.7** Verify: shellcheck clean, full suite green; measure render latency with a mocked slow list command stays bounded by the timeout.

## Phase 3 — (Optional, later) Session Picker

- [ ] **T3.1** Per-project key to list sessions and pick → launch `--resume-id <id>`, or delegate to `--resume-picker`.
  - Deferred; not part of the initial delivery.

## Sequencing & Review Gates

1. Phase 1 first — delivers the headline value with contained risk. Land + push.
2. Design review before Phase 2 (touches menu render + config contract + the no-jq parse). Then implement.
3. Phase 3 only if Phase 1/2 prove valuable.

## Open Items Needing External Input

- **claude flags** — verify `--continue` vs `--resume` and the list-sessions command on a machine with claude installed; fill the `_LK_CLI_CAPS` claude entry. Until then it ships as to-verify and claude resume is effectively best-effort.
- **jq availability** — decide whether to hard-require jq for Phase 2 richness or keep the bash fallback. Default per spec: no hard requirement, bash fallback with best-effort title.
