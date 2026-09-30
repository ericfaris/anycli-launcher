# Brief: create a new project from the launcher

## Problem

Today `anycli-launcher` only lists existing subdirectories of the configured
projects root (`_AC_PROJECTS_DIR`, scanned one level deep by
`_ac_collect_projects` in `anycli-launcher.bash`). There's no way to create a
brand-new project folder from inside the `ac` menu — the user has to `mkdir`
and `git init` it manually outside the tool, then come back and it just
appears in the list.

## Goal

Let the user create a new project folder without leaving the `ac` menu:
press a key on the main menu, type a name, get a folder + git repo, and
optionally set its display name/color/title/prompt right then — all reusing
existing config-editing machinery rather than duplicating it.

## In scope

- A new key on the **main menu** (alongside `c`/`u`/`v`/`q`) to create a new
  project. Suggest `n` for "new" — confirm it's not already bound to
  something else in `ac()`'s key-handling block before committing to it (it
  currently isn't, based on the read of `ac()`'s while-loop, but the executor
  should double check since digit/letter keys and the filter fallback
  interact — any non-numeric, non-`/`-prefixed input not matching an existing
  case currently falls through to "set filter to this text", so the new key
  must be intercepted before that fallback, same as `c`/`u`/`v`/`?`/`q`).
