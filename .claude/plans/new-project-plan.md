# Implementation Plan: create a new project from the launcher menu

Repo: `/home/eric/projects/anycli-launcher` (branch `main`, HEAD `50a9d3e`,
clean tree). Source brief: `.claude/plans/new-project-brief.md`.

All line numbers below refer to the tree at `50a9d3e` and are approximate —
locate code by function name / literal string, not by number.

---

## 1. Summary

`anycli-launcher` (the `ac` shell function in `anycli-launcher.bash`) renders a
numbered menu of the subdirectories of `$_AC_PROJECTS_DIR` and launches an AI
CLI in the one you pick. Today there is no way to *create* a project from the
menu — you leave `ac`, `mkdir` + `git init` by hand, and come back. This change
adds an `n` key to the main menu that prompts for a folder name, validates it,
creates `"$_AC_PROJECTS_DIR/<name>"`, runs `git init -q` inside it (degrading
gracefully when `git` is missing or fails), then offers an inline
alias/color/title/prompt setup screen for the brand-new project that reuses the
existing per-field editors (`_ac_edit_style_field`, `_ac_assign`,
`_ac_write_config`) rather than forking them. When the flow finishes (or is
skipped/cancelled), control returns to the main loop, which re-scans the
projects directory on every iteration, so the new project simply appears in the
numbered list with any alias applied.

---

## 2. Approach & key decisions

### 2.1 Three new functions, no changes to existing helpers

Add exactly three new functions to `anycli-launcher.bash`:

| Function | Role |
|---|---|
| `_ac_validate_new_project_name <name>` | Pure validator. Prints a human error message to **stdout** and returns 1 when the name is unusable; prints nothing and returns 0 when it is fine. Reads `$_AC_PROJECTS_DIR` for the collision check. No `read`, no screen clearing — directly unit-testable. |
| `_ac_new_project_setup <name>` | The inline "set alias/color/title/prompt for this project" submenu. Same shape as `_ac_config_edit_project_style`'s inner `while true` loop, but scoped to one already-known project (no `_ac_config_pick_project` step) and with an extra `alias` field. |
| `_ac_new_project` | Top-level flow: prompt → validate (retry loop) → `mkdir` → `git init` → register → call `_ac_new_project_setup` → return. |

No existing function's behaviour changes. `_ac_edit_style_field` already does
exactly what is needed per field (blank clears via `unset`, non-blank assigns
via `_ac_assign` after optional regex validation, then calls
`_ac_write_config`), so all four fields go through it — including alias, which
is why this is a *reuse* rather than a copy of
`_ac_config_edit_project_aliases`' hand-rolled read/assign/unset block.

**Rejected:** refactoring `_ac_config_edit_project_style` /
`_ac_config_edit_project_aliases` to take an optional project argument and
calling them from the new flow. It would work, but both functions open with a
full-screen "here is every configured project" listing plus a
`_ac_config_pick_project` call, and threading an optional arg through that
preamble makes two well-tested functions conditional and harder to read for no
gain. A small dedicated submenu that calls the same *leaf* helper
(`_ac_edit_style_field`) keeps the logic single-sourced where it actually
matters.

**Rejected:** a non-interactive `ac new <name>` subcommand. Explicitly out of
scope per the brief; the `ac()` argument dispatch (the
`help|version|list|config|doctor|setup` case) would also need a
"project literally named `new` wins" guard, which is complexity this feature
does not need. Do not build it.

### 2.2 Key binding: `n` / `N`

Confirmed free. The keys intercepted in `ac()`'s main `while` loop today are
`q/Q`, `c/C`, `u/U`, `v/V`, `?`, `/` and `/<text>`, plus the regex triggers
`^[rR][0-9]+$` and `^[pP][0-9]+$`. `n` collides with none of them.

**Ordering is load-bearing.** The loop's catch-all is:

```bash
# Any other non-numeric input is treated as a name filter
if [[ -n "$key" && ! "$key" =~ ^[0-9]+$ ]]; then
    _ac_filter="$key"
    continue
fi
```

so the `n` intercept must be added **before** that branch — put it immediately
after the `?` help-key block and before the `"/"` filter-clearing block, so the
whole intercept chain stays in "single-letter commands, then filter syntax,
then numeric" order.

