# Code Review — kiro-launcher 1.12.0

**Repo**: kiro-launcher
**Branch/Commit**: main @ 600aedf ("fix(menu): restore per-project aliases and robust TTY drain on spawn")
**Reviewer**: Kiro (generic multi-agent code-review, Tier 1)
**Review Date**: 2026-09-04
**Detection**: Bash shell scripts — no framework detected, generic passes applied
**Review Type**: Full (6 passes; diff > 30 lines)
**Pre-Flight**: FULL — 2 code files structural (`kiro-launcher.bash`, `tests/test-bash.sh`); 4 doc/config files non-code (`.gitignore`, `CHANGELOG.md`, `README.md`, `VERSION`)
**Tooling**: Clean — `bash -n` passes on both shell files; `shellcheck` not installed (skipped)

**Tooling**: `bash -n` clean on both shell files. `shellcheck 0.10.0` installed post-review and run — 44 findings on `kiro-launcher.bash` (mostly pre-existing/whole-file; see Tooling Findings section). No errors, all info/warning severity.

## Tooling Findings (Mechanical — shellcheck 0.10.0)

Run after install (`shellcheck -s bash kiro-launcher.bash`). shellcheck reports info/warnings only — no errors. Findings that intersect this commit's changed code:

| Line(s) | Code | Verdict | Note |
|---------|------|---------|------|
| 603, 611, 613 | SC2016 | **False positive (intentional)** | Drain block emits literal shell code via `printf '%s\n' '...$_saved...'`; single quotes are required so `$_saved`/`$_junk` do NOT expand at generation time. Suppress with a `# shellcheck disable=SC2016` directive on the block. |
| 1628, 1899 | SC2178 | **False positive** | `found` is an integer counter, never an array; shellcheck mis-attributes `varname`'s loop usage from the shared `local varname found=0` line. Cheap fix: declare on separate lines. |
| 2017, 2029 | SC2155 | Real (low) | `local v="$(...)"` masks the command-substitution exit status. Style-only here (the substitution can't meaningfully fail). Split declare/assign to silence. |

Pre-existing / out of scope for this commit (whole-file, not introduced by these changes): SC2034 (15× unused theme vars in `_lk_apply_theme_*`), SC2206 (3× word-splitting in version parsing), SC1003/SC2059 (encoding helpers). These predate the commit and are noted for a future cleanup pass, not this review.

**Recommendation**: Add a `.shellcheckrc` or inline `# shellcheck disable=` directives for the intentional SC2016 drain lines so shellcheck can be wired into CI/tests cleanly. The SC2178/SC2155 items are trivial hygiene fixes; the pre-existing SC2034 batch deserves its own cleanup task.

## Scope

The commit bundles four logical changes:
1. **TTY drain-loop** on new-tab spawn (fixes stray `2c/3434` prompt leak) — reviewed in depth
2. **Tolerant alias lookup** reconciling hyphen/underscore config keys (restores menu aliases) — reviewed in depth
3. **CLI short-option** help parsing (`-a`, `-v`) — pre-existing working-tree change, now committed
4. **Shared project picker + appearance editor** (`_lk_config_pick_project`, `_lk_config_edit_project_style`) — pre-existing working-tree change, now committed

## Summary Table

| ID | Severity | Pass | Location | Finding |
|----|----------|------|----------|---------|
| M1 | MEDIUM | Logic | `_lk_lookup_alias` | Normalized fallback can return the wrong alias when two project names collide under normalization (`my-app` vs `my_app`) |
| M2 | MEDIUM | Test | tests | New CLI short-option parsing branch is untested |
| M3 | MEDIUM | Test | tests | Normalization-collision case (M1) is untested/unspecified |
| L2 | LOW | Pattern | ~2851 | `_lk_project_label` doc-comment separated from its function by two newly inserted functions |
| L3 | LOW | Pattern | `_lk_config_edit_project_style` | Three near-identical color/title/prompt branches — extractable duplication |
| L4 | LOW | Test | tests | Appearance editor (incl. color-reject path) has no test |
| L5 | LOW | Perf | `_lk_lookup_alias` | Unaliased projects run the full O(aliases) fallback scan (subshell forks) on every render |
| L6 | LOW | Integration | drain block | `stty raw` restore is not `trap`-guarded; signal in the tiny window could leave raw mode |

No CRITICAL or HIGH findings. No security findings.

## Findings by Severity

### MEDIUM

**M1 — Normalized alias fallback collision** (`kiro-launcher.bash`, `_lk_lookup_alias`)
`_lk_normalize_name` maps both `-` and `_` to the same character and lowercases, so `my-app` and `my_app` normalize identically. The exact-key fast path protects any project that has *its own* alias, so impact is limited: an **unaliased** project whose normalized name collides with an aliased sibling will display the sibling's alias, and among multiple aliased collisions the winner is the first in `compgen` order (effectively arbitrary). Real bug, low practical likelihood. Options: (a) accept as by-design and document, (b) prefer an exact case-sensitive match in the fallback before falling back to normalized, or (c) skip the fallback entirely when the requested name exists on disk as-is. Recommendation: (b) — try exact-decoded match first, normalized only as last resort.

**M2 — Short-option parsing untested** (`tests/test-bash.sh`)
The commit adds regex branches to collect short options (`-a, --trust-all-tools` → long form; bare `-v` → short). The existing flag-parsing test feeds only long-form help. Add a fixture line like `-v, --version` and a bare `-q` and assert the collected result. This directly exercises the claimed fix.

**M3 — Collision case untested** (`tests/test-bash.sh`)
Pin down M1: register an alias for `my-app`, leave `my_app` unaliased, and assert the chosen behavior for `_lk_lookup_alias "my_app"`. Whether the resolution is "no match" (after fix) or "documented collision," a test makes the contract explicit.

### LOW

- **L2 — Doc-comment adjacency**: Move `_lk_normalize_name`/`_lk_lookup_alias` above the `# The label shown for a project directory…` comment, or attach that comment directly to `_lk_project_label`, so the doc sits on the function it describes.
- **L3 — Editor duplication**: Extract `_lk_edit_style_field <var> <label> [validator]` to collapse the color/title/prompt branches (~30 lines). Non-blocking.
- **L4 — Appearance editor untested**: Add a piped-input smoke test covering render and the bad-color reject path (`^#[0-9a-fA-F]{6}$`).
- **L5 — Fallback scan cost**: Unaliased projects always run the O(aliases) fallback with per-iteration subshells on every render. Consider short-circuiting when no aliases are configured, or building a normalized map once per render if project counts grow.
- **L6 — Untrapped raw mode**: Add `trap 'stty "$_saved" 2>/dev/null' EXIT INT TERM` inside `_lk_drain_tty` so an interrupt during the drain window cannot leave the terminal in raw mode.

## Verification Notes (self-check)

- Verified drain runs before `exec` and degrades to a no-op when stdin is not a TTY (`[[ -t 0 ]]`), across all three launch branches (WSL / Git Bash / fallback).
- Verified no user data is interpolated into the generated drain function (static single-quoted `printf`), preserving the existing `%q` command-injection hardening.
- Verified the alias fix resolves against the user's real config (`lumen`, `nexus`, `tmcp`) and that the full suite passes 573/0.
- Confirmed `bash -n` clean on both changed shell files.

## Verdict

**Ready to Merge (already merged)** — with follow-ups recommended.

No blocking issues. The two headline fixes (drain, alias) are correct and well-tested. The findings are one genuine-but-low-likelihood logic edge (M1, normalization collision) and test-coverage gaps for the bundled pre-existing changes (M2, M3, L4). Since the commit is already on `main`, treat M1–M3 as fast-follow items:

1. Fast-follow: address M1 (prefer exact match in fallback) + M3 (collision test)
2. Fast-follow: M2 (short-option test)
3. Optional: L2, L3, L4, L5, L6 as cleanup

## Resolution Log (2026-09-04, follow-up commits)

| Finding | Status | How |
|---------|--------|-----|
| M1 | Resolved | `_lk_lookup_alias` fallback rewritten to two collision-safe stages: exact case-insensitive match, then the precise one-directional legacy shape (hyphen dir → underscore key). `_lk_normalize_name` removed as dead code. A real `my_app` no longer inherits a `my-app` alias. |
| M2 | Resolved | Added short-option fixtures (`-a, --trust-tools`, `-v, --version`, bare `-q`) and assertions; also pinned that `--version` stays filtered even via a short alias. |
| M3 | Resolved | Added collision tests: `my_app` does NOT inherit `my-app`'s alias; hyphen dir still matches legacy underscore key; unrelated underscore project stays empty; case-insensitive exact match resolves. |
| L2 | Resolved | Moved the `_lk_project_label` doc-comment to sit directly on the function (was orphaned above the alias helpers). |
| L6 | Resolved (prior commit) | `INT`/`TERM` trap added in `_lk_drain_tty`; guarded by tests. |
| L3 | Resolved | Extracted `_lk_edit_style_field` helper; the three color/title/prompt branches now delegate to it (~30 lines removed). |
| L4 | Resolved | Added appearance-editor tests: valid-color assign, invalid-color reject (rc=1, not assigned), blank-clears, unvalidated-assign, and menu-render. |
| L5 | Resolved | `_lk_lookup_alias` captures the alias var list once via `mapfile` and short-circuits the fallback when no aliases are configured. |

Post-fix: 588 tests pass, shellcheck clean.

## Intent Violations

None observed — the changes conform to the project's `_lk_` naming, `local` discipline, and script-generation idioms.
