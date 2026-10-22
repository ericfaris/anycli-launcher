# Design Review — Session-Resume Phase 2 (Session-Aware Menu)

**Spec**: `docs/specs/session-resume/` (requirements US-3, US-4; design Component 3)
**Reviewer**: Kiro (self-review, pre-implementation gate)
**Date**: 2026-09-04
**Scope**: `show_sessions` toggle + `_lk_session_summary` + in-invocation cache + menu render.

## Check 1: Menu-Render Impact

**Finding: Low risk — a proven template exists.**

Phase 2 mirrors the existing `show_git_status` → `_lk_git_summary` → `_git_suffix` pipeline exactly:
- `show_git_status` gates a per-row `_lk_git_summary "$dir"` call (line 3347) whose output becomes a dim suffix (`_git_suffix`, line 3365) appended to the project row.
- `show_sessions` will gate a per-row `_lk_session_summary "$dir"` call, producing a parallel `_session_suffix`.

Render integration: the row printer already composes optional suffixes (`_git_suffix`, app version, update marker). Adding a `_session_suffix` is additive — append it in the same three `printf` branches. **Risk**: row-width budget. Git status + app version + update marker + session title could overflow narrow terminals. Mitigation: truncate the session title to a fixed budget (32 chars) and place the session suffix last so it's the first thing to run off-screen, not the project name.

**Verdict: Clean to proceed.** Follow the git-status render pattern; truncate title; session suffix goes last.

## Check 2: Config Contract

**Finding: Clean, provided all four sites are updated (the known failure mode).**

`show_git_status` touches exactly four sites; `show_sessions` must touch the same four to avoid a phantom/missing key:
| Site | Line (git) | Action for `show_sessions` |
|------|-----------|----------------------------|
| Default | 300 (`_LK_SHOW_GIT_STATUS="false"`) | `_LK_SHOW_SESSIONS="false"` |
| Parse | 406 (`show_git_status) ...`) | `show_sessions) _LK_SHOW_SESSIONS="$value" ;;` |
| Write | 700 (`echo "show_git_status: ..."`) | `echo "show_sessions: $(_lk_yaml_quote ...)"` |
| Config menu | toggle entry | add a `show_sessions` toggle |

**Risk**: forgetting one site → the key silently resets or never persists. Mitigation: a config round-trip test (write → reload → assert) mirroring the existing `show_git_status` round-trip test. This is exactly the M-class config contract failure the harness guards against; the test closes it.

**Verdict: Clean to proceed.** Update all four sites; add a round-trip test.

## Check 3: jq / no-jq Parse Risk

**Finding: The parse is the real risk area; contain it with a best-effort contract.**

`kiro-cli chat --list-sessions --format json` emits single-line JSON: `[{"cwd":...,"sessions":[{"sessionId","source","title","updatedAt","messageCount"}...]}]`. The launcher must extract the **newest** session's `title` + `updatedAt`.

- **jq present** (verified on dev machine, jq 1.7): `jq -r '.[0].sessions | sort_by(.updatedAt) | last | "\(.title)\t\(.updatedAt)"'` — robust, handles titles with quotes/commas/unicode. Do NOT rely on array order; sort by `updatedAt`.
- **jq absent**: bash/grep/sed fallback. Titles are free-form user text (may contain `"`, `,`, `:`, escaped chars) — a regex parse is inherently fragile. **Contract**: the fallback is *best-effort*. If it can cleanly extract a title, show it; if parsing is ambiguous, degrade to a plain marker (`⏵ session`) with no title. A parse miss must NEVER break the menu or emit garbage into the row.
- **Timeout**: `timeout 3` around the `cd`+list call (kiro-cli spawn can be slow); on non-zero/empty, cache "no sessions" so it isn't retried (mirrors `_lk_git_summary`'s fail-quiet return).

**Risk**: (a) fallback mis-parses a tricky title → mitigated by best-effort degradation; (b) latency from per-project CLI spawns → mitigated by the toggle (default off = zero cost) + one-shot in-invocation cache; (c) `messageCount:0` "empty" sessions (seen in real data) counting as resumable → decide: treat `messageCount==0` as no-session so the marker reflects real work.

**Verdict: Clean to proceed with guardrails.** jq-preferred, best-effort bash fallback, timeout-guarded, cache-once, treat 0-message sessions as none. Tests must cover: jq path, no-jq fallback (simulate by masking jq), malformed JSON → marker-only, empty list → no marker, toggle-off → zero CLI calls.

## Summary

| Check | Status | Key guardrail |
|-------|--------|---------------|
| Menu-render impact | Clean | Follow git-status suffix pattern; truncate title; suffix last |
| Config contract | Clean | Update all 4 sites; round-trip test |
| jq/no-jq parse | Clean w/ guardrails | jq-preferred, best-effort fallback, timeout, cache, 0-msg = none |

**Overall verdict: Clean to proceed to implementation.** No blocking issues. The dominant risk is the no-jq parse, contained by the best-effort contract (never break the menu; degrade to a title-less marker). Proceed T2.1 → T2.5 with the tests named above.