Match `n` and `N` exactly (`[[ "$key" == "n" || "$key" == "N" ]]`), not a
prefix — typing `node` as a filter must still filter, exactly like `config`
still filters despite the `c` key.

### 2.3 Name validation rules

`_ac_validate_new_project_name` rejects, in this order (first failure wins, one
message each):

1. **Empty / whitespace-only** → handled by the caller as "cancel" (see 2.4);
   the validator still rejects it defensively with
   `"Name cannot be empty."`.
2. **Contains a path separator** — `/` or `\` → `"Name cannot contain a path separator (/ or \\) — projects are created directly inside <root>."`
   Checking `\` too matters on Git Bash, where a backslash is a separator.
3. **Character allowlist**: must match `^[A-Za-z0-9][A-Za-z0-9._-]*$` →
   `"Use letters, digits, dot, dash or underscore; must start with a letter or digit."`
   This deliberately also rejects `.`, `..`, leading dots (hidden dirs are not
   scanned as projects anyway), leading `-` (argument confusion), spaces, and
   the Windows-reserved set `: * ? " < > |` — so the same name is creatable on
   Linux, macOS, WSL2 and Git Bash.
4. **Already exists**: `[[ -e "$_AC_PROJECTS_DIR/$name" ]]` (use `-e`, not
   `-d`, so a colliding *file* is caught too) →
   `"A project named '<name>' already exists."` Case-sensitive, matching how
   `_ac_collect_projects` globs the directory.

**Decision on `_ac_safe_name`:** it is *not* used to validate or transform the
directory name. It is a config-key encoder (`my-app` → `my_2dapp`), and the
directory on disk must stay human-readable like every other project dir. It is
used only in the config-writing path, keyed as
`safe="$(_ac_safe_name "$name")"` → `_AC_PROJECT_ALIAS_${safe}` etc., exactly
as `_ac_config_edit_project_style` does. The allowlist above is the *separate,
lighter* raw-name check the brief asks for.

### 2.4 Cancel & retry UX (no extra keypress consumed)

The prompt loop re-renders each iteration and carries a `err_msg` local that is
printed above the prompt when non-empty. That means an invalid name costs the
user exactly one more line of input (the corrected name) and **no `_ac_pause`**
— important because `_ac_pause` over a non-tty pipe consumes a whole line,
which makes piped tests fragile.

- **Blank input = cancel**, returns to the main menu. Advertise it in the
  prompt: `Project name (blank to cancel):`.
- **Invalid input** = error is shown, prompt repeats.
- `read -r name || return 0` — EOF must return cleanly, never spin.

Use `_ac_pause` only for terminal states the user must read before the menu
repaints: the post-creation success/failure summary (see 2.5). That is
consistent with `_ac_config_pick_project` and `_ac_show_key_help`.

### 2.5 Creation & git init

```bash
local dir="${_AC_PROJECTS_DIR%/}/$name"
mkdir -- "$dir" 2>/dev/null || { <error + _ac_pause>; return 0; }
```

Plain `mkdir` (not `-p`): the parent is guaranteed to exist (the main loop
errors out earlier when `$_AC_PROJECTS_DIR` is missing) and the duplicate case
is already rejected, so `-p` would only mask real failures such as a read-only
root. A `mkdir` failure prints an error, pauses, and returns 0 — the menu loop
must never be broken.

`git init` degrades gracefully, mirroring `_ac_find_python` / the optional
shellcheck gate:

```bash
local git_bin git_note
git_bin="$(_ac_resolve_bin git)"
if [[ -z "$git_bin" ]]; then
    git_note="git not found on PATH — folder created without a repo"
elif "$git_bin" -C "$dir" init -q >/dev/null 2>&1; then
    git_note="git repository initialized"
else
    git_note="git init failed — folder created without a repo"
fi
```

Notes:
- Use `_ac_resolve_bin git` (not `command -v`) — the file's established way to
  get a real executable path past shell aliases/functions, and it is what makes
  the "missing git" branch testable by shadowing `git` in the test's mock PATH.