- Prompt for a project name (folder name). Validate it:
  - non-empty
  - no path separators (`/`) — this creates a folder directly inside
    `_AC_PROJECTS_DIR`, not an arbitrary nested path
  - doesn't already exist in `_AC_PROJECTS_DIR` (case-sensitive collision
    check is fine, matching how directories are scanned)
  - reasonable filesystem-safe characters (reuse or mirror whatever
    sanitization logic already exists, e.g. `_ac_safe_name`, for consistency
    — that function encodes into a config-key-safe form, so check whether it
    should be used only for validation display or whether the raw name needs
    its own lighter check; the actual directory name should stay
    human-readable, matching how other project dirs are named on disk)
  - On invalid input, show an error and let the user retry or cancel back to
    the main menu (don't silently fail).
- Always create the new folder directly inside `_AC_PROJECTS_DIR` (the "root"
  entry, entry #2 in the menu) — **not** in any configured extra dirs
  (`_AC_EXTRA_DIRS`), even if some are configured. No root-picker UI needed.
- After `mkdir`, run `git init` inside the new folder (quiet/non-interactive,
  e.g. `git init -q`). If `git` isn't available or `git init` fails, don't
  hard-fail the whole flow — the folder still exists and is still a valid
  project; just skip/report that git init didn't happen (mirror how the rest
  of the script degrades gracefully when optional tools are missing, e.g.
  `_ac_find_python`, shellcheck-optional in tests).
- Immediately after creation, **offer to set project-specific config**
  inline: alias (display name), color, title, initial prompt — i.e. give the
  user a way to do what `_ac_config_edit_project_aliases` and
  `_ac_config_edit_project_style` do, but scoped to the one just-created
  project, without going through `_ac_config_pick_project`'s "pick from
  list" step (the project is already known — pass it directly). Reuse
  `_ac_edit_style_field` and `_ac_assign` for the actual field edits, keyed
  on `_ac_safe_name "$new_name"`, and call `_ac_write_config` after any
  change, exactly as the existing editors do. It's fine to literally reuse
  the same small submenu shape as `_ac_config_edit_project_style` (fields:
  color/title/prompt) plus an alias field, or to compose the two existing
  per-field editors into one combined "new project setup" screen — the
  executor's call, but it must not fork the field-editing logic into a
  separate implementation; call the existing helpers.
  - Make this step skippable (e.g. a "d done / skip" option), since the user
    may just want the folder created with defaults.
- After the whole flow (create + optional config), return to the main menu.
  The new project should now appear in the numbered list on the next render
  — since `_ac_collect_projects` re-scans the directory each loop iteration,
  no special cache invalidation should be needed, but verify this in testing
  (Acceptance criteria below).
- Do **not** cd into or launch the CLI in the new project automatically —
  just create it and land back on the refreshed main menu (per discovery
  answer: "just refresh the menu").

## Out of scope

- Creating projects in any root other than `_AC_PROJECTS_DIR` (no
  multi-root picker).
- Any project scaffolding beyond `mkdir` + `git init` (no template files,
  README, `.gitignore`, license, etc.) — this is a bare empty repo.
- Renaming/deleting/moving existing projects (separate feature).
- Non-interactive (`ac new <name>` CLI subcommand) form — this is purely the
  interactive main-menu flow described above. If trivial to add without
  complicating the interactive path, the executor may leave a `// TODO`-free
  note in the plan/PR description that it was considered, but should not
  build it unless it falls out naturally — don't scope-creep.
- Editing the "scan_depth 2" grouping-folder behavior in
  `_ac_collect_projects` — new projects are created as direct children of
  `_AC_PROJECTS_DIR`, same as any other flat project.

## Constraints

- Must fit the existing key-handling structure in `ac()`'s main while-loop
  (`anycli-launcher.bash`, roughly lines 4657–4760): quit/config/update/
  changelog/help are each intercepted with an `if [[ "$key" == ... ]]`
  before the catch-all "non-numeric input sets the filter" branch and the
  numeric selection branch. The new key must be added to that same
  intercept chain, in the same style, and the footer help line in
  `_ac_render_menu` (the `c … v … q …` list, around lines 4459–4463) should
  advertise the new key so it's discoverable, matching how existing keys are
  documented there.
- Must work across the project's supported platforms: Linux, macOS, WSL2,
  Git Bash (per README's "Supported platforms" and the existing
  `tests/check-platform.sh` cross-platform checks). `mkdir`/`git init` are
  fine everywhere, but double check any path handling matches existing
  conventions (e.g. `_ac_normalize_typed_path` handles `~` and Windows-style
  paths for typed paths elsewhere — this feature doesn't take a typed path,
  just a bare name, so this is likely a non-issue, but worth a sanity check).
- Follow the file's existing bash style: `_ac_`-prefixed function names,
  local variable declarations, the existing color-variable-per-function
  pattern (`c_title`, `c_num`, etc.) for any new interactive screen, and
  `_ac_clear` / `_ac_pause` usage matching neighboring functions like
  `_ac_config_edit_project_aliases`.
- Config persistence: any alias/color/title/prompt set during this flow must
  be written via the existing `_ac_write_config` (do not hand-roll YAML
  writing).
- Must not break the existing config-menu reload logic (`_ac_cfg_mtime`
  tracking in `ac()`'s loop) — since this flow may call `_ac_write_config`,
  make sure the outer loop picks up the change the same way returning from
  `_ac_config_menu` already does.

## Acceptance criteria

1. From the `ac` main menu, pressing the new key (e.g. `n`) prompts for a
   project name.
2. Entering a valid new name creates `"$_AC_PROJECTS_DIR/<name>"` as a
   directory, with a git repo initialized inside it (`.git` exists).
3. Entering an invalid name (empty, contains `/`, or already exists) shows
   an error and lets the user retry or back out, without creating anything
   or crashing the menu loop.
4. After creation, the user is offered to set alias/color/title/prompt for
   the new project; setting any of these persists to the config file
   (`~/.config/anycli-launcher/config.yaml` or equivalent) and is readable
   back via the normal per-project editors (`ac` → `c` → `7`/`8` should show
   the same values).
5. Skipping the inline config step still leaves the folder + git repo
   created, no config entries added.
6. After the flow completes (with or without inline config), the main menu
   redisplays and the new project appears in the numbered project list with
   any alias applied.
7. The new menu key is listed in the footer help text.
8. `git` missing/failing does not crash the flow — the folder is still
   created and the menu still works.
9. Existing test suite (`tests/test-bash.sh` via `tests/run-tests.sh`) still
   passes, and new tests are added covering: successful creation + git init,
   duplicate-name rejection, empty/invalid-name rejection, and that the
   inline config step correctly writes alias/color/title/prompt (mirroring
   the style of existing tests like the "config:" and "menu:" assertions in
   `tests/test-bash.sh`).
10. `shellcheck -s bash anycli-launcher.bash` still passes cleanly (part of
    `tests/run-tests.sh`'s lint gate) — new code must be shellcheck-clean or
    use the same suppression conventions as `.shellcheckrc`.

## Open questions & decisions made

- **Plan review**: user chose to skip the plan-approval gate — Opus plan
  will be sanity-checked by this session but not shown to the user for
  approval before building.
- **Entry point**: new key on the main menu (not buried in the config menu).
- **Post-create behavior**: refresh the menu; do not auto-launch into the
  new project.
- **Git init**: yes, automatically `git init` the new folder.
- **Target root when multiple roots configured**: always use
  `_AC_PROJECTS_DIR` (the main root); ignore `_AC_EXTRA_DIRS` for this
  feature.
- **Inline project config offer**: yes — after creating the folder, offer to
  set alias/color/title/prompt right away, reusing existing editor helpers
  rather than writing new ones.
- **Exact key binding** (`n` suggested) and **exact submenu layout** for the
  inline config step are left to the planner/executor's judgment, within the
  constraints above (must not collide with existing keys, must reuse
  existing field-editing helpers).

## Relevant files/areas

- `anycli-launcher.bash`
  - `ac()` main loop and key-handling (~lines 4472–4790, especially the
    intercept chain ~4657–4760)
  - `_ac_render_menu` footer help text (~lines 4459–4463)
  - `_ac_collect_projects` / `_ac_project_roots` (~lines 3629–3680) — how the
    project list is built each loop iteration; confirms no manual refresh
    needed after creating a folder
  - `_ac_config_edit_project_aliases` (~2575) and
    `_ac_config_edit_project_style` (~2134) — the alias/color/title/prompt
    editors to reuse
  - `_ac_edit_style_field` (~2111), `_ac_assign` (~106), `_ac_write_config`
    (~988), `_ac_safe_name` (~147) — shared low-level helpers
  - `_ac_clear` (~303), `_ac_pause` (~94) — UI helpers used by every
    interactive submenu
- `tests/test-bash.sh` — existing test patterns (`assert_eq`,
  `assert_contains`, `assert_exit`) to follow for new tests; look at the
  existing "config:" and "menu:" sections for style.
- `tests/run-tests.sh` — how the suite (incl. shellcheck lint gate) is
  invoked.
- `README.md` — screenshot/footer-key documentation may need a one-line
  update if the executor judges it in scope (menu footer keys are
  documented there); not required but consistent with existing doc
  practice.

## Repo commands & tree state

- **Test suite**: `./tests/run-tests.sh` from repo root (runs shellcheck
  lint gate if installed, then `tests/test-bash.sh`). No separate build
  step — this is a sourced bash script, not a compiled artifact.
- **Direct unit test run**: `bash tests/test-bash.sh`
- **Platform check** (not required for this feature, but available):
  `./tests/check-platform.sh`
- **Working tree state at brief time**: clean (`git status --short` empty),
  on `main`, HEAD at `50a9d3e`. No pre-existing uncommitted changes to
  account for.
- Repo is a **solo repo** (per global CLAUDE.md convention) — committing to
  `main` directly is acceptable once tests pass, but only commit if the user
  asks.