- `git -C <dir> init -q` matches existing usage elsewhere in the file
  (`_ac_update_launcher`, `_ac_git_summary`, `_ac_update_probe` all use
  `git -C`). Redirect both streams: recent git prints a default-branch-name
  hint on stderr even with `-q`.
- The flow **continues to the config step regardless** of the git outcome. The
  folder is a valid project either way.

Then `_ac_register_project "$name"` **before** any config write — otherwise
`_ac_write_config` resolves the project key back through `_ac_project_name`
without a registry entry.

### 2.6 Config reload interaction

Nothing special is needed. `ac()`'s loop re-reads the config only when its
mtime changed:

```bash
_ac_cfg_mtime_now="$(_ac_file_mtime "$_AC_CONFIG_FILE")"
if (( ! _ac_cfg_loaded )) || [[ "$_ac_cfg_mtime_now" != "$_ac_cfg_mtime" ]]; then
```

`_ac_edit_style_field` always calls `_ac_write_config`, which rewrites the file
and bumps its mtime, so the next iteration reloads — identical to what already
happens on return from `_ac_config_menu`. And `_ac_collect_projects` re-globs
the root every iteration unconditionally, so a folder created with no config at
all still appears. **Do not** add a manual `_ac_load_config` or cache-bust call.

One real hazard to be aware of (not a code change, a test-writing rule): on a
coarse-mtime filesystem two writes within the same second can produce the same
mtime. Irrelevant here because the reload is only needed to *re-read what we
ourselves just set in memory*; the in-memory `_AC_PROJECT_*` variables are
already correct whether or not the reload fires.

### 2.7 Discoverability

- `_ac_render_menu` footer: both branches (update-available and not) gain
  `n  new`.
- `_ac_show_key_help`: gains a line for `n`.
- `README.md`: the sample menu block and the Usage key table.
- `CHANGELOG.md`: an `## Unreleased` → `### Added` entry. **Do not bump
  `VERSION`** — `tests/test-bash.sh` asserts `_AC_VERSION` equals the `VERSION`
  file's contents, and the release-version choice is the user's.

---

## 3. Step-by-step tasks

Each step is independently verifiable. Run `bash tests/test-bash.sh` after
steps 2, 3, 4 and 7, and the full `./tests/run-tests.sh` at the end.

### Step 1 — Add `_ac_validate_new_project_name`

**File:** `anycli-launcher.bash`
**Where:** immediately after `_ac_config_edit_project_aliases`'s closing brace
(~line 2620) and before the `# --- Changelog Viewer ---` comment (~2621).

Signature: `_ac_validate_new_project_name <name>`. Implements the four rules
from §2.3, in that order, printing one message on stdout and returning 1;
returns 0 silently otherwise. All locals declared `local`. Add a short comment
block above it explaining *why* `_ac_safe_name` is not used here (directory
names stay human-readable; `_ac_safe_name` is a config-key encoder).

The error strings are asserted by tests — keep them stable once written, and
name them in the test file by the same literal.

### Step 2 — Add `_ac_new_project_setup`

**File:** `anycli-launcher.bash`, directly below Step 1's function.

Signature: `_ac_new_project_setup <project-name>`.

```
local proj="$1" safe
safe="$(_ac_safe_name "$proj")"
```

Then a `while true` loop, modelled line-for-line on
`_ac_config_edit_project_style`'s inner loop (same color locals: `c_dim`,
`c_title`, `c_num`, `c_name`, `c_prompt`, `c_val`, `reset`; same `_ac_clear` at
the top of each iteration; same `printf "\n  %s›%s " "$c_prompt" "$reset"`
prompt; same `read -r choice || return 0`):

- Header: `Set up: <proj>` plus a dim hint line
  `(all optional — press d when you're done)`.
- Show current values via `_ac_indirect "_AC_PROJECT_ALIAS_${safe}"` etc.,
  rendering an unset value as `(none)`.
- Menu rows:
  - `1  alias` → `_ac_edit_style_field "_AC_PROJECT_ALIAS_${safe}" "Display name (blank to clear): "`
  - `2  color` → `_ac_edit_style_field "_AC_PROJECT_COLOR_${safe}" "Hex color (e.g. #00cc66, blank to clear): " '^#[0-9a-fA-F]{6}$' "6-digit hex color" || continue`
  - `3  title` → `_ac_edit_style_field "_AC_PROJECT_TITLE_${safe}" "Tab title (blank to clear): "`
  - `4  prompt` → `_ac_edit_style_field "_AC_PROJECT_PROMPT_${safe}" "Initial prompt (blank to clear): "`
  - `d  done` → `return 0` (also accept `D`)
  - `*` → `continue`

Note the `|| continue` on the color case only — that mirrors
`_ac_config_edit_project_style` exactly (the helper returns 1 without writing
when the regex fails, so the loop just re-renders).

Do **not** call `_ac_write_config` here; `_ac_edit_style_field` already does on
every accepted change.

### Step 3 — Add `_ac_new_project`

**File:** `anycli-launcher.bash`, directly below Step 2's function.

No arguments. Structure:

1. Color locals (`c_title`, `c_dim`, `c_name`, `c_prompt`, `c_err`, `c_ok`,
   `reset`) in the file's `local x=$'\e[...m'` style; `c_err=$'\e[31m'` and
   `c_ok=$'\e[32m'` match existing usage in `_ac_edit_style_field` /
   `_ac_first_run_wizard`.
2. `local name="" err_msg=""` then `while true`:
   - `_ac_clear`
   - print header `New Project` and a dim line showing the destination root
     via `_ac_tildify "$_AC_PROJECTS_DIR"`.
   - if `-n "$err_msg"`, print it with `c_err` and a `✗` prefix, matching
     `_ac_edit_style_field`'s error style.
   - prompt `Project name (blank to cancel): `, `read -r name || return 0`
   - `[[ -z "$name" ]] && return 0`
   - `if err_msg="$(_ac_validate_new_project_name "$name")"; then break; fi`
     — on success the command substitution is empty so `err_msg` is cleared
     naturally; on failure it holds the message and the loop repeats.
     (Watch the exit-status semantics: `err_msg="$(cmd)"` takes `cmd`'s status,
     which is what makes this one-liner correct.)
3. Create the directory and run git init per §2.5.
4. `_ac_register_project "$name"`.
5. Print a success block: `✓ Created <tildified dir>` and the `git_note` line,
   then ask whether to configure it now:
   `Set alias / color / title / prompt now? (Y/n): `, `read -r ans || ans="n"`.
   Treat empty/`y`/`Y` as yes → `_ac_new_project_setup "$name"`; anything else
   skips. (This mirrors the `Create it and continue? (Y/n):` prompt in
   `_ac_first_run_wizard`.) Skipping must leave the config file untouched.
6. `return 0`.

If `mkdir` fails: print the error, `printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"`, `_ac_pause`, `return 0` — the same terminal-state
pattern `_ac_config_pick_project` uses.

### Step 4 — Wire the `n` key into `ac()`

**File:** `anycli-launcher.bash`, inside `ac()`'s main `while true` loop.

Insert immediately after the `if [[ "$key" == "?" ]]` block and **before** the
`if [[ "$key" == "/" ]]` block (~line 4700):

```bash
        # New project: create a folder (+ git repo) under the projects root
        # and optionally configure it, then fall back into the menu, which
        # re-scans the root every iteration so the new entry appears.
        if [[ "$key" == "n" || "$key" == "N" ]]; then
            _ac_new_project
            continue
        fi
```

Verify by inspection that this sits above the `_ac_filter="$key"` catch-all.

### Step 5 — Footer help text

**File:** `anycli-launcher.bash`, `_ac_render_menu` (~lines 4459–4465).

Add `n  new` to **both** `printf` branches, between `changelog` and `quit`, in
the same `%s n%s  new` / `"$c_num" "$reset"` style:

- update-available branch: `c config | u update ⬆ | v changelog | n new | q quit` + `$_help_key`
- normal branch: `c config | v changelog | n new | q quit` + `$_help_key` + `(type to filter)`

Keep the existing two-space separators; trim one space of padding between
groups if the line grows uncomfortably wide (the normal branch currently ends
with four spaces before `(type to filter)`).

### Step 6 — `_ac_show_key_help`

**File:** `anycli-launcher.bash`, `_ac_show_key_help` (~line 2676).

Extend the existing combined line to include `n`, e.g.:

```bash
    printf "   %s c%s   config    %s n%s  new project    %s v%s  changelog    %s q%s  quit\n" ...
```

### Step 7 — Tests

**File:** `tests/test-bash.sh`, inserted **above** the
`# === NEW TESTS GO ABOVE THIS LINE ===` marker near the end of the file.

Open a new section header in the file's style:

```bash
# ═══════════════════════════════════════
echo "── New Project (n) ──"
```

Use only the existing helpers `assert_eq` / `assert_contains` / `assert_exit`
and the inline `if …; then printf '  \e[32m✓\e[0m …'; ((PASS++)); else …` form
for boolean checks. Every test in this block must use a `mktemp -d` root and a
`mktemp` config file — **never** `$FIXTURES/config.yaml`, because this flow
writes the config.

Shared setup for the block:

```bash
NEWP_DIR=$(mktemp -d)
NEWP_CONFIG=$(mktemp)
_AC_CONFIG_FILE="$NEWP_CONFIG"
export AC_PROJECTS_DIR="$NEWP_DIR"
_AC_CLI_FLAGS=""; _AC_CLI_TOOL="kiro-cli"; _AC_CLI_LAUNCH="chat"
_AC_AUTO_UPDATE_CHECK="false"
for v in $(_ac_vars_with_prefix _AC_PROJECT_ALIAS_); do unset "$v"; done
for v in $(_ac_vars_with_prefix _AC_PROJECT_COLOR_); do unset "$v"; done
for v in $(_ac_vars_with_prefix _AC_PROJECT_TITLE_); do unset "$v"; done
for v in $(_ac_vars_with_prefix _AC_PROJECT_PROMPT_); do unset "$v"; done
_ac_write_config
_ac_load_config
```

Tests to write (each maps to an acceptance criterion; AC numbers from the
brief in brackets):

**7a. Validator unit tests [AC 3]** — call `_ac_validate_new_project_name`
directly, no stdin needed. With `_AC_PROJECTS_DIR="$NEWP_DIR"` and an existing
`mkdir "$NEWP_DIR/taken"`:

| input | expect |
|---|---|
| `good-name_1.x` | rc 0, empty output |
| `""` | rc 1 |
| `a/b` | rc 1, message contains `path separator` |
| `a\b` | rc 1 |
| `has space` | rc 1 |
| `-lead` | rc 1 |
| `..` | rc 1 |
| `taken` | rc 1, message contains `already exists` |

Capture as `out="$(_ac_validate_new_project_name "$n")"; rc=$?` and assert both
with `assert_exit` / `assert_contains`.

**7b. Happy path through the menu [AC 1, 2, 6]**

```bash
printf 'n\nbrandnew\nn\nq\n' | ac >/dev/null 2>&1 || true
```
(keys: `n` open flow → name → `n` to decline the config offer → `q` to quit)

- `assert_eq "new-project: directory created" "1" "$( [[ -d "$NEWP_DIR/brandnew" ]] && echo 1 || echo 0 )"` (or the inline if/printf form).
- `.git` exists: `[[ -d "$NEWP_DIR/brandnew/.git" ]]`.
- The project then appears in the menu:
  `menu_out=$(echo q | ac 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')` →
  `assert_contains "new-project: appears in menu" "brandnew" "$menu_out"`.

**7c. Skipping the config step writes nothing [AC 5]** — after 7b, read
`$NEWP_CONFIG` and assert it contains no `brandnew:` key:

```bash
cfg="$(cat "$NEWP_CONFIG")"
if [[ "$cfg" != *"brandnew"* ]]; then ✓ else ✗ fi
```

**7d. Inline config persists all four fields [AC 4, 6]**

```bash
printf 'n\nstyled\ny\n1\nMy Styled\n2\n#00cc66\n3\nStyled Tab\n4\nhello there\nd\nq\n' | ac >/dev/null 2>&1 || true
```
Then assert against the **config file on disk** (proves `_ac_write_config`
ran), reading it with `cat`:

- contains `alias: "My Styled"` — check the exact quoting `_ac_yaml_quote`
  produces by inspecting the written file once during development and
  asserting on the substring that actually appears (safest:
  `assert_contains … "My Styled" "$cfg"` plus `assert_contains … "#00cc66" "$cfg"`,
  `"Styled Tab"`, `"hello there"`).
- And round-trip through memory: `_ac_load_config` then
  `assert_eq "…alias" "My Styled" "$(_ac_indirect "_AC_PROJECT_ALIAS_styled")"`
  and the same for `_AC_PROJECT_COLOR_styled` / `_TITLE_` / `_PROMPT_`.
- Menu render shows the alias instead of the dirname:
  `assert_contains "new-project: menu shows alias" "My Styled" "$menu_out"`.

**7e. Duplicate name rejected through the menu [AC 3]** — with `styled`
already created, feed `n`, `styled`, then a blank line (cancel), then `q`:

```bash
dup_out=$(printf 'n\nstyled\n\nq\n' | ac 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
assert_contains "new-project: duplicate rejected" "already exists" "$dup_out"
```
Also assert nothing was clobbered — `styled/.git` still there — and that `ac`
exited normally (the menu loop did not crash): the trailing `q` was consumed,
so the command's own exit status is 0.

**7f. Invalid name, then retry succeeds [AC 3]**

```bash
printf 'n\nbad name\nrecovered\nn\nq\n' | ac >/dev/null 2>&1 || true
```
→ `$NEWP_DIR/recovered` exists and `$NEWP_DIR/bad name` does not.

**7g. Missing/failing git does not break the flow [AC 8]** — shadow `git` in
the already-PATH-prepended `$MOCK_BIN`, and **remove it immediately after**:

```bash
cat > "$MOCK_BIN/git" << 'MOCK'
#!/usr/bin/env bash
exit 1
MOCK
chmod +x "$MOCK_BIN/git"
nogit_out=$(printf 'n\nnogitproj\nn\nq\n' | ac 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
rm -f "$MOCK_BIN/git"
```
Assert: `$NEWP_DIR/nogitproj` is a directory, `$NEWP_DIR/nogitproj/.git` is
**not**, and `nogit_out` contains `git init failed` (or whatever literal Step 3
uses — keep them in sync). Also assert the menu still rendered afterwards
(`assert_contains … "anycli-launcher" "$nogit_out"`).

> Removing the stub in the very next line is mandatory — later tests in the
> file (the self-update section, ~line 4440) run real `git init` / `git commit`
> and will fail loudly if a stub survives. If `_ac_resolve_bin` is used as
> specified, the stub is found the same way the real binary is.

**7h. Footer + help text [AC 7]**

```bash
foot=$(echo q | ac 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
assert_contains "new-project: footer advertises n" "n  new" "$foot"
help_out=$(printf '?\nq\n' | ac 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
assert_contains "new-project: ? help lists n" "new project" "$help_out"
```
(The `?` key only renders when the configured CLI has a resume or picker flag —
`kiro-cli` does, which is why the block's setup pins `_AC_CLI_TOOL="kiro-cli"`.)

**7i. `n` as a filter prefix still filters** — regression guard for §2.2:
with a project named `nodeapp` present, `printf 'node\nq\n' | ac` must show the
filter banner and still list `nodeapp`, i.e. the `n` intercept did not swallow
multi-character input starting with `n`.

Teardown at the end of the block:

```bash
rm -rf "$NEWP_DIR" "$NEWP_CONFIG"
unset AC_PROJECTS_DIR
```
(`AC_PROJECTS_DIR` is re-exported by later code and finally unset in the file's
cleanup section; unsetting here matches how other blocks end.)

### Step 8 — Docs

**File:** `README.md`

1. The sample menu block near the top (~line 22): change
   `   c  config    q  quit` to `   c  config    n  new    q  quit`.
2. The Usage key table (~lines 245–256): add a row between the `v` and `?`
   rows:
   `| n | Create a new project folder in the projects root (`mkdir` + `git init`), then optionally set its alias/color/title/prompt |`

**File:** `CHANGELOG.md`

Add at the top, above `## 3.1.0`:

```markdown
## Unreleased

### Added
- **New project from the menu.** Press `n` on the main menu to create a
  project folder under your projects root, `git init` it, and optionally set
  its alias, tab color, tab title and initial prompt right away. The folder is
  always created directly in `projects_dir` (extra dirs are never targets), and
  a missing or failing `git` leaves the folder in place rather than aborting.
```

Do not touch `VERSION`.

### Step 9 — Full verification

Run the commands in §5 and fix anything that falls out.

---

## 4. Data / model / API changes

**None.** No new config keys, no schema change, no migration.

The inline setup step writes the four *existing* per-project keys —
`alias`, `color`, `title`, `prompt` — under the existing `projects:` map in
`~/.config/anycli-launcher/config.yaml`, emitted by the existing
`_ac_write_config` and parsed by the existing `_ac_parse_yaml`. In memory they
live in the existing shell variables
`_AC_PROJECT_{ALIAS,COLOR,TITLE,PROMPT}_<safe>` where `<safe>` is
`_ac_safe_name "<project>"`, all of which are already listed in
`_AC_PROJ_PROP_PREFIXES`. A config file written before this change is read
identically after it, and vice versa.

The only new "interface" is the `n` menu key and the three new internal
functions (§2.1).

---

## 5. Testing & verification

### Commands

```bash
cd /home/eric/projects/anycli-launcher
bash tests/test-bash.sh          # fast loop while developing
./tests/run-tests.sh             # shellcheck lint gate + full suite (the gate)
shellcheck -s bash anycli-launcher.bash   # AC 10, standalone
./tests/check-platform.sh        # optional sanity check on this machine
```

Manual smoke test (uses a throwaway root, leaves your real config alone):

```bash
tmproot=$(mktemp -d); tmpcfg=$(mktemp)
( source ./anycli-launcher.bash
  _AC_CONFIG_FILE="$tmpcfg"; export AC_PROJECTS_DIR="$tmproot"
  _AC_CLI_TOOL=kiro-cli; _AC_AUTO_UPDATE_CHECK=false
  ac )
# press n, type a name, answer the config offer, then q
ls -a "$tmproot"/*; cat "$tmpcfg"
rm -rf "$tmproot" "$tmpcfg"
```

### Acceptance-criteria map

| AC | Proven by |
|---|---|
| 1 — `n` prompts for a name | Test 7b (the flow only proceeds if the prompt consumed the name line) + manual smoke |
| 2 — creates dir + `.git` | Test 7b |
| 3 — invalid/duplicate name errors, retries, creates nothing, no crash | Tests 7a (unit), 7e (duplicate via menu), 7f (retry) |
| 4 — inline config persists and reads back | Test 7d (file contents + `_ac_load_config` round-trip + menu alias) |
| 5 — skipping leaves folder, no config entries | Test 7c |
| 6 — new project appears in the refreshed menu | Tests 7b and 7d (menu render assertions) |
| 7 — key listed in the footer | Test 7h |
| 8 — git missing/failing does not crash | Test 7g |
| 9 — existing suite passes, new tests added | `./tests/run-tests.sh` green; all of §3 Step 7 |
| 10 — shellcheck clean | `./tests/run-tests.sh` lint gate / standalone `shellcheck` |

---

## 6. Risks & watch-outs

1. **Intercept ordering (highest risk).** If the `n` block lands below the
   `if [[ -n "$key" && ! "$key" =~ ^[0-9]+$ ]]` catch-all, pressing `n` will
   silently set the filter to `n` instead of opening the flow, and every test
   in 7b–7g will fail confusingly (the piped name line then becomes the next
   filter). Place it with the other single-letter keys.

2. **Exact-match, not prefix.** `[[ "$key" == n* ]]` would break filtering for
   every word starting with `n`. Test 7i guards this.

3. **`_ac_pause` eats a line over a pipe.** Its non-tty branch is a plain
   `read -r`, so every `_ac_pause` in the flow consumes one line of piped test
   input. Keep `_ac_pause` out of the retry loop (§2.4) and count the lines in
   each test's `printf` against every `read` the flow performs — the most
   common cause of a mystifying test failure here.

4. **`err_msg="$(cmd)"` carries `cmd`'s exit status** — that is deliberate and
   the whole retry loop depends on it. Don't "clean it up" into
   `err_msg=$(...); if [[ -n "$err_msg" ]]` (works, but then an empty-message
   failure mode silently passes) or into a `local err_msg="$(...)"` declaration,
   where `local` masks the status (this is exactly the SC2155 class the repo
   suppresses — a *declaration* would swallow the return code here and break the
   logic, so declare `err_msg` on its own line first).

5. **Register before writing config.** Without `_ac_register_project "$name"`,
   `_ac_write_config` falls back to `_ac_decode_name` for the YAML key. It
   usually round-trips, but for anything non-alphanumeric this is the exact
   class of bug the registry was introduced to fix. Register right after
   `mkdir`.

6. **Never use a fixture config in the new tests.** The flow calls
   `_ac_write_config`, which *overwrites* `$_AC_CONFIG_FILE`. Pointing it at
   `tests/fixtures/config.yaml` would rewrite a tracked file and cascade
   failures into every later test. Always `mktemp`.

7. **Remove the `git` stub immediately** after test 7g — later tests
   (self-update, ~line 4440 of `tests/test-bash.sh`) run real `git`.

8. **`git init -q` still writes to stderr** (default-branch hint on git ≥ 2.28).
   Redirect `>/dev/null 2>&1` or the hint will leak into the menu render and
   into `assert_contains` haystacks.

9. **shellcheck strictness.** The `.shellcheckrc` disables only SC2016, SC2155,
   SC2178, SC2034, SC2206, SC1003, SC2059. Everything else is live — notably
   SC2086 (quote every expansion, including `"$dir"` and `"$name"`) and SC2181.
   Do not add new `disable=` lines; write code that passes.

10. **zsh compatibility.** `ac()` runs under `emulate -L bash -o bash_rematch`
    when sourced from zsh, so bash-flavored `[[ … =~ … ]]` and 0-based arrays
    are fine inside the call tree. Use `_ac_assign` rather than `printf -v` if
    you ever need dynamic assignment (you shouldn't — `_ac_edit_style_field`
    handles it), and `_ac_resolve_bin` rather than `type -P`.

11. **Git Bash / Windows.** The backslash and character-allowlist rules (§2.3)
    exist for this. `_ac_normalize_typed_path` is *not* used — this flow takes a
    bare name, never a path — so there is no `~`/drive-letter handling to worry
    about. Do not add any.

12. **`scan_depth: 2` interaction.** A freshly `git init`-ed folder has `.git`,
    so `_ac_collect_projects` lists it as a project even under `scan_depth 2`.
    If `git init` *failed*, the empty folder has no `.git` and no children, so
    the `kids` array is empty and the code falls through to listing the folder
    itself — still correct. No change needed, but don't be surprised by the
    branch.

13. **The `mkdir` failure path must `return 0`,** not a non-zero status: the
    caller does `_ac_new_project; continue`, and a stray `set -e`-ish habit
    (`|| return 1`) would be a no-op here but is still misleading. Keep the menu
    loop alive on every failure path.

---

## 7. Out of scope — do not build

- **Any root other than `_AC_PROJECTS_DIR`.** No multi-root picker, no
  `_AC_EXTRA_DIRS` target, even when extra dirs are configured.
- **Any scaffolding beyond `mkdir` + `git init`** — no README, `.gitignore`,
  license, initial commit, `git branch -M main`, template files, or
  language-specific init. A bare empty repo, nothing more.
- **Renaming, deleting or moving existing projects.**
- **A non-interactive `ac new <name>` subcommand.** Considered and
  deliberately declined (§2.1). Mention it in the PR description as "considered,
  not built" if a PR is opened; do not add a `TODO` in the source.
- **Changing `scan_depth 2` grouping behaviour** in `_ac_collect_projects`.
- **Refactoring `_ac_config_edit_project_style` or
  `_ac_config_edit_project_aliases`.** They keep their current signatures and
  behaviour; only the leaf helper `_ac_edit_style_field` is shared.
- **Bumping `VERSION`** or cutting a release.
- **Committing or pushing.** Leave the change in the working tree unless the
  user explicitly asks for a commit/PR.
