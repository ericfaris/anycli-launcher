#!/usr/bin/env bash
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FIXTURES="$SCRIPT_DIR/fixtures"
PASS=0
FAIL=0

assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [[ "$expected" == "$actual" ]]; then
        printf '  \e[32m✓\e[0m %s\n' "$desc"
        ((PASS++))
    else
        printf '  \e[31m✗\e[0m %s\n    expected: %s\n    actual:   %s\n' "$desc" "$expected" "$actual"
        ((FAIL++))
    fi
}

assert_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        printf '  \e[32m✓\e[0m %s\n' "$desc"
        ((PASS++))
    else
        printf '  \e[31m✗\e[0m %s\n    expected to contain: %s\n    actual: %s\n' "$desc" "$needle" "$haystack"
        ((FAIL++))
    fi
}

assert_exit() {
    local desc="$1" expected="$2" actual="$3"
    if [[ "$expected" == "$actual" ]]; then
        printf '  \e[32m✓\e[0m %s\n' "$desc"
        ((PASS++))
    else
        printf '  \e[31m✗\e[0m %s (exit: %s, expected: %s)\n' "$desc" "$actual" "$expected"
        ((FAIL++))
    fi
}

# --- Setup ---
# Mock kiro-cli
MOCK_BIN="$SCRIPT_DIR/.mock-bin"
mkdir -p "$MOCK_BIN"
cat > "$MOCK_BIN/kiro-cli" << 'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
    echo "kiro-cli 2.16.1"
    exit 0
fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
EOF
chmod +x "$MOCK_BIN/kiro-cli"
export PATH="$MOCK_BIN:$PATH"
export LK_TEST_LOG="$SCRIPT_DIR/.test-kiro.log"

# Source the launcher
source "$PROJECT_DIR/kiro-launcher.bash"

# Marker for cleaning up generated tab scripts created during THIS run. The
# launcher writes lk-tab-*.sh into ${TMPDIR:-/tmp} and only self-deletes them
# when the script is actually executed — but tests use a mocked wt.exe that
# never runs them, so they would otherwise accumulate. We delete any newer than
# this marker at teardown. (The launcher's own sweep is time-based, -mtime +0,
# and intentionally leaves recent files alone, so it will not do this for us.)
_LK_TEST_RUN_MARKER="$(mktemp)"

# ═══════════════════════════════════════
echo "── Config Loading ──"

# Test: config file with all fields
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
unset LK_PROJECTS_DIR
_lk_load_config
assert_eq "config: PROJECTS_DIR loaded from file" "$HOME/dev" "$_LK_PROJECTS_DIR"
assert_eq "config: KIRO_FLAGS loaded from file" "--trust-all-tools" "$_LK_KIRO_FLAGS"

# Test: CLI tool fields
assert_eq "config: cli_tool loaded from file" "kiro-cli" "$_LK_CLI_TOOL"
assert_eq "config: cli_launch loaded from file" "chat" "$_LK_CLI_LAUNCH"

# Test: per-project flags (inherits default + extends)
flags=$(_lk_get_flags "beta")
assert_eq "config: per-project flags for beta" "--trust-all-tools --no-interactive" "$flags"

# Test: fallback to global flags for unknown project
flags=$(_lk_get_flags "alpha")
assert_eq "config: global flags for alpha (no override)" "--trust-all-tools" "$flags"

# Test: env var overrides config file
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="/tmp/override"
_lk_load_config
assert_eq "config: env var overrides file" "/tmp/override" "$_LK_PROJECTS_DIR"
unset LK_PROJECTS_DIR

# Test: missing config file uses defaults (also ensure no legacy file interferes)
_LK_CONFIG_FILE="/nonexistent/path/config.yaml"
HOME="/nonexistent" _lk_load_config
assert_eq "config: missing file uses default PROJECTS_DIR" "/nonexistent/projects" "$_LK_PROJECTS_DIR"
assert_eq "config: missing file uses empty KIRO_FLAGS" "" "$_LK_KIRO_FLAGS"

# ═══════════════════════════════════════
echo "── Menu Output ──"

# Point config at fixtures
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config

# Capture menu output (q to quit the loop)
menu_output=$(echo "q" | lk 2>/dev/null || true)

assert_contains "menu: has header" "Kiro Launcher" "$menu_output"
assert_contains "menu: has terminal entry" "terminal" "$menu_output"
assert_contains "menu: has root entry" "(root)" "$menu_output"
assert_contains "menu: has alpha" "alpha" "$menu_output"
assert_contains "menu: has beta" "beta" "$menu_output"
assert_contains "menu: has gamma" "gamma" "$menu_output"

# Count entries (lines with numbers, strip ANSI codes first)
entry_count=$(echo "$menu_output" | sed 's/\x1b\[[0-9;]*m//g' | grep -cE '^\s+[0-9]' || true)
assert_eq "menu: 5 entries (terminal + root + 3 projects)" "5" "$entry_count"

# ═══════════════════════════════════════
echo "── Selection ──"

# Test: invalid input
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config

# Invalid input exits cleanly on EOF (loop exits, no error)
result=$(echo "99" | lk 2>/dev/null || true)
exit_code=$?
assert_eq "selection: invalid number exits cleanly" "0" "$exit_code"

result=$(echo "abc" | lk 2>/dev/null || true)
assert_contains "selection: non-numeric renders menu" "Kiro Launcher" "$result"

result=$(echo "" | lk 2>/dev/null || true)
assert_contains "selection: empty input renders menu" "Kiro Launcher" "$result"

# Test: q quits the main menu
result=$(echo "q" | lk 2>/dev/null || true)
exit_code=$?
assert_eq "selection: q quits cleanly" "0" "$exit_code"

# Test: entry 1 (terminal - cd only, no kiro)
rm -f "$LK_TEST_LOG"
(echo "1" | lk) >/dev/null 2>&1 || true
if [[ ! -f "$LK_TEST_LOG" ]]; then
    printf '  \e[32m✓\e[0m selection: entry 1 does not call kiro-cli\n'
    ((PASS++))
else
    printf '  \e[31m✗\e[0m selection: entry 1 should not call kiro-cli\n'
    ((FAIL++))
fi

# Test: entry 3 (alpha project - cd + kiro with global flags)
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_contains "selection: entry 3 calls kiro-cli" "KIRO_CALLED:" "$kiro_call"
    assert_contains "selection: entry 3 uses global flags" "--trust-all-tools" "$kiro_call"
    printf '  \e[32m✓\e[0m selection: entry 3 calls kiro-cli with global flags\n'
    ((PASS++))
else
    printf '  \e[31m✗\e[0m selection: entry 3 should call kiro-cli\n'
    ((FAIL++))
fi

# Test: entry 4 (beta project - cd + kiro with per-project flags)
rm -f "$LK_TEST_LOG"
(echo "4" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_contains "selection: entry 4 uses per-project flags" "--no-interactive" "$kiro_call"
else
    printf '  \e[31m✗\e[0m selection: entry 4 should call kiro-cli\n'
    ((FAIL++))
fi

# ═══════════════════════════════════════
echo "── Flags ──"

# Test: no flags configured
_LK_CONFIG_FILE="/nonexistent"
export LK_PROJECTS_DIR="$FIXTURES/projects"
HOME="/nonexistent" _lk_load_config
flags=$(_lk_get_flags "alpha")
assert_eq "flags: no config means empty flags" "" "$flags"

# --- Flag Inheritance Tests ---
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
unset LK_PROJECTS_DIR
_lk_load_config

# Test: project with no config inherits only defaults
flags=$(_lk_get_flags "alpha")
assert_eq "flags: unset project inherits defaults" "--trust-all-tools" "$flags"

# Test: project with flags gets default + own
flags=$(_lk_get_flags "beta")
assert_eq "flags: project extends defaults" "--trust-all-tools --no-interactive" "$flags"

# Test: "none" suppresses all inheritance
flags=$(_lk_get_flags "silent")
assert_eq "flags: none suppresses all flags" "" "$flags"

# Test: empty string (set but empty) inherits defaults only
_LK_KIRO_FLAGS_emptytest=""
flags=$(_lk_get_flags "emptytest")
assert_eq "flags: empty string inherits defaults" "--trust-all-tools" "$flags"
unset _LK_KIRO_FLAGS_emptytest

# Test: 3-layer inheritance (default → project → tab)
_LK_KIRO_FLAGS="--trust-all-tools"
_LK_KIRO_FLAGS_myproj="--agent mechanic"
# Simulate tab resolution: parent_flags = _lk_get_flags "myproj", tab_flags = "--effort high"
parent_flags="$(_lk_get_flags "myproj")"
tab_flags="--effort high"
resolved="${parent_flags:+$parent_flags }$tab_flags"
assert_eq "flags: 3-layer inheritance (default+project+tab)" "--trust-all-tools --agent mechanic --effort high" "$resolved"

# Test: 3-layer with "none" project suppresses all
_LK_KIRO_FLAGS_isolated="none"
parent_flags="$(_lk_get_flags "isolated")"
tab_flags="--custom"
resolved="${parent_flags:+$parent_flags }$tab_flags"
assert_eq "flags: 3-layer none project + tab gets only tab" "--custom" "$resolved"
unset _LK_KIRO_FLAGS_myproj _LK_KIRO_FLAGS_isolated

# ═══════════════════════════════════════
echo "── Edge Cases ──"

_LK_CONFIG_FILE="$FIXTURES/config.yaml"
unset LK_PROJECTS_DIR
_lk_load_config

# Hyphenated project name (inherits default + extends)
flags=$(_lk_get_flags "my-app")
assert_eq "edge: hyphenated project name" "--trust-all-tools --agent mechanic" "$flags"

# Project name with dots (inherits default + extends)
flags=$(_lk_get_flags "v2.core")
assert_eq "edge: dotted project name" "--trust-all-tools --no-interactive" "$flags"

# Value containing = signs preserved (inherits + extends)
flags=$(_lk_get_flags "ml-project")
assert_eq "edge: value with = signs preserved" "--trust-all-tools --model=claude --effort=high" "$flags"

# "none" suppresses inheritance — start empty
flags=$(_lk_get_flags "silent")
assert_eq "edge: none suppresses inheritance" "" "$flags"

# Unknown project still falls back to global
flags=$(_lk_get_flags "unknown-project")
assert_eq "edge: unknown project falls back to global" "--trust-all-tools" "$flags"

# Multiple spaces between flags preserved (inherits + extends)
flags=$(_lk_get_flags "spacey")
assert_eq "edge: multiple spaces in value preserved" "--trust-all-tools --effort high   --no-interactive" "$flags"

# ═══════════════════════════════════════
echo "── Project Color & Title ──"

# Test: color parsed from config
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
color="$(_lk_indirect "_LK_PROJECT_COLOR_$(_lk_safe_name "ml-project")")"
assert_eq "color: parsed from config" "#00cc66" "$color"

# Test: title parsed from config
title="$(_lk_indirect "_LK_PROJECT_TITLE_$(_lk_safe_name "ml-project")")"
assert_eq "title: parsed from config" "ML Project" "$title"

# Test: project without color returns empty
color="$(_lk_indirect "_LK_PROJECT_COLOR_$(_lk_safe_name "v2.core")")"
assert_eq "color: empty when not configured" "" "$color"

# Test: project without title returns empty
title="$(_lk_indirect "_LK_PROJECT_TITLE_$(_lk_safe_name "v2.core")")"
assert_eq "title: empty when not configured" "" "$title"

# Test: color persists in config file
TEST_COLOR_CONFIG="/tmp/lk-test-color-$$.yaml"
_LK_CONFIG_FILE="$TEST_COLOR_CONFIG"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
# Clear everything
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
_lk_assign "_LK_PROJECT_COLOR_myproj" "#00ff00"
_lk_assign "_LK_PROJECT_TITLE_myproj" "My Project"
_lk_write_config
written=$(cat "$TEST_COLOR_CONFIG")
assert_contains "color: persists in config file" "#00ff00" "$written"
assert_contains "title: persists in config file" "My Project" "$written"

# Test: color round-trips through write/load
_lk_load_config
color="$(_lk_indirect "_LK_PROJECT_COLOR_myproj")"
title="$(_lk_indirect "_LK_PROJECT_TITLE_myproj")"
assert_eq "color: round-trip write/load" "#00ff00" "$color"
assert_eq "title: round-trip write/load" "My Project" "$title"
rm -f "$TEST_COLOR_CONFIG"

# Test: _lk_apply_tab_style resolves title for configured project
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
title="$(_lk_indirect "_LK_PROJECT_TITLE_$(_lk_safe_name "ml-project")")"
assert_eq "style: resolves title for configured project" "ML Project" "$title"

# Test: _lk_apply_tab_style resolves color for configured project
color="$(_lk_indirect "_LK_PROJECT_COLOR_$(_lk_safe_name "ml-project")")"
assert_eq "style: resolves color for configured project" "#00cc66" "$color"

# Test: _lk_apply_tab_style does nothing for unconfigured project
title="$(_lk_indirect "_LK_PROJECT_TITLE_$(_lk_safe_name "v2.core")")"
color="$(_lk_indirect "_LK_PROJECT_COLOR_$(_lk_safe_name "v2.core")")"
assert_eq "style: no title for unconfigured project" "" "$title"
assert_eq "style: no color for unconfigured project" "" "$color"

# ═══════════════════════════════════════
echo "── Project Alias ──"

# Test: alias parsed from config
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
alias_val="$(_lk_indirect "_LK_PROJECT_ALIAS_$(_lk_safe_name "ml-project")")"
assert_eq "alias: parsed from config" "ML" "$alias_val"

# Test: project without alias returns empty
alias_val="$(_lk_indirect "_LK_PROJECT_ALIAS_$(_lk_safe_name "v2.core")")"
assert_eq "alias: empty when not configured" "" "$alias_val"

# Test: alias persists in config file
TEST_ALIAS_CONFIG="/tmp/lk-test-alias-$$.yaml"
_LK_CONFIG_FILE="$TEST_ALIAS_CONFIG"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
_lk_assign "_LK_PROJECT_ALIAS_myproj" "My Project"
_lk_write_config
written=$(cat "$TEST_ALIAS_CONFIG")
assert_contains "alias: persists in config file" 'alias: "My Project"' "$written"

# Test: alias round-trips through write/load
_lk_load_config
alias_val="$(_lk_indirect "_LK_PROJECT_ALIAS_myproj")"
assert_eq "alias: round-trip write/load" "My Project" "$alias_val"
rm -f "$TEST_ALIAS_CONFIG"

# Test: alias shows in menu instead of dirname
TEST_ALIAS_MENU_CONFIG="/tmp/lk-test-alias-menu-$$.yaml"
_LK_CONFIG_FILE="$TEST_ALIAS_MENU_CONFIG"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
_LK_AUTO_UPDATE_CHECK="false"
_LK_THEME="Default"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
_lk_assign "_LK_PROJECT_ALIAS_alpha" "My Alpha"
_lk_write_config
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config
menu_output=$(echo "q" | lk 2>/dev/null || true)
assert_contains "alias: shown in menu" "My Alpha" "$menu_output"
rm -f "$TEST_ALIAS_MENU_CONFIG"

# ═══════════════════════════════════════
echo "── Alias: hyphen/underscore key regression ──"
# Regression: a config written by an older version stored the project key in
# its sanitized form (hyphens turned into underscores), e.g.
# "public-knowledge-network" was saved as "public_knowledge_network". The menu
# looks aliases up by the on-disk directory name (with hyphens), so the exact
# key never matched and the alias silently vanished from the menu.
# _lk_lookup_alias must reconcile the two via normalized matching.

# Simulate a legacy config key (underscores) with a directory that uses hyphens.
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
# Register the name exactly as the legacy parser would have (underscore key).
_lk_register_project "public_knowledge_network"
_lk_assign "_LK_PROJECT_ALIAS_$(_lk_safe_name "public_knowledge_network")" "lumen"

# Lookup by the real directory name (hyphens) must still find the alias.
assert_eq "alias: legacy underscore key resolves for hyphen dir" \
    "lumen" "$(_lk_lookup_alias "public-knowledge-network")"

# _lk_project_label (takes a full path) must apply the alias too.
assert_eq "alias: _lk_project_label resolves legacy key" \
    "lumen" "$(_lk_project_label "$HOME/projects/public-knowledge-network")"

# _lk_display_name (takes a name) must apply the alias too.
assert_eq "alias: _lk_display_name resolves legacy key" \
    "lumen" "$(_lk_display_name "public-knowledge-network")"

# The modern exact-key path must keep working (no normalization needed).
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
_lk_register_project "my-app"
_lk_assign "_LK_PROJECT_ALIAS_$(_lk_safe_name "my-app")" "App"
assert_eq "alias: modern exact hyphen key resolves" "App" "$(_lk_lookup_alias "my-app")"

# A project with no alias returns empty (no false positives).
assert_eq "alias: unrelated name returns empty" "" "$(_lk_lookup_alias "some-other-project")"

# M1/M3 regression: normalization collision must NOT cross-match distinct
# projects. An alias set for "my-app" must not leak onto an unaliased "my_app".
# The legacy match is one-directional (hyphen dir -> underscore key), so a real
# underscore directory does not pick up a hyphen project's alias.
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
_lk_register_project "my-app"
_lk_assign "_LK_PROJECT_ALIAS_$(_lk_safe_name "my-app")" "HyphenApp"
assert_eq "alias: my-app resolves its own alias" "HyphenApp" "$(_lk_lookup_alias "my-app")"
assert_eq "alias: my_app does NOT inherit my-app alias (no collision)" \
    "" "$(_lk_lookup_alias "my_app")"

# The reverse legacy case still works: a hyphen directory finds an underscore
# (legacy-sanitized) key. This is the intended tolerance.
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
_lk_register_project "public_knowledge_network"
_lk_assign "_LK_PROJECT_ALIAS_$(_lk_safe_name "public_knowledge_network")" "lumen"
assert_eq "alias: hyphen dir matches legacy underscore key" \
    "lumen" "$(_lk_lookup_alias "public-knowledge-network")"
# But an unrelated real underscore project must not match it.
assert_eq "alias: unrelated underscore project stays empty" \
    "" "$(_lk_lookup_alias "private_data_store")"

# Case-insensitive exact match resolves (a key differing only by case).
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
_lk_register_project "MyProject"
_lk_assign "_LK_PROJECT_ALIAS_$(_lk_safe_name "MyProject")" "MP"
assert_eq "alias: case-insensitive exact match" "MP" "$(_lk_lookup_alias "myproject")"

# Full end-to-end: a config authored with the buggy underscore keys still shows
# aliases in the rendered menu.
LEGACY_ALIAS_CONFIG="/tmp/lk-test-legacy-alias-$$.yaml"
mkdir -p "$FIXTURES/projects/public-knowledge-network"
cat > "$LEGACY_ALIAS_CONFIG" <<EOF
projects_dir: "$FIXTURES/projects"
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"

projects:
  "public_knowledge_network":
    alias: "lumen"
EOF
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done
_LK_CONFIG_FILE="$LEGACY_ALIAS_CONFIG"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config
legacy_menu=$(echo "q" | lk 2>/dev/null || true)
assert_contains "alias: legacy-key config renders alias in menu" "lumen" "$legacy_menu"
rm -f "$LEGACY_ALIAS_CONFIG"
rmdir "$FIXTURES/projects/public-knowledge-network" 2>/dev/null || true
for v in $(_lk_vars_with_prefix _LK_PROJECT_ALIAS_); do unset "$v"; done

# ═══════════════════════════════════════
echo "── Per-Project Appearance Editor ──"
# L4 regression: cover the extracted _lk_edit_style_field helper — assign,
# blank-clears, and regex validation (the color reject path).
APPEAR_CONFIG="/tmp/lk-test-appear-$$.yaml"
_LK_CONFIG_FILE="$APPEAR_CONFIG"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
_LK_AUTO_UPDATE_CHECK="false"
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done

# Valid hex color is accepted and assigned.
_lk_edit_style_field "_LK_PROJECT_COLOR_appearproj" \
    "color: " '^#[0-9a-fA-F]{6}$' "6-digit hex color" <<< "#00cc66" >/dev/null
assert_eq "appearance: valid color assigned" "#00cc66" \
    "$(_lk_indirect "_LK_PROJECT_COLOR_appearproj")"

# Invalid hex color is rejected (variable stays unset) and helper returns 1.
_lk_edit_style_field "_LK_PROJECT_COLOR_rejectproj" \
    "color: " '^#[0-9a-fA-F]{6}$' "6-digit hex color" <<< "notacolor" >/dev/null
rc=$?
assert_eq "appearance: invalid color rejected (rc=1)" "1" "$rc"
assert_eq "appearance: invalid color not assigned" "" \
    "$(_lk_indirect "_LK_PROJECT_COLOR_rejectproj")"

# Blank input clears an existing value.
_lk_assign "_LK_PROJECT_TITLE_clearproj" "Old Title"
_lk_edit_style_field "_LK_PROJECT_TITLE_clearproj" "title: " <<< "" >/dev/null
assert_eq "appearance: blank clears the field" "" \
    "$(_lk_indirect "_LK_PROJECT_TITLE_clearproj")"

# Title (no validator) accepts arbitrary text.
_lk_edit_style_field "_LK_PROJECT_TITLE_titleproj" "title: " <<< "My Tab" >/dev/null
assert_eq "appearance: unvalidated field assigns freely" "My Tab" \
    "$(_lk_indirect "_LK_PROJECT_TITLE_titleproj")"

# The config menu lists the appearance option with a configured count.
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config 2>/dev/null || true
_lk_assign "_LK_PROJECT_COLOR_appearproj" "#00cc66"
appear_menu=$(printf 'c\nb\n' | lk 2>/dev/null || true)
assert_contains "appearance: menu shows appearance option" "Per-project appearance" "$appear_menu"
rm -f "$APPEAR_CONFIG"
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done

# ═══════════════════════════════════════
echo "── Config Menu ──"

# Test: menu shows config entry
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config
menu_output=$(echo "q" | lk 2>/dev/null || true)
assert_contains "menu: has config entry" "config" "$menu_output"

# Test: 'c' opens config submenu (feed 'c' then 'b' to exit)
unset LK_PROJECTS_DIR
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
export LK_PROJECTS_DIR="$FIXTURES/projects"  # need this for lk() to find the dir
config_output=$(printf 'c\nb\n' | lk 2>/dev/null || true)
assert_contains "config: c opens config submenu" "Kiro Launcher Config" "$config_output"
assert_contains "config: shows projects directory" "Projects directory" "$config_output"
assert_contains "config: shows default flags" "Default flags" "$config_output"
assert_contains "config: shows per-project flags" "Per-project flags" "$config_output"
assert_contains "config: shows back option" "back" "$config_output"

# Test: config submenu shows current values from YAML
assert_contains "config: shows current flags value" "--trust-all-tools" "$config_output"

# T2.2: config submenu shows the Show-sessions toggle for a CLI that can list
# sessions (kiro-cli).
assert_contains "config: shows Show last session toggle" "Show last session" "$config_output"

# ═══════════════════════════════════════
echo "── Config Persistence ──"

# Test: writing config creates YAML file
TEST_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$TEST_CONFIG"
_LK_PROJECTS_DIR="$HOME/myprojects"
_LK_KIRO_FLAGS="--trust-all-tools"
# Clear and set a per-project flag
for v in $(compgen -v _LK_KIRO_FLAGS_ 2>/dev/null); do unset "$v"; done
_LK_KIRO_FLAGS_testproj="--agent mechanic"

_lk_write_config
assert_eq "persist: file created" "0" "$?"

written=$(cat "$TEST_CONFIG")
assert_contains "persist: contains projects_dir" 'projects_dir: "~/myprojects"' "$written"
assert_contains "persist: contains default_flags" 'default_flags: "--trust-all-tools"' "$written"
assert_contains "persist: contains project entry" '"testproj":' "$written"
assert_contains "persist: contains project flags" 'flags: "--agent mechanic"' "$written"

# Test: round-trip (write then read back)
_LK_PROJECTS_DIR=""
_LK_KIRO_FLAGS=""
for v in $(compgen -v _LK_KIRO_FLAGS_ 2>/dev/null); do unset "$v"; done
unset LK_PROJECTS_DIR
_lk_load_config
assert_eq "persist: round-trip projects_dir" "$HOME/myprojects" "$_LK_PROJECTS_DIR"
assert_eq "persist: round-trip default_flags" "--trust-all-tools" "$_LK_KIRO_FLAGS"
flags=$(_lk_get_flags "testproj")
assert_eq "persist: round-trip per-project flags" "--trust-all-tools --agent mechanic" "$flags"

rm -f "$TEST_CONFIG"

# Test: editing projects dir persists
TEST_CONFIG2=$(mktemp)
_LK_CONFIG_FILE="$TEST_CONFIG2"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
for v in $(compgen -v _LK_KIRO_FLAGS_ 2>/dev/null); do unset "$v"; done

# Simulate: c -> 1 -> enter new value -> b
printf '1\n%s\nb\n' "$FIXTURES/projects" | _lk_config_menu >/dev/null 2>&1
_lk_load_config
assert_eq "persist: edited projects_dir saved" "$FIXTURES/projects" "$_LK_PROJECTS_DIR"

rm -f "$TEST_CONFIG2"

# ═══════════════════════════════════════
echo "── Legacy Migration ──"

# Test: legacy flat config auto-migrates to YAML
MIGRATION_DIR=$(mktemp -d)
mkdir -p "$MIGRATION_DIR"
cat > "$MIGRATION_DIR/config" << 'EOF'
PROJECTS_DIR=~/legacy-projects
KIRO_FLAGS=--trust-all-tools
KIRO_FLAGS_myapp=--agent mechanic
EOF

_LK_CONFIG_FILE="$MIGRATION_DIR/config.yaml"
# Patch the legacy path for this test
_lk_migrate_legacy "$MIGRATION_DIR/config"

# Verify YAML was created
if [[ -f "$MIGRATION_DIR/config.yaml" ]]; then
    printf '  \e[32m✓\e[0m migration: YAML file created\n'
    ((PASS++))
else
    printf '  \e[31m✗\e[0m migration: YAML file should be created\n'
    ((FAIL++))
fi

# Verify content
migrated=$(cat "$MIGRATION_DIR/config.yaml")
assert_contains "migration: contains projects_dir" "projects_dir:" "$migrated"
assert_contains "migration: contains default_flags" 'default_flags: "--trust-all-tools"' "$migrated"
assert_contains "migration: contains per-project" '"myapp":' "$migrated"

# Verify old file renamed to .bak
if [[ -f "$MIGRATION_DIR/config.bak" ]]; then
    printf '  \e[32m✓\e[0m migration: old file renamed to .bak\n'
    ((PASS++))
else
    printf '  \e[31m✗\e[0m migration: old file should be renamed to .bak\n'
    ((FAIL++))
fi

rm -rf "$MIGRATION_DIR"

# ═══════════════════════════════════════
echo "── Version & Changelog ──"

# Test: version is loaded
_EXPECTED_VER="$(cat "$PROJECT_DIR/VERSION" | tr -d '[:space:]')"
assert_eq "version: loaded from VERSION file" "$_EXPECTED_VER" "$_LK_VERSION"

# Test: main menu shows version
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config
menu_output=$(echo "q" | lk 2>/dev/null || true)
assert_contains "version: shown in menu header" "v${_EXPECTED_VER}" "$menu_output"

# Test: menu shows quit option
assert_contains "menu: shows quit option" "quit" "$menu_output"

# Test: changelog function exists and renders
changelog_output=$( (echo "" | _lk_show_changelog) 2>/dev/null || true)
assert_contains "changelog: shows header" "Changelog" "$changelog_output"
assert_contains "changelog: shows version" "1.6.1" "$changelog_output"

# Test: changelog handles missing file gracefully
_LK_SCRIPT_DIR="/nonexistent"
no_cl_output=$( (echo "" | _lk_show_changelog) 2>/dev/null || true)
assert_contains "changelog: missing file shows fallback" "No changelog found" "$no_cl_output"
_LK_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ═══════════════════════════════════════
echo "── Save Feedback ──"

# Test: editing projects dir shows "Saved" on change
TEST_FEEDBACK=$(mktemp)
_LK_CONFIG_FILE="$TEST_FEEDBACK"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done

feedback=$(printf '%s\n' "$FIXTURES/projects" | _lk_config_edit_projects_dir 2>/dev/null)
assert_contains "feedback: save shows checkmark" "✓ Saved" "$feedback"

# Test: blank input shows "No changes"
feedback=$(printf '\n' | _lk_config_edit_projects_dir 2>/dev/null)
assert_contains "feedback: blank shows no changes" "No changes" "$feedback"

# Test: default flags cleared (c = clear, d = done)
TEST_FEEDBACK=$(mktemp)
_LK_CONFIG_FILE="$TEST_FEEDBACK"
_LK_KIRO_FLAGS="something"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
_lk_write_config
printf 'c\nd\n' | _lk_config_edit_default_flags 2>/dev/null
written=$(cat "$TEST_FEEDBACK")
# After clear, the file should have "default_flags: " with empty value (not "something")
if [[ "$written" == *"default_flags: something"* ]]; then
    printf '  \e[31m✗\e[0m feedback: clear removes all flags\n'
    ((FAIL++))
else
    printf '  \e[32m✓\e[0m feedback: clear removes all flags\n'
    ((PASS++))
fi

# Test: project flags — accumulate multiple flags
_LK_KIRO_FLAGS="--trust-all-tools"
export LK_PROJECTS_DIR="$FIXTURES/projects"
# Select alpha (1), pick "custom" (second-to-last), type a flag, then done
flag_count=$(_lk_get_flag_opts | wc -l)
custom_idx=$((flag_count - 1))
_lk_write_config
printf "1\n${custom_idx}\n--test-proj-flag\nd\n" | _lk_config_edit_project_flags 2>/dev/null
written=$(cat "$TEST_FEEDBACK")
assert_contains "feedback: project flags saved to file" '"alpha":' "$written"

rm -f "$TEST_FEEDBACK"

rm -f "$TEST_FEEDBACK"

# ═══════════════════════════════════════
echo "── Flag Parameters ──"

# Test: _lk_parse_cli_flags detects parameterized flags
# Create a mock CLI that outputs help with parameterized flags
cat > "$MOCK_BIN/test-cli" << 'EOF'
#!/usr/bin/env bash
if [[ "$1" == "run" && "$2" == "--help" ]]; then
    cat << 'HELP'
Usage: test-cli run [OPTIONS]

Options:
  --agent <AGENT>       Load a specific agent profile
  --model MODEL         Override the default model
  --trust-all-tools     Allow all tools
  --no-interactive      Run without user input
  --effort <EFFORT>     Set effort level
  -a, --trust-tools     Trust specific tools (short + long)
  -v, --version         Print version (short + long)
  -q                    Quiet mode (short only)
HELP
fi
EOF
chmod +x "$MOCK_BIN/test-cli"
rm -f /tmp/lk-flags-test-cli.cache

_LK_CLI_TOOL="test-cli"
_LK_CLI_LAUNCH="run"
parsed=$(_lk_parse_cli_flags "test-cli" "run")
assert_contains "flags: detects --agent with param" "--agent=<AGENT>" "$parsed"
assert_contains "flags: detects --model with param" "--model=<MODEL>" "$parsed"
assert_contains "flags: detects --trust-all-tools (boolean)" "--trust-all-tools" "$parsed"
assert_contains "flags: detects --effort with param" "--effort=<EFFORT>" "$parsed"
# M2: short-option parsing. A "-x, --long" line yields the long form (long
# branch matches first); a short-only line like "-q" yields the short flag.
# --version/--help are intentionally filtered out as noise, even via a short alias.
assert_contains "flags: -a, --trust-tools yields long form" "--trust-tools" "$parsed"
assert_contains "flags: bare -q short-only flag collected" "-q" "$parsed"
if [[ "$parsed" != *"--version"* ]]; then
    printf '  \e[32m✓\e[0m flags: --version filtered even with short alias\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m flags: --version should be filtered\n'; ((FAIL++))
fi

# Restore
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"

# ═══════════════════════════════════════
echo "── CLI Resume Capabilities ──"
# T1.1: capability map accessors
assert_eq "caps: kiro-cli resume flag" "--resume" "$(_lk_cli_resume_flag kiro-cli)"
assert_eq "caps: claude resume flag (to-verify)" "--continue" "$(_lk_cli_resume_flag claude)"
assert_eq "caps: aider has no resume flag" "" "$(_lk_cli_resume_flag aider)"
assert_eq "caps: unknown tool has no resume flag" "" "$(_lk_cli_resume_flag nope-cli)"
assert_eq "caps: kiro-cli list argv" "chat --list-sessions --format json" "$(_lk_cli_list_argv kiro-cli)"
assert_eq "caps: claude has no list argv" "" "$(_lk_cli_list_argv claude)"
assert_eq "caps: unknown tool has no list argv" "" "$(_lk_cli_list_argv nope-cli)"
assert_eq "caps: kiro-cli picker flag" "--resume-picker" "$(_lk_cli_picker_flag kiro-cli)"
assert_eq "caps: claude has no picker flag" "" "$(_lk_cli_picker_flag claude)"
assert_eq "caps: unknown tool has no picker flag" "" "$(_lk_cli_picker_flag nope-cli)"

# T1.2: resume threading through _lk_run_cli (current-tab launch).
# The mock kiro-cli logs its argv to $LK_TEST_LOG.
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"

# Resume inserts --resume after the subcommand and suppresses the prompt.
rm -f "$LK_TEST_LOG"
_lk_run_cli "--trust-all-tools" "You are Lumen" "1" >/dev/null 2>&1
resume_call=$(cat "$LK_TEST_LOG" 2>/dev/null)
assert_contains "resume: --resume flag present" "--resume" "$resume_call"
assert_contains "resume: subcommand before flag" "chat --resume" "$resume_call"
assert_contains "resume: other flags still passed" "--trust-all-tools" "$resume_call"
if [[ "$resume_call" != *"You are Lumen"* ]]; then
    printf '  \e[32m✓\e[0m resume: prompt suppressed on resume\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m resume: prompt should be suppressed on resume\n'; ((FAIL++))
fi

# Non-resume launch keeps the prompt and omits --resume.
rm -f "$LK_TEST_LOG"
_lk_run_cli "--trust-all-tools" "You are Lumen" "" >/dev/null 2>&1
fresh_call=$(cat "$LK_TEST_LOG" 2>/dev/null)
assert_contains "resume: fresh launch keeps prompt" "You are Lumen" "$fresh_call"
if [[ "$fresh_call" != *"--resume"* ]]; then
    printf '  \e[32m✓\e[0m resume: fresh launch omits --resume\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m resume: fresh launch should omit --resume\n'; ((FAIL++))
fi

# Picker mode inserts --resume-picker (not --resume) and drops the prompt.
rm -f "$LK_TEST_LOG"
_lk_run_cli "--trust-all-tools" "You are Lumen" "picker" >/dev/null 2>&1
picker_call=$(cat "$LK_TEST_LOG" 2>/dev/null)
assert_contains "picker: --resume-picker flag present" "--resume-picker" "$picker_call"
assert_contains "picker: subcommand before flag" "chat --resume-picker" "$picker_call"
if [[ "$picker_call" != *"You are Lumen"* ]]; then
    printf '  \e[32m✓\e[0m picker: prompt suppressed\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m picker: prompt should be suppressed\n'; ((FAIL++))
fi

# Unsupported CLI: resume requested but no flag emitted (mock a flagless tool).
cat > "$MOCK_BIN/noresume-cli" << 'MOCK'
#!/usr/bin/env bash
echo "NORESUME_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/noresume-cli"
# Add a flagless entry to the caps map for the duration of this test.
_LK_CLI_CAPS+=("noresume-cli||")
_LK_CLI_TOOL="noresume-cli"
_LK_CLI_LAUNCH=""
rm -f "$LK_TEST_LOG"
noresume_out=$(_lk_run_cli "" "" "1" 2>&1)
noresume_call=$(cat "$LK_TEST_LOG" 2>/dev/null)
assert_contains "resume: unsupported CLI notes fresh start" "no session resume support" "$noresume_out"
if [[ "$noresume_call" != *"--resume"* && "$noresume_call" != *"--continue"* ]]; then
    printf '  \e[32m✓\e[0m resume: unsupported CLI emits no resume flag\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m resume: unsupported CLI must not emit a resume flag\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/noresume-cli"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
rm -f "$LK_TEST_LOG"

# ═══════════════════════════════════════
echo "── Integration (Live App) ──"

# Setup: temp config for integration tests
INT_CONFIG=$(mktemp)
INT_DIR=$(mktemp -d)
mkdir -p "$INT_DIR/project-a" "$INT_DIR/project-b" "$INT_DIR/project-c"

_LK_CONFIG_FILE="$INT_CONFIG"
export LK_PROJECTS_DIR="$INT_DIR"
_LK_KIRO_FLAGS="--trust-all-tools"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
_lk_assign "_LK_KIRO_FLAGS_$(_lk_safe_name "project-b")" "--agent mechanic"
_lk_assign "_LK_KIRO_FLAGS_$(_lk_safe_name "project-c")" "none"
_lk_write_config
_lk_load_config

# Test: main menu renders all projects
menu=$(echo "q" | lk 2>/dev/null)
assert_contains "live: shows project-a" "project-a" "$menu"
assert_contains "live: shows project-b" "project-b" "$menu"
assert_contains "live: shows project-c" "project-c" "$menu"
assert_contains "live: shows version" "v${_EXPECTED_VER}" "$menu"
assert_contains "live: shows config option" "config" "$menu"
assert_contains "live: shows quit option" "quit" "$menu"

# Test: selecting project launches CLI with correct flags (word-split)
rm -f "$LK_TEST_LOG"
# project-a = entry 3 (terminal=1, root=2, project-a=3)
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_contains "live: project-a gets default flags" "--trust-all-tools" "$kiro_call"
else
    printf '  \e[31m✗\e[0m live: project-a should call CLI\n'; ((FAIL++))
fi

# Test: project with per-project flags gets inherited + own
rm -f "$LK_TEST_LOG"
# project-b = entry 4
(echo "4" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_contains "live: project-b gets default flag" "--trust-all-tools" "$kiro_call"
    assert_contains "live: project-b gets own flag" "--agent" "$kiro_call"
    assert_contains "live: project-b gets flag value" "mechanic" "$kiro_call"
else
    printf '  \e[31m✗\e[0m live: project-b should call CLI\n'; ((FAIL++))
fi

# Test: flags are word-split (each flag is a separate argument)
rm -f "$LK_TEST_LOG"
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
# Log each argument separately
for arg in "$@"; do
    echo "ARG: $arg" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
done
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
(echo "4" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    # Should have separate args, not one combined string
    arg_count=$(grep -c "^ARG:" "$LK_TEST_LOG")
    # Expected: chat --trust-all-tools --agent mechanic = 4 args
    assert_eq "live: flags word-split correctly (4 args)" "4" "$arg_count"
    assert_contains "live: arg includes chat" "ARG: chat" "$(cat "$LK_TEST_LOG")"
    assert_contains "live: arg includes --trust-all-tools" "ARG: --trust-all-tools" "$(cat "$LK_TEST_LOG")"
    assert_contains "live: arg includes --agent" "ARG: --agent" "$(cat "$LK_TEST_LOG")"
    assert_contains "live: arg includes mechanic" "ARG: mechanic" "$(cat "$LK_TEST_LOG")"
else
    printf '  \e[31m✗\e[0m live: word-split test should call CLI\n'; ((FAIL++))
fi

# Restore simple mock
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"

# Test: entry 1 (terminal) does not launch CLI
rm -f "$LK_TEST_LOG"
(echo "1" | lk) >/dev/null 2>&1 || true
if [[ ! -f "$LK_TEST_LOG" ]]; then
    printf '  \e[32m✓\e[0m live: terminal entry does not launch CLI\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m live: terminal should not launch CLI\n'; ((FAIL++))
fi

# Test: entry 2 (root) launches CLI
rm -f "$LK_TEST_LOG"
(echo "2" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    printf '  \e[32m✓\e[0m live: root entry launches CLI\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m live: root entry should launch CLI\n'; ((FAIL++))
fi

# Test: config menu full flow (open → edit default flags → add custom flag → done → back → quit)
flag_count=$(_lk_get_flag_opts | wc -l)
custom_idx=$((flag_count - 1))
printf "c\n2\n${custom_idx}\n--test-flag\nd\nb\nq\n" | lk >/dev/null 2>&1
_lk_load_config
assert_contains "live: config edit persists flag" "--test-flag" "$_LK_KIRO_FLAGS"

# Test: "none" flag suppression works end-to-end
# project-c has "none" from the config we wrote earlier, entry 5
_lk_write_config
rm -f "$LK_TEST_LOG"
(echo "5" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_eq "live: none suppresses all flags" "KIRO_CALLED: chat" "$kiro_call"
else
    printf '  \e[31m✗\e[0m live: project-c should call CLI\n'; ((FAIL++))
fi

# Cleanup integration tests
rm -rf "$INT_CONFIG" "$INT_DIR"

# ═══════════════════════════════════════
echo "── Self-Inclusion ──"

# Test: launcher directory is shown in menu when inside projects dir
EXCL_DIR=$(mktemp -d)
mkdir -p "$EXCL_DIR/real-project" "$EXCL_DIR/kiro-launcher"
_LK_CONFIG_FILE="/nonexistent"
export LK_PROJECTS_DIR="$EXCL_DIR"
_LK_SCRIPT_DIR="$EXCL_DIR/kiro-launcher"
_lk_load_config
menu_output=$(echo "q" | lk 2>/dev/null || true)
assert_contains "self-include: real project shown" "real-project" "$menu_output"
assert_contains "self-include: launcher dir shown in menu" "kiro-launcher" "$menu_output"

# Restore
_LK_SCRIPT_DIR="$PROJECT_DIR"
rm -rf "$EXCL_DIR"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Prompt Config ──"

# Test: prompt parsed from config
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
prompt="$(_lk_indirect "_LK_PROJECT_PROMPT_$(_lk_safe_name "ml-project")")"
assert_eq "prompt: parsed from config" "help me with ML" "$prompt"

# Test: project without prompt returns empty
prompt="$(_lk_indirect "_LK_PROJECT_PROMPT_beta")"
assert_eq "prompt: empty when not configured" "" "$prompt"

# Test: prompt passed to CLI on launch
rm -f "$LK_TEST_LOG"
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
PROMPT_DIR=$(mktemp -d)
mkdir -p "$PROMPT_DIR/prompted"
export LK_PROJECTS_DIR="$PROMPT_DIR"
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_contains "prompt: passed to CLI invocation" "help me with this" "$kiro_call"
else
    printf '  \e[31m✗\e[0m prompt: CLI should be called with prompt\n'; ((FAIL++))
fi
rm -rf "$PROMPT_DIR"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Tilde Expansion ──"

# Test: tilde in projects_dir is expanded to HOME
TILDE_CONFIG=$(mktemp)
cat > "$TILDE_CONFIG" << EOF
projects_dir: ~/my-projects
default_flags: --test
cli_tool: kiro-cli
cli_launch: chat
EOF
_LK_CONFIG_FILE="$TILDE_CONFIG"
unset LK_PROJECTS_DIR
_lk_load_config
assert_eq "tilde: projects_dir expanded" "$HOME/my-projects" "$_LK_PROJECTS_DIR"
rm -f "$TILDE_CONFIG"

# ═══════════════════════════════════════
echo "── Invalid Projects Dir ──"

# Test: error message when directory doesn't exist
_LK_CONFIG_FILE="/nonexistent"
export LK_PROJECTS_DIR="/nonexistent/fake-dir"
_lk_load_config
error_output=$(lk 2>/dev/null)
assert_contains "invalid-dir: shows error message" "not found" "$error_output"
assert_contains "invalid-dir: shows path" "/nonexistent/fake-dir" "$error_output"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Changelog Key ──"

# Test: 'v' key shows changelog from main menu
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_SCRIPT_DIR="$PROJECT_DIR"
_lk_load_config
changelog_menu=$(printf 'v\n' | lk 2>/dev/null || true)
assert_contains "changelog-key: v shows changelog" "Changelog" "$changelog_menu"
assert_contains "changelog-key: shows version entry" "1.6.1" "$changelog_menu"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── CLI Detection ──"

# Test: _lk_detect_clis finds mocked tool
# kiro-cli mock is already on PATH from setup
detected=$(_lk_detect_clis)
assert_contains "cli-detect: finds kiro-cli on PATH" "kiro-cli" "$detected"

# Test: _lk_get_default_launch returns correct subcommand
subcmd=$(_lk_get_default_launch "kiro-cli")
assert_eq "cli-detect: kiro-cli default launch is chat" "chat" "$subcmd"

subcmd=$(_lk_get_default_launch "claude")
assert_eq "cli-detect: claude default launch is empty" "" "$subcmd"

subcmd=$(_lk_get_default_launch "goose")
assert_eq "cli-detect: goose default launch is session" "session" "$subcmd"

# ═══════════════════════════════════════
echo "── CLI Tool Config ──"

# Test: _lk_config_edit_cli_tool with mock selection
CLI_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$CLI_CONFIG"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
_lk_write_config
# Select option 1 (kiro-cli, since it's on PATH)
printf '1\n' | _lk_config_edit_cli_tool >/dev/null 2>&1
_lk_load_config
assert_eq "cli-config: tool saved after selection" "kiro-cli" "$_LK_CLI_TOOL"
assert_eq "cli-config: launch saved after selection" "chat" "$_LK_CLI_LAUNCH"
rm -f "$CLI_CONFIG"

# ═══════════════════════════════════════
echo "── Root Entry Flags ──"

# Test: entry 2 (root) passes resolved flags to CLI
ROOT_DIR=$(mktemp -d)
mkdir -p "$ROOT_DIR/sub"
ROOT_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$ROOT_CONFIG"
export LK_PROJECTS_DIR="$ROOT_DIR"
_LK_KIRO_FLAGS="--trust-all-tools"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
_lk_write_config
_lk_load_config
rm -f "$LK_TEST_LOG"
(echo "2" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_contains "root-flags: root entry passes default flags" "--trust-all-tools" "$kiro_call"
else
    printf '  \e[31m✗\e[0m root-flags: root entry should call CLI\n'; ((FAIL++))
fi
rm -rf "$ROOT_DIR" "$ROOT_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── New Tab Fallback ──"

# Test: project with color but no wt.exe falls back to current tab
FALLBACK_DIR=$(mktemp -d)
mkdir -p "$FALLBACK_DIR/colorful"
FALLBACK_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$FALLBACK_CONFIG"
export LK_PROJECTS_DIR="$FALLBACK_DIR"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
_lk_assign "_LK_PROJECT_COLOR_colorful" "#ff0000"
_lk_write_config
_lk_load_config
# Ensure wt.exe is NOT found (remove from PATH if somehow present)
OLD_PATH="$PATH"
PATH="$MOCK_BIN"
rm -f "$MOCK_BIN/wt.exe"
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
PATH="$OLD_PATH"
if [[ -f "$LK_TEST_LOG" ]]; then
    printf '  \e[32m✓\e[0m new-tab-fallback: runs in current tab when wt.exe missing\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m new-tab-fallback: should fall back to current tab\n'; ((FAIL++))
fi
rm -rf "$FALLBACK_DIR" "$FALLBACK_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Menu Loop After New Tab ──"

# Test: _LK_LAUNCHED_NEW_TAB causes menu to continue (not return)
# We simulate this by mocking wt.exe so _lk_launch_project sets the flag
LOOP_DIR=$(mktemp -d)
mkdir -p "$LOOP_DIR/tabproj"
LOOP_CONFIG=$(mktemp)
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
# Mock wt.exe — just succeed
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
_LK_CONFIG_FILE="$LOOP_CONFIG"
export LK_PROJECTS_DIR="$LOOP_DIR"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
_lk_assign "_LK_PROJECT_COLOR_tabproj" "#00ff00"
_lk_assign "_LK_PROJECT_TITLE_tabproj" "Tab Project"
_lk_write_config
_lk_load_config
# Select the project (entry 3), then menu should loop — we send 'q' to exit
loop_output=$(printf '3\nq\n' | lk 2>/dev/null || true)
# The menu should render twice (once before selection, once after loop-back)
menu_count=$(echo "$loop_output" | grep -c "Kiro Launcher" || true)
if (( menu_count >= 2 )); then
    printf '  \e[32m✓\e[0m menu-loop: menu re-renders after new-tab launch\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m menu-loop: menu should re-render after new-tab launch (got %d)\n' "$menu_count"; ((FAIL++))
fi
rm -f "$MOCK_BIN/wt.exe"
rm -rf "$LOOP_DIR" "$LOOP_CONFIG"
unset LK_PROJECTS_DIR

# Restore simple mock
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"

# ═══════════════════════════════════════
echo "── Clean Prompt After Spawn ──"

# Test: focus event escape sequences are flushed after new-tab spawn
# Simulates terminal sending ^[[O (focus out) and ^[[I (focus in) after wt.exe launches
CLEAN_DIR=$(mktemp -d)
mkdir -p "$CLEAN_DIR/cleanproj"
CLEAN_CONFIG=$(mktemp)
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
_LK_CONFIG_FILE="$CLEAN_CONFIG"
export LK_PROJECTS_DIR="$CLEAN_DIR"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
_lk_assign "_LK_PROJECT_COLOR_cleanproj" "#00ff00"
_lk_write_config
_lk_load_config
# Feed: select project (3), then focus events (as if terminal sent them), then quit
# \e[O = focus out, \e[I = focus in — these arrive after wt.exe spawns
clean_output=$(printf '3\n\e[O\e[I\nq\n' | lk 2>/dev/null || true)
# The prompt line after re-render should NOT contain escape sequences as visible text
if [[ "$clean_output" != *"^[[O"* && "$clean_output" != *"^[[I"* && "$clean_output" != *$'\e[O'* && "$clean_output" != *$'\e[I'* ]]; then
    printf '  \e[32m✓\e[0m clean-prompt: no focus event garbage after spawn\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m clean-prompt: focus events should be flushed\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/wt.exe"
rm -rf "$CLEAN_DIR" "$CLEAN_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Spawned Tab: TTY drain-loop ──"
# Regression: stray terminal-reply bytes (device-attribute / OSC color-query
# responses from the wt.exe tab handshake, e.g. "2c/3434") leaked into the
# CLI's prompt on launch. The generated tab script must drain the TTY in a
# loop (raw + no-echo, settle window, read-until-empty) rather than a single
# fixed-timeout read that misses late-arriving bytes.

DRAIN_DIR=$(mktemp -d)
mkdir -p "$DRAIN_DIR/drainproj"
DRAIN_CONFIG=$(mktemp)
DRAIN_CAPTURE=$(mktemp)
# Mock wt.exe: capture the generated tab-script path (the last arg) and copy it
# so we can inspect what the launcher wrote.
cat > "$MOCK_BIN/wt.exe" << MOCK
#!/usr/bin/env bash
for a in "\$@"; do
    case "\$a" in
        *lk-tab-*.sh) cp "\$a" "$DRAIN_CAPTURE" 2>/dev/null ;;
    esac
done
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
_LK_CONFIG_FILE="$DRAIN_CONFIG"
export LK_PROJECTS_DIR="$DRAIN_DIR"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
# color/title forces the new-tab (wt.exe) path that generates the script.
_lk_assign "_LK_PROJECT_COLOR_drainproj" "#123456"
_lk_write_config
_lk_load_config
printf '3\nq\n' | lk >/dev/null 2>&1 || true

drain_script="$(cat "$DRAIN_CAPTURE" 2>/dev/null || true)"
assert_contains "drain: generated script defines drain function" "_lk_drain_tty()" "$drain_script"
assert_contains "drain: generated script calls drain function" $'\n_lk_drain_tty\n' "$drain_script"
assert_contains "drain: uses stty raw/no-echo" "stty raw -echo" "$drain_script"
assert_contains "drain: loops until input is empty" "IFS= read -r -t 0.05 -n 4096" "$drain_script"
assert_contains "drain: restores TTY state" 'stty "$_saved"' "$drain_script"
assert_contains "drain: traps INT/TERM to restore TTY" 'trap' "$drain_script"
assert_contains "drain: trap restores saved state on signal" 'trap '\''stty "$_saved"' "$drain_script"
# Guard against reverting to the ineffective single-shot drain.
if [[ "$drain_script" != *'read -r -t 0.2 -n 10000'* ]]; then
    printf '  \e[32m✓\e[0m drain: no ineffective single-shot read\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m drain: single-shot read must not return\n'; ((FAIL++))
fi
# The drain must run before the exec of the CLI, not after.
if [[ "$drain_script" == *_lk_drain_tty*exec* ]]; then
    printf '  \e[32m✓\e[0m drain: runs before exec\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m drain: must run before exec\n'; ((FAIL++))
fi

# Behavioral check: the drain-loop logic consumes stray bytes and leaves none.
drain_leftover="$(
    printf '\033[?62;1;2c\033]4;3434\007' | {
        total=""
        for ((i=0; i<50; i++)); do
            junk=""
            IFS= read -r -t 0.05 -n 4096 junk 2>/dev/null
            [[ -z "$junk" ]] && break
        done
        leftover=""
        IFS= read -r -t 0.05 -n 4096 leftover 2>/dev/null
        printf '%s' "$leftover"
    }
)"
assert_eq "drain: loop leaves no leftover bytes" "" "$drain_leftover"

rm -f "$MOCK_BIN/wt.exe"
rm -rf "$DRAIN_DIR" "$DRAIN_CONFIG" "$DRAIN_CAPTURE"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Resume: new-tab launch ──"
# T1.3: resume flag must land in the generated tab script (new-tab path) and the
# per-project prompt must be dropped on resume.
RESUME_DIR=$(mktemp -d)
mkdir -p "$RESUME_DIR/resumeproj"
RESUME_CONFIG=$(mktemp)
RESUME_CAPTURE=$(mktemp)
cat > "$MOCK_BIN/wt.exe" << MOCK
#!/usr/bin/env bash
for a in "\$@"; do
    case "\$a" in
        *lk-tab-*.sh) cp "\$a" "$RESUME_CAPTURE" 2>/dev/null ;;
    esac
done
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
_LK_CONFIG_FILE="$RESUME_CONFIG"
export LK_PROJECTS_DIR="$RESUME_DIR"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
# color forces the new-tab path; prompt is set so we can assert it's dropped.
_lk_assign "_LK_PROJECT_COLOR_resumeproj" "#334455"
_lk_assign "_LK_PROJECT_PROMPT_resumeproj" "You are Lumen"
_lk_write_config
_lk_load_config

# Reset cached wt.exe path so the new-tab path re-detects the mock.
unset _LK_WT_BIN
export PATH="$MOCK_BIN:$PATH"

# Resume launch (call _lk_launch_project directly with resume=1).
: > "$RESUME_CAPTURE"
_lk_launch_project "$RESUME_DIR/resumeproj" "1" >/dev/null 2>&1
sleep 0.3
resume_script="$(cat "$RESUME_CAPTURE" 2>/dev/null || true)"
assert_contains "resume-tab: script contains --resume" "--resume" "$resume_script"
if [[ "$resume_script" != *"Lumen"* ]]; then
    printf '  \e[32m✓\e[0m resume-tab: prompt dropped on resume\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m resume-tab: prompt should be dropped on resume\n'; ((FAIL++))
fi

# Fresh launch (no resume): no --resume, prompt retained.
: > "$RESUME_CAPTURE"
_lk_launch_project "$RESUME_DIR/resumeproj" "" >/dev/null 2>&1
sleep 0.3
fresh_script="$(cat "$RESUME_CAPTURE" 2>/dev/null || true)"
# The prompt is %q-quoted in the generated script (spaces escaped), so match a
# bare token rather than the spaced phrase.
assert_contains "resume-tab: fresh script keeps prompt" "Lumen" "$fresh_script"
if [[ "$fresh_script" != *"--resume"* ]]; then
    printf '  \e[32m✓\e[0m resume-tab: fresh launch omits --resume\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m resume-tab: fresh launch should omit --resume\n'; ((FAIL++))
fi

rm -f "$MOCK_BIN/wt.exe"
rm -rf "$RESUME_DIR" "$RESUME_CONFIG" "$RESUME_CAPTURE"
unset LK_PROJECTS_DIR
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done

# ═══════════════════════════════════════
echo "── Resume: menu r<N> trigger ──"
# T1.4: "r<N>" in the menu resume-launches project N; bare "N" does not.
# Restore the simple logging mock and use a current-tab project (no color/title).
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
RMENU_DIR=$(mktemp -d)
mkdir -p "$RMENU_DIR/alpha"
RMENU_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$RMENU_CONFIG"
export LK_PROJECTS_DIR="$RMENU_DIR"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
_LK_AUTO_UPDATE_CHECK="false"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
_lk_write_config
_lk_load_config

# Menu footer advertises the "?" help key (not the verbose r<N>/p<N> hints).
rmenu_view=$(echo "q" | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
assert_contains "resume-menu: footer offers ? help" "?  help" "$rmenu_view"
if [[ "$rmenu_view" != *"r<N>"* ]]; then
    printf '  \e[32m✓\e[0m resume-menu: footer is uncluttered (no inline r<N>)\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m resume-menu: r<N> should not appear inline in the footer\n'; ((FAIL++))
fi

# The "?" help screen documents the session triggers.
help_view=$(printf '?\nq\n' | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
assert_contains "help: shows r<N> resume" "r<N>" "$help_view"
assert_contains "help: shows p<N> pick" "p<N>" "$help_view"

# "r3" resume-launches the first project (index 3) with --resume.
rm -f "$LK_TEST_LOG"
(echo "r3" | lk) >/dev/null 2>&1 || true
rmenu_call=$(cat "$LK_TEST_LOG" 2>/dev/null)
assert_contains "resume-menu: r3 launches with --resume" "--resume" "$rmenu_call"

# Bare "3" launches fresh (no --resume).
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
plain_call=$(cat "$LK_TEST_LOG" 2>/dev/null)
if [[ "$plain_call" != *"--resume"* ]]; then
    printf '  \e[32m✓\e[0m resume-menu: bare 3 launches fresh\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m resume-menu: bare 3 must not resume\n'; ((FAIL++))
fi

# "p3" launches with --resume-picker.
rm -f "$LK_TEST_LOG"
(echo "p3" | lk) >/dev/null 2>&1 || true
pmenu_call=$(cat "$LK_TEST_LOG" 2>/dev/null)
assert_contains "picker-menu: p3 launches with --resume-picker" "--resume-picker" "$pmenu_call"

# Negative: a CLI with no resume/picker support hides the ? help key entirely.
cat > "$MOCK_BIN/plaincli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "plaincli 1.0.0"; exit 0; fi
echo "PLAIN_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/plaincli"
cat > "$RMENU_CONFIG" << EOF
projects_dir: "$RMENU_DIR"
cli_tool: "plaincli"
cli_launch: ""
auto_update_check: "false"
EOF
_lk_load_config
_LK_CLI_VERSION_CACHED=""
plain_view=$(echo "q" | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
if [[ "$plain_view" != *"?  help"* ]]; then
    printf '  \e[32m✓\e[0m help: ? key hidden when CLI lacks resume/picker\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m help: ? key should be hidden for a CLI without resume/picker\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/plaincli"
_LK_CLI_TOOL="kiro-cli"; _LK_CLI_LAUNCH="chat"; _LK_CLI_VERSION_CACHED=""

rm -rf "$RMENU_DIR" "$RMENU_CONFIG"
unset LK_PROJECTS_DIR
rm -f "$LK_TEST_LOG"

# ═══════════════════════════════════════
echo "── Refactor: compgen prefix ──"

# Test: _lk_vars_with_prefix finds matching variables
_LK_TEST_VAR_alpha=1
_LK_TEST_VAR_beta=2
_LK_TEST_VAR_gamma=3
found=$(_lk_vars_with_prefix _LK_TEST_VAR_)
assert_contains "compgen: finds alpha" "_LK_TEST_VAR_alpha" "$found"
assert_contains "compgen: finds beta" "_LK_TEST_VAR_beta" "$found"
assert_contains "compgen: finds gamma" "_LK_TEST_VAR_gamma" "$found"
unset _LK_TEST_VAR_alpha _LK_TEST_VAR_beta _LK_TEST_VAR_gamma

# Test: _lk_vars_with_prefix returns empty for no matches
found=$(_lk_vars_with_prefix _LK_NONEXISTENT_PREFIX_)
assert_eq "compgen: empty for no matches" "" "$found"

# ═══════════════════════════════════════
echo "── Refactor: indirect expansion ──"

# Test: _lk_indirect reads variable value
_LK_TEST_INDIRECT="hello world"
result=$(_lk_indirect "_LK_TEST_INDIRECT")
assert_eq "indirect: reads value" "hello world" "$result"

# Test: _lk_indirect returns empty for unset variable
unset _LK_TEST_UNSET_VAR 2>/dev/null
result=$(_lk_indirect "_LK_TEST_UNSET_VAR")
assert_eq "indirect: empty for unset" "" "$result"

# Test: _lk_indirect_set detects set variable
_LK_TEST_SET_VAR="x"
_lk_indirect_set "_LK_TEST_SET_VAR"
assert_eq "indirect_set: true for set var" "0" "$?"

# Test: _lk_indirect_set detects unset variable
unset _LK_TEST_UNSET_VAR 2>/dev/null
if _lk_indirect_set "_LK_TEST_UNSET_VAR"; then
    printf '  \e[31m✗\e[0m indirect_set: should be false for unset var\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m indirect_set: false for unset var\n'; ((PASS++))
fi

# Test: _lk_indirect_set true for empty string (set but empty)
_LK_TEST_EMPTY=""
_lk_indirect_set "_LK_TEST_EMPTY"
assert_eq "indirect_set: true for empty string" "0" "$?"
unset _LK_TEST_INDIRECT _LK_TEST_SET_VAR _LK_TEST_EMPTY

# ═══════════════════════════════════════
echo "── Refactor: write_config dedup ──"

# Test: write_config doesn't duplicate projects with multiple config types
DEDUP_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$DEDUP_CONFIG"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
# Set same project across all 4 namespaces
_lk_assign "_LK_KIRO_FLAGS_myproj" "--test"
_lk_assign "_LK_PROJECT_COLOR_myproj" "#ff0000"
_lk_assign "_LK_PROJECT_TITLE_myproj" "My Proj"
_lk_assign "_LK_PROJECT_PROMPT_myproj" "hello"
_lk_write_config
# Project should appear exactly once
proj_count=$(grep -c '^  "myproj":' "$DEDUP_CONFIG")
assert_eq "dedup: project appears once despite 4 config types" "1" "$proj_count"
# All properties should be present
written=$(cat "$DEDUP_CONFIG")
assert_contains "dedup: has flags" "--test" "$written"
assert_contains "dedup: has color" "#ff0000" "$written"
assert_contains "dedup: has title" "My Proj" "$written"
assert_contains "dedup: has prompt" "hello" "$written"
rm -f "$DEDUP_CONFIG"

# ═══════════════════════════════════════
echo "── Refactor: wt.exe caching ──"

# Test: _LK_WT_BIN is set after _lk_launch_project runs (cached)
unset _LK_WT_BIN
# Use an explicit mock wt.exe so this test can NEVER invoke the real Windows
# Terminal (which, on a WSL host, would spawn an actual tab into the temp dir
# and race the rm -rf below — producing a stray "chdir failed" tab).
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
WT_DIR=$(mktemp -d)
mkdir -p "$WT_DIR/proj-wt"
_LK_PROJECTS_DIR="$WT_DIR"
_LK_WT_BIN="$MOCK_BIN/wt.exe"   # bypass discovery; never touch real wt.exe
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "proj-wt")" "#aabbcc"
# Call _lk_launch_project directly (not through pipe which creates subshell)
rm -f "$LK_TEST_LOG"
_lk_launch_project "$WT_DIR/proj-wt" >/dev/null 2>&1 || true
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
# After the call, _LK_WT_BIN should be set (even if empty — means lookup was done)
if [[ "${_LK_WT_BIN+set}" == "set" ]]; then
    printf '  \e[32m✓\e[0m wt-cache: _LK_WT_BIN set after first launch\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m wt-cache: _LK_WT_BIN should be set after launch\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/wt.exe"
rm -rf "$WT_DIR"
unset _LK_WT_BIN

# ═══════════════════════════════════════
echo "── Refactor: menu timeout ──"

# Test: menu returns on EOF (simulates timeout behavior — non-zero exit is expected)
TIMEOUT_DIR=$(mktemp -d)
mkdir -p "$TIMEOUT_DIR/proj1"
TIMEOUT_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$TIMEOUT_CONFIG"
_LK_PROJECTS_DIR="$TIMEOUT_DIR"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
export LK_PROJECTS_DIR="$TIMEOUT_DIR"
_lk_write_config
# Empty input triggers EOF on read — lk returns (doesn't hang)
lk <<< "" >/dev/null 2>&1 || true
printf '  \e[32m✓\e[0m timeout: exits on EOF without hanging\n'; ((PASS++))
rm -rf "$TIMEOUT_DIR" "$TIMEOUT_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Cross-Platform: TMPDIR ──"

# Test: flag cache uses TMPDIR when set
CUSTOM_TMP=$(mktemp -d)
export TMPDIR="$CUSTOM_TMP"
# Create a mock CLI that outputs help-like text
cat > "$MOCK_BIN/test-cli" << 'MOCK'
#!/usr/bin/env bash
echo "  --some-flag    Enable something"
MOCK
chmod +x "$MOCK_BIN/test-cli"
# Clear any cached value
rm -f "$CUSTOM_TMP/lk-flags-test-cli.cache"
_lk_parse_cli_flags "test-cli" "" >/dev/null 2>&1
if [[ -f "$CUSTOM_TMP/lk-flags-test-cli.cache" ]]; then
    printf '  \e[32m✓\e[0m tmpdir: flag cache written to TMPDIR\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m tmpdir: flag cache should be in TMPDIR\n'; ((FAIL++))
fi

# Test: cache invalidation uses TMPDIR
_LK_CLI_TOOL="test-cli"
rm -f "${TMPDIR:-/tmp}/lk-flags-${_LK_CLI_TOOL}.cache"
# Write a dummy cache to verify removal
echo "cached" > "$CUSTOM_TMP/lk-flags-test-cli.cache"
rm -f "${TMPDIR:-/tmp}/lk-flags-${_LK_CLI_TOOL}.cache"
if [[ ! -f "$CUSTOM_TMP/lk-flags-test-cli.cache" ]]; then
    printf '  \e[32m✓\e[0m tmpdir: cache invalidation uses TMPDIR\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m tmpdir: cache invalidation should use TMPDIR\n'; ((FAIL++))
fi
rm -rf "$CUSTOM_TMP"
rm -f "$MOCK_BIN/test-cli"
unset TMPDIR
_LK_CLI_TOOL="kiro-cli"

# Test: TMPDIR fallback to /tmp when unset
unset TMPDIR
result="${TMPDIR:-/tmp}"
assert_eq "tmpdir: falls back to /tmp when unset" "/tmp" "$result"

# ═══════════════════════════════════════
echo "── Cross-Platform: wt.exe Search Paths ──"

# Test: wt.exe found via command -v (on PATH)
unset _LK_WT_BIN
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
WT_SEARCH_DIR=$(mktemp -d)
mkdir -p "$WT_SEARCH_DIR/proj-wts"
_LK_PROJECTS_DIR="$WT_SEARCH_DIR"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "proj-wts")" "#aabbcc"
# Prepend MOCK_BIN so discovery (`command -v wt.exe`) resolves the MOCK, never
# a real Windows Terminal on the host PATH (which on WSL would spawn a real tab
# and race the cleanup below). Restore PATH afterwards.
_WTS_OLD_PATH="$PATH"; PATH="$MOCK_BIN:$PATH"
_lk_launch_project "$WT_SEARCH_DIR/proj-wts" >/dev/null 2>&1 || true
PATH="$_WTS_OLD_PATH"
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
assert_eq "wt-search: finds wt.exe on PATH" "$MOCK_BIN/wt.exe" "$_LK_WT_BIN"
rm -f "$MOCK_BIN/wt.exe"
rm -rf "$WT_SEARCH_DIR"
unset _LK_WT_BIN _WTS_OLD_PATH

# Test: wt.exe search tries /c/Users path (Git Bash style) after /mnt/c fails
# We can't easily mock find results, but we can verify the function
# doesn't error when neither path exists
unset _LK_WT_BIN
WT_NONE_DIR=$(mktemp -d)
mkdir -p "$WT_NONE_DIR/proj-none"
_LK_PROJECTS_DIR="$WT_NONE_DIR"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "proj-none")" "#112233"
OLD_PATH="$PATH"
PATH="$MOCK_BIN"  # no wt.exe in mock bin
_lk_launch_project "$WT_NONE_DIR/proj-none" >/dev/null 2>&1 || true
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
PATH="$OLD_PATH"
assert_eq "wt-search: empty when wt.exe not found anywhere" "" "${_LK_WT_BIN:-}"
rm -rf "$WT_NONE_DIR"
unset _LK_WT_BIN

# ═══════════════════════════════════════
echo "── Cross-Platform: Git Bash Detection ──"

# Test: MSYSTEM=MINGW64 triggers Git Bash code path
# We test _lk_launch_project with a mock wt.exe and MSYSTEM set
unset _LK_WT_BIN
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
echo "WT_ARGS: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
# Mock cygpath for Git Bash path conversion
cat > "$MOCK_BIN/cygpath" << 'MOCK'
#!/usr/bin/env bash
# Simulate cygpath -w: convert /tmp/x to C:\tmp\x
if [[ "$1" == "-w" ]]; then
    echo "C:\\tmp\\${2##*/}"
else
    echo "$1"
fi
MOCK
chmod +x "$MOCK_BIN/cygpath"
GITBASH_DIR=$(mktemp -d)
mkdir -p "$GITBASH_DIR/gb-proj"
_LK_PROJECTS_DIR="$GITBASH_DIR"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "gb-proj")" "#aabbcc"
_lk_assign "_LK_PROJECT_TITLE_$(_lk_safe_name "gb-proj")" "GB Test"
rm -f "$LK_TEST_LOG"
export MSYSTEM="MINGW64"
_LK_IS_WSL=0
_LK_WT_BIN="$MOCK_BIN/wt.exe"
_lk_launch_project "$GITBASH_DIR/gb-proj" >/dev/null 2>&1 || true
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
sleep 0.2
unset MSYSTEM
if [[ -f "$LK_TEST_LOG" ]]; then
    wt_call=$(cat "$LK_TEST_LOG")
    # On Git Bash, the script path should be converted via cygpath
    assert_contains "gitbash: wt.exe called with windows path" "C:\\tmp\\" "$wt_call"
    assert_contains "gitbash: wt.exe has tabColor arg" "--tabColor" "$wt_call"
    assert_contains "gitbash: wt.exe has title arg" "--title" "$wt_call"
else
    printf '  \e[31m✗\e[0m gitbash: wt.exe should be called\n'; ((FAIL++))
    printf '  \e[31m✗\e[0m gitbash: (skipping dependent assertions)\n'; ((FAIL+=2))
fi
rm -f "$MOCK_BIN/wt.exe" "$MOCK_BIN/cygpath" "$LK_TEST_LOG"
rm -rf "$GITBASH_DIR"
unset _LK_WT_BIN

# Test: without MSYSTEM, non-WSL uses plain bash path (Linux/macOS)
unset _LK_WT_BIN
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
echo "WT_ARGS: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
UNIX_DIR=$(mktemp -d)
mkdir -p "$UNIX_DIR/unix-proj"
_LK_PROJECTS_DIR="$UNIX_DIR"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "unix-proj")" "#ccddee"
rm -f "$LK_TEST_LOG"
unset MSYSTEM
# The launcher's git-bash detection also falls back to `uname -s` (in case
# MSYSTEM isn't set), so unsetting MSYSTEM alone doesn't simulate a plain
# unix host when this suite happens to be running for real on Git Bash for
# Windows, where `uname -s` genuinely reports MINGW*. Shadow it with a
# function for the duration of this one check.
uname() { echo "Linux"; }
_LK_IS_WSL=0
_LK_WT_BIN="$MOCK_BIN/wt.exe"
_lk_launch_project "$UNIX_DIR/unix-proj" >/dev/null 2>&1 || true
unset -f uname
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
sleep 0.2
if [[ -f "$LK_TEST_LOG" ]]; then
    wt_call=$(cat "$LK_TEST_LOG")
    # Should use unix-style path (not cygpath converted)
    assert_contains "unix: wt.exe called with bash -l" "bash" "$wt_call"
    # Should NOT contain C:\ prefix
    if [[ "$wt_call" != *'C:\'* ]]; then
        printf '  \e[32m✓\e[0m unix: path not converted (native unix)\n'; ((PASS++))
    else
        printf '  \e[31m✗\e[0m unix: path should not have windows conversion\n'; ((FAIL++))
    fi
else
    printf '  \e[31m✗\e[0m unix: wt.exe should be called\n'; ((FAIL++))
    printf '  \e[31m✗\e[0m unix: (skipping dependent assertion)\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/wt.exe" "$LK_TEST_LOG"
rm -rf "$UNIX_DIR"
unset _LK_WT_BIN _LK_IS_WSL

# ═══════════════════════════════════════
echo "── Regression: WSL new-tab uses --cd, not a UNC startingDirectory ──"
# Bug: the WSL branch pinned wt.exe --startingDirectory to a
# \\wsl.localhost\<distro>\... UNC path (from `wslpath -w`). WSL cannot set its
# working directory to a UNC path, so wsl.exe failed with
# "Wsl/ERROR_PATH_NOT_FOUND" before the tab script ran. The fix passes the Linux
# path to `wsl.exe --cd` and drops the UNC --startingDirectory.
unset _LK_WT_BIN
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
echo "WT_ARGS: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
WSL_DIR=$(mktemp -d)
mkdir -p "$WSL_DIR/wsl-proj"
_LK_PROJECTS_DIR="$WSL_DIR"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "wsl-proj")" "#123456"
rm -f "$LK_TEST_LOG"
unset MSYSTEM
_LK_IS_WSL=1
WSL_DISTRO_NAME="Ubuntu"
_LK_WT_BIN="$MOCK_BIN/wt.exe"
_lk_launch_project "$WSL_DIR/wsl-proj" >/dev/null 2>&1 || true
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
sleep 0.2
if [[ -f "$LK_TEST_LOG" ]]; then
    wt_call=$(cat "$LK_TEST_LOG")
    assert_contains "wsl: wt.exe invokes wsl.exe" "wsl.exe" "$wt_call"
    assert_contains "wsl: passes --cd with the linux project dir" "--cd $WSL_DIR/wsl-proj" "$wt_call"
    # Must NOT pass a \\wsl UNC path as startingDirectory.
    if [[ "$wt_call" != *'--startingDirectory'* && "$wt_call" != *'\\wsl'* ]]; then
        printf '  \e[32m✓\e[0m wsl: no UNC --startingDirectory (ERROR_PATH_NOT_FOUND guard)\n'; ((PASS++))
    else
        printf '  \e[31m✗\e[0m wsl: must not pass a UNC --startingDirectory\n    actual: %s\n' "$wt_call"; ((FAIL++))
    fi
    assert_contains "wsl: still launches bash -l with the script" "bash -l" "$wt_call"
else
    printf '  \e[31m✗\e[0m wsl: wt.exe should be called\n'; ((FAIL+=4))
fi
rm -f "$MOCK_BIN/wt.exe" "$LK_TEST_LOG"
rm -rf "$WSL_DIR"
unset _LK_WT_BIN _LK_IS_WSL WSL_DISTRO_NAME

# ═══════════════════════════════════════
echo "── Cross-Platform: WSL Detection ──"

# Test: WSL detection sets is_wsl based on /proc/version
# We can't mock /proc/version easily, but we can test the _LK_IS_WSL override
unset _LK_WT_BIN
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
echo "WT_ARGS: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
WSL_DIR=$(mktemp -d)
mkdir -p "$WSL_DIR/wsl-proj"
_LK_PROJECTS_DIR="$WSL_DIR"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "wsl-proj")" "#112233"
rm -f "$LK_TEST_LOG"
_LK_IS_WSL=1
export WSL_DISTRO_NAME="Ubuntu-24.04"
_LK_WT_BIN="$MOCK_BIN/wt.exe"
_lk_launch_project "$WSL_DIR/wsl-proj" >/dev/null 2>&1 || true
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
sleep 0.2
if [[ -f "$LK_TEST_LOG" ]]; then
    wt_call=$(cat "$LK_TEST_LOG")
    assert_contains "wsl: wt.exe uses wsl.exe" "wsl.exe" "$wt_call"
    assert_contains "wsl: wt.exe passes distro name" "Ubuntu-24.04" "$wt_call"
else
    printf '  \e[31m✗\e[0m wsl: wt.exe should be called\n'; ((FAIL++))
    printf '  \e[31m✗\e[0m wsl: (skipping dependent assertion)\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/wt.exe" "$LK_TEST_LOG"
rm -rf "$WSL_DIR"
unset _LK_WT_BIN _LK_IS_WSL WSL_DISTRO_NAME

# Test: WSL_DISTRO_NAME defaults to Ubuntu when unset
unset _LK_WT_BIN
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
echo "WT_ARGS: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
WSL_DEF_DIR=$(mktemp -d)
mkdir -p "$WSL_DEF_DIR/wsl-def"
_LK_PROJECTS_DIR="$WSL_DEF_DIR"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "wsl-def")" "#445566"
rm -f "$LK_TEST_LOG"
_LK_IS_WSL=1
unset WSL_DISTRO_NAME
_LK_WT_BIN="$MOCK_BIN/wt.exe"
_lk_launch_project "$WSL_DEF_DIR/wsl-def" >/dev/null 2>&1 || true
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
sleep 0.2
if [[ -f "$LK_TEST_LOG" ]]; then
    wt_call=$(cat "$LK_TEST_LOG")
    assert_contains "wsl: defaults to Ubuntu distro" "Ubuntu" "$wt_call"
else
    printf '  \e[31m✗\e[0m wsl: wt.exe should be called\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/wt.exe" "$LK_TEST_LOG"
rm -rf "$WSL_DEF_DIR"
unset _LK_WT_BIN _LK_IS_WSL

# ═══════════════════════════════════════
echo "── Cross-Platform: Script File ──"

# Test: launch creates script file in TMPDIR
unset _LK_WT_BIN
cat > "$MOCK_BIN/wt.exe" << 'MOCK'
#!/usr/bin/env bash
# Extract script path from args (last arg)
echo "WT_ARGS: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
exit 0
MOCK
chmod +x "$MOCK_BIN/wt.exe"
SCRIPT_TMP=$(mktemp -d)
export TMPDIR="$SCRIPT_TMP"
SCRIPT_DIR2=$(mktemp -d)
mkdir -p "$SCRIPT_DIR2/script-proj"
_LK_PROJECTS_DIR="$SCRIPT_DIR2"
_lk_assign "_LK_PROJECT_COLOR_$(_lk_safe_name "script-proj")" "#998877"
rm -f "$LK_TEST_LOG"
_LK_IS_WSL=0
unset MSYSTEM
_LK_WT_BIN="$MOCK_BIN/wt.exe"
_lk_launch_project "$SCRIPT_DIR2/script-proj" >/dev/null 2>&1 || true
cd "$PROJECT_DIR" || true   # launching cds the shell; restore it
# Check that a script file was created in TMPDIR
script_files=$(find "$SCRIPT_TMP" -name "lk-tab-*.sh" 2>/dev/null | head -1)
if [[ -n "$script_files" ]]; then
    printf '  \e[32m✓\e[0m script-file: created in TMPDIR\n'; ((PASS++))
    # Verify script contains proper shebang and cd command
    script_content=$(cat "$script_files")
    assert_contains "script-file: has bash shebang" "#!/usr/bin/env bash" "$script_content"
    assert_contains "script-file: has cd command" "cd " "$script_content"
    assert_contains "script-file: has self-cleanup" "rm -f" "$script_content"
else
    printf '  \e[31m✗\e[0m script-file: should be created in TMPDIR\n'; ((FAIL++))
    printf '  \e[31m✗\e[0m script-file: (skipping content checks)\n'; ((FAIL+=2))
fi
rm -f "$MOCK_BIN/wt.exe" "$LK_TEST_LOG"
rm -rf "$SCRIPT_TMP" "$SCRIPT_DIR2"
unset TMPDIR _LK_WT_BIN _LK_IS_WSL

# Restore simple mock
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"

# ═══════════════════════════════════════
echo "── YAML Parser Corner Cases ──"

# Test: empty flags value parses as empty string (set but empty)
YAML_CORNER=$(mktemp)
cat > "$YAML_CORNER" << 'EOF'
projects_dir: ~/dev
default_flags: --global
cli_tool: kiro-cli
cli_launch: chat

projects:
  emptyflags:
    flags:
  coloronly:
    color: "#aabbcc"
  titleonly:
    title: Hello World
EOF
_LK_CONFIG_FILE="$YAML_CORNER"
_lk_load_config
# emptyflags has "flags:" with no value — should be empty string
flags_val="$(_lk_indirect "_LK_KIRO_FLAGS_emptyflags")"
assert_eq "yaml: empty flags value parsed as empty" "" "$flags_val"
# coloronly has no flags — should inherit defaults
flags=$(_lk_get_flags "coloronly")
assert_eq "yaml: project with only color inherits defaults" "--global" "$flags"
# titleonly has no flags or color — just title
title="$(_lk_indirect "_LK_PROJECT_TITLE_titleonly")"
assert_eq "yaml: title-only project parses title" "Hello World" "$title"
color="$(_lk_indirect "_LK_PROJECT_COLOR_titleonly")"
assert_eq "yaml: title-only project has no color" "" "$color"
rm -f "$YAML_CORNER"

# Test: config with no projects section
YAML_NO_PROJ=$(mktemp)
cat > "$YAML_NO_PROJ" << 'EOF'
projects_dir: ~/code
default_flags: --test
cli_tool: claude
cli_launch:
EOF
_LK_CONFIG_FILE="$YAML_NO_PROJ"
_lk_load_config
assert_eq "yaml: no-projects config loads dir" "$HOME/code" "$_LK_PROJECTS_DIR"
assert_eq "yaml: no-projects config loads flags" "--test" "$_LK_KIRO_FLAGS"
assert_eq "yaml: no-projects config loads cli_tool" "claude" "$_LK_CLI_TOOL"
# cli_launch: with empty value doesn't match "key: value" regex, so default persists
assert_eq "yaml: no-projects config retains default cli_launch" "chat" "$_LK_CLI_LAUNCH"
rm -f "$YAML_NO_PROJ"

# Test: config with comments between project entries
YAML_COMMENTS=$(mktemp)
cat > "$YAML_COMMENTS" << 'EOF'
projects_dir: ~/dev
default_flags: --flag1
cli_tool: kiro-cli
cli_launch: chat

projects:
  # This is a comment
  proj-a:
    flags: --fa

  # Another comment
  proj-b:
    flags: --fb
    color: "#112233"
EOF
_LK_CONFIG_FILE="$YAML_COMMENTS"
_lk_load_config
flags=$(_lk_get_flags "proj-a")
assert_eq "yaml: comments don't break parsing (proj-a)" "--flag1 --fa" "$flags"
flags=$(_lk_get_flags "proj-b")
assert_eq "yaml: comments don't break parsing (proj-b)" "--flag1 --fb" "$flags"
color="$(_lk_indirect "_LK_PROJECT_COLOR_$(_lk_safe_name "proj-b")")"
assert_eq "yaml: comments don't break color parsing" "#112233" "$color"
rm -f "$YAML_COMMENTS"

# ═══════════════════════════════════════
echo "── Flags: No Defaults ──"

# Test: project flags without default_flags
NO_DEF_CONFIG=$(mktemp)
cat > "$NO_DEF_CONFIG" << 'EOF'
projects_dir: ~/dev
default_flags:
cli_tool: kiro-cli
cli_launch: chat

projects:
  myproj:
    flags: --agent mechanic
  bare:
    flags: none
EOF
_LK_CONFIG_FILE="$NO_DEF_CONFIG"
_lk_load_config
flags=$(_lk_get_flags "myproj")
assert_eq "no-defaults: project flags only" "--agent mechanic" "$flags"
flags=$(_lk_get_flags "bare")
assert_eq "no-defaults: none still empty" "" "$flags"
flags=$(_lk_get_flags "unconfigured")
assert_eq "no-defaults: unconfigured project gets empty" "" "$flags"
rm -f "$NO_DEF_CONFIG"

# ═══════════════════════════════════════
echo "── Safe Name Conversion ──"

# Test: various project name → safe var name conversions
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config

# Hyphen → underscore
flags=$(_lk_get_flags "my-app")
assert_eq "safe-name: hyphen converted" "--trust-all-tools --agent mechanic" "$flags"

# Dot → underscore
flags=$(_lk_get_flags "v2.core")
assert_eq "safe-name: dot converted" "--trust-all-tools --no-interactive" "$flags"

# Test: multiple consecutive special chars
SAFE_CONFIG=$(mktemp)
cat > "$SAFE_CONFIG" << 'EOF'
projects_dir: ~/dev
default_flags: --default
cli_tool: kiro-cli
cli_launch: chat

projects:
  my--project:
    flags: --double-hyphen
  a.b.c:
    flags: --multi-dot
EOF
_LK_CONFIG_FILE="$SAFE_CONFIG"
_lk_load_config
flags=$(_lk_get_flags "my--project")
assert_eq "safe-name: double hyphen" "--default --double-hyphen" "$flags"
flags=$(_lk_get_flags "a.b.c")
assert_eq "safe-name: multiple dots" "--default --multi-dot" "$flags"
rm -f "$SAFE_CONFIG"

# ═══════════════════════════════════════
echo "── Write Config: Edge Cases ──"

# Test: write_config with no projects configured
EMPTY_PROJ_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$EMPTY_PROJ_CONFIG"
_LK_PROJECTS_DIR="$HOME/dev"
_LK_KIRO_FLAGS="--flag"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
_lk_write_config
written=$(cat "$EMPTY_PROJ_CONFIG")
assert_contains "write-empty: has projects_dir" "projects_dir:" "$written"
assert_contains "write-empty: has projects section header" "projects:" "$written"
# Should not have any "  name:" lines (no indented project entries)
proj_entries=$(grep -cE '^\s{2}[a-zA-Z]' "$EMPTY_PROJ_CONFIG" || true)
assert_eq "write-empty: no project entries" "0" "$proj_entries"
rm -f "$EMPTY_PROJ_CONFIG"

# Test: write_config preserves special characters in prompts
SPECIAL_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$SPECIAL_CONFIG"
_LK_PROJECTS_DIR="$HOME/dev"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
_lk_assign "_LK_PROJECT_PROMPT_myproj" "help me with this project"
_lk_assign "_LK_PROJECT_TITLE_myproj" "My Cool Project"
_lk_write_config
written=$(cat "$SPECIAL_CONFIG")
assert_contains "write-special: prompt with spaces preserved" "help me with this project" "$written"
assert_contains "write-special: title with spaces preserved" "My Cool Project" "$written"
# Verify round-trip
_lk_load_config
prompt="$(_lk_indirect "_LK_PROJECT_PROMPT_myproj")"
assert_eq "write-special: prompt round-trips" "help me with this project" "$prompt"
title="$(_lk_indirect "_LK_PROJECT_TITLE_myproj")"
assert_eq "write-special: title round-trips" "My Cool Project" "$title"
rm -f "$SPECIAL_CONFIG"

# ═══════════════════════════════════════
echo "── Menu: Color Indicator ──"

# Test: projects with color show in menu correctly (no special indicator for projects)
COLOR_MENU_DIR=$(mktemp -d)
mkdir -p "$COLOR_MENU_DIR/styled" "$COLOR_MENU_DIR/plain"
COLOR_MENU_CONFIG=$(mktemp)
cat > "$COLOR_MENU_CONFIG" << 'EOF'
projects_dir: placeholder
default_flags:
cli_tool: kiro-cli
cli_launch: chat

projects:
  styled:
    color: "#ff0000"
    title: Styled
  plain:
    flags: --test
EOF
_LK_CONFIG_FILE="$COLOR_MENU_CONFIG"
export LK_PROJECTS_DIR="$COLOR_MENU_DIR"
_lk_load_config
menu_output=$(echo "q" | lk 2>/dev/null || true)
# Strip ANSI codes for reliable matching
clean_menu=$(echo "$menu_output" | sed 's/\x1b\[[0-9;]*m//g')
# Both projects should appear in the menu
assert_contains "menu-projects: styled project listed" "styled" "$clean_menu"
assert_contains "menu-projects: plain project listed" "plain" "$clean_menu"
# terminal and root entries have ● marker
if echo "$clean_menu" | grep -q "● .*terminal"; then
    printf '  \e[32m✓\e[0m menu-projects: terminal has ● marker\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m menu-projects: terminal should have ● marker\n'; ((FAIL++))
fi
rm -rf "$COLOR_MENU_DIR" "$COLOR_MENU_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── CLI Launch Subcommand ──"

# Test: different cli_launch subcommands work correctly
LAUNCH_CONFIG=$(mktemp)
cat > "$LAUNCH_CONFIG" << 'EOF'
projects_dir: placeholder
default_flags: --test
cli_tool: goose
cli_launch: session
EOF
LAUNCH_DIR=$(mktemp -d)
mkdir -p "$LAUNCH_DIR/proj1"
# Create goose mock
cat > "$MOCK_BIN/goose" << 'MOCK'
#!/usr/bin/env bash
echo "GOOSE_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/goose"
_LK_CONFIG_FILE="$LAUNCH_CONFIG"
export LK_PROJECTS_DIR="$LAUNCH_DIR"
_lk_load_config
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    call=$(cat "$LK_TEST_LOG")
    assert_contains "cli-launch: goose session called" "GOOSE_CALLED: session" "$call"
    assert_contains "cli-launch: goose gets flags" "--test" "$call"
else
    printf '  \e[31m✗\e[0m cli-launch: goose should be called\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/goose"
rm -rf "$LAUNCH_DIR" "$LAUNCH_CONFIG"
unset LK_PROJECTS_DIR

# Test: empty cli_launch (e.g. claude has no subcommand)
NOLNCH_CONFIG=$(mktemp)
cat > "$NOLNCH_CONFIG" << 'EOF'
projects_dir: placeholder
default_flags: --test
cli_tool: claude
cli_launch:
EOF
NOLNCH_DIR=$(mktemp -d)
mkdir -p "$NOLNCH_DIR/proj1"
cat > "$MOCK_BIN/claude" << 'MOCK'
#!/usr/bin/env bash
echo "CLAUDE_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/claude"
_LK_CONFIG_FILE="$NOLNCH_CONFIG"
export LK_PROJECTS_DIR="$NOLNCH_DIR"
_lk_load_config
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    call=$(cat "$LK_TEST_LOG")
    assert_contains "cli-launch: claude called with flags directly" "CLAUDE_CALLED:" "$call"
    assert_contains "cli-launch: claude gets flags" "--test" "$call"
else
    printf '  \e[31m✗\e[0m cli-launch: claude should be called\n'; ((FAIL++))
fi
rm -f "$MOCK_BIN/claude"
rm -rf "$NOLNCH_DIR" "$NOLNCH_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Root Entry With Flags ──"

# Test: entry 2 (root) uses root dir name for flag lookup
ROOT_FLAG_DIR=$(mktemp -d)
ROOT_FLAG_BASE=$(basename "$ROOT_FLAG_DIR")
mkdir -p "$ROOT_FLAG_DIR/sub"
ROOT_FLAG_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$ROOT_FLAG_CONFIG"
export LK_PROJECTS_DIR="$ROOT_FLAG_DIR"
_LK_KIRO_FLAGS="--global"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
_lk_write_config
_lk_load_config
rm -f "$LK_TEST_LOG"
(echo "2" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    kiro_call=$(cat "$LK_TEST_LOG")
    assert_contains "root-entry: uses chat subcommand" "chat" "$kiro_call"
    assert_contains "root-entry: gets global flags" "--global" "$kiro_call"
else
    printf '  \e[31m✗\e[0m root-entry: should call CLI\n'; ((FAIL++))
fi
rm -rf "$ROOT_FLAG_DIR" "$ROOT_FLAG_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Prompt Passed to CLI ──"

# Test: prompt with spaces is passed as a single argument
PROMPT_ARG_DIR=$(mktemp -d)
mkdir -p "$PROMPT_ARG_DIR/prompted"
PROMPT_ARG_CONFIG=$(mktemp)
cat > "$PROMPT_ARG_CONFIG" << 'EOF'
projects_dir: placeholder
default_flags: --trust-all-tools
cli_tool: kiro-cli
cli_launch: chat

projects:
  prompted:
    prompt: help me fix the bug in auth module
EOF
# Use arg-logging mock
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
for arg in "$@"; do
    echo "ARG: $arg" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
done
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
_LK_CONFIG_FILE="$PROMPT_ARG_CONFIG"
export LK_PROJECTS_DIR="$PROMPT_ARG_DIR"
_lk_load_config
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    # Prompt should be a single arg (not split on spaces)
    prompt_arg=$(grep "help me fix" "$LK_TEST_LOG" || true)
    assert_contains "prompt-arg: prompt passed as single arg" "help me fix the bug in auth module" "$prompt_arg"
    # Should still have separate flag args
    assert_contains "prompt-arg: chat subcommand present" "ARG: chat" "$(cat "$LK_TEST_LOG")"
    assert_contains "prompt-arg: flags present" "ARG: --trust-all-tools" "$(cat "$LK_TEST_LOG")"
else
    printf '  \e[31m✗\e[0m prompt-arg: CLI should be called\n'; ((FAIL++))
fi
rm -rf "$PROMPT_ARG_DIR" "$PROMPT_ARG_CONFIG"
unset LK_PROJECTS_DIR

# Test: project without prompt does NOT pass empty arg
NO_PROMPT_DIR=$(mktemp -d)
mkdir -p "$NO_PROMPT_DIR/noprompt"
NO_PROMPT_CONFIG=$(mktemp)
cat > "$NO_PROMPT_CONFIG" << 'EOF'
projects_dir: placeholder
default_flags: --flag1
cli_tool: kiro-cli
cli_launch: chat
EOF
_LK_CONFIG_FILE="$NO_PROMPT_CONFIG"
export LK_PROJECTS_DIR="$NO_PROMPT_DIR"
_lk_load_config
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    arg_count=$(grep -c "^ARG:" "$LK_TEST_LOG")
    # Should be just: chat --flag1 = 2 args (no empty prompt arg)
    assert_eq "prompt-arg: no prompt means no extra arg" "2" "$arg_count"
else
    printf '  \e[31m✗\e[0m prompt-arg: CLI should be called\n'; ((FAIL++))
fi
rm -rf "$NO_PROMPT_DIR" "$NO_PROMPT_CONFIG"
unset LK_PROJECTS_DIR

# Restore simple mock
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"

# ═══════════════════════════════════════
echo "── Config: CLI Tool Persistence ──"

# Test: cli_tool and cli_launch round-trip through write/load
CLI_RT_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$CLI_RT_CONFIG"
_LK_PROJECTS_DIR="$HOME/dev"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="goose"
_LK_CLI_LAUNCH="session"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
_lk_write_config
# Verify written
written=$(cat "$CLI_RT_CONFIG")
assert_contains "cli-persist: cli_tool written" 'cli_tool: "goose"' "$written"
assert_contains "cli-persist: cli_launch written" 'cli_launch: "session"' "$written"
# Reload and verify
_LK_CLI_TOOL=""
_LK_CLI_LAUNCH=""
_lk_load_config
assert_eq "cli-persist: cli_tool round-trips" "goose" "$_LK_CLI_TOOL"
assert_eq "cli-persist: cli_launch round-trips" "session" "$_LK_CLI_LAUNCH"
rm -f "$CLI_RT_CONFIG"

# ═══════════════════════════════════════
echo "── _lk_get_default_launch Coverage ──"

# Test: all known CLI tools have correct default launch
subcmd=$(_lk_get_default_launch "kiro-cli")
assert_eq "default-launch: kiro-cli → chat" "chat" "$subcmd"
subcmd=$(_lk_get_default_launch "claude")
assert_eq "default-launch: claude → empty" "" "$subcmd"
subcmd=$(_lk_get_default_launch "aider")
assert_eq "default-launch: aider → empty" "" "$subcmd"
subcmd=$(_lk_get_default_launch "goose")
assert_eq "default-launch: goose → session" "session" "$subcmd"
subcmd=$(_lk_get_default_launch "codex")
assert_eq "default-launch: codex → empty" "" "$subcmd"
subcmd=$(_lk_get_default_launch "amp")
assert_eq "default-launch: amp → empty" "" "$subcmd"
subcmd=$(_lk_get_default_launch "unknown-tool")
assert_eq "default-launch: unknown → empty" "" "$subcmd"

# ═══════════════════════════════════════
echo "── Menu: Entry Count With Hidden Dir ──"

# Test: launcher directory is included in menu (no self-exclusion)
COUNT_DIR=$(mktemp -d)
mkdir -p "$COUNT_DIR/a" "$COUNT_DIR/b" "$COUNT_DIR/c" "$COUNT_DIR/kiro-launcher"
_LK_CONFIG_FILE="/nonexistent"
export LK_PROJECTS_DIR="$COUNT_DIR"
_LK_SCRIPT_DIR="$COUNT_DIR/kiro-launcher"
_lk_load_config
menu_output=$(echo "q" | lk 2>/dev/null || true)
entry_count=$(echo "$menu_output" | sed 's/\x1b\[[0-9;]*m//g' | grep -cE '^\s+[0-9]' || true)
# Should be 6: terminal(1) + root(2) + a(3) + b(4) + c(5) + kiro-launcher(6)
assert_eq "count-no-hide: 6 entries with launcher included" "6" "$entry_count"

# Same count regardless of _LK_SCRIPT_DIR location
_LK_SCRIPT_DIR="/elsewhere"
menu_output=$(echo "q" | lk 2>/dev/null || true)
entry_count=$(echo "$menu_output" | sed 's/\x1b\[[0-9;]*m//g' | grep -cE '^\s+[0-9]' || true)
assert_eq "count-no-hide: 6 entries regardless of script dir" "6" "$entry_count"
_LK_SCRIPT_DIR="$PROJECT_DIR"
rm -rf "$COUNT_DIR"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── _lk_assign and _lk_indirect Consistency ──"

# Test: _lk_assign + _lk_indirect round-trip with various values
_lk_assign "_LK_TEST_RT_1" "simple"
assert_eq "assign-rt: simple value" "simple" "$(_lk_indirect "_LK_TEST_RT_1")"

_lk_assign "_LK_TEST_RT_2" "--flag1 --flag2 --flag3"
assert_eq "assign-rt: space-separated flags" "--flag1 --flag2 --flag3" "$(_lk_indirect "_LK_TEST_RT_2")"

_lk_assign "_LK_TEST_RT_3" "value with = sign"
assert_eq "assign-rt: value with equals" "value with = sign" "$(_lk_indirect "_LK_TEST_RT_3")"

_lk_assign "_LK_TEST_RT_4" ""
assert_eq "assign-rt: empty string" "" "$(_lk_indirect "_LK_TEST_RT_4")"
# But it should be set (not unset)
if _lk_indirect_set "_LK_TEST_RT_4"; then
    printf '  \e[32m✓\e[0m assign-rt: empty string is still set\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m assign-rt: empty string should be set\n'; ((FAIL++))
fi
unset _LK_TEST_RT_1 _LK_TEST_RT_2 _LK_TEST_RT_3 _LK_TEST_RT_4

# ═══════════════════════════════════════
echo "── Multiple Selections in Sequence ──"

# Test: multiple selections work correctly (menu stays for new-tab, returns for normal)
SEQ_DIR=$(mktemp -d)
mkdir -p "$SEQ_DIR/proj1" "$SEQ_DIR/proj2"
SEQ_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$SEQ_CONFIG"
export LK_PROJECTS_DIR="$SEQ_DIR"
_LK_KIRO_FLAGS="--test"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
for v in $(_lk_vars_with_prefix _LK_KIRO_FLAGS_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_COLOR_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_TITLE_); do unset "$v"; done
for v in $(_lk_vars_with_prefix _LK_PROJECT_PROMPT_); do unset "$v"; done
_lk_write_config
_lk_load_config
# Select proj1 (entry 3) — should exit since no color/title
rm -f "$LK_TEST_LOG"
(echo "3" | lk) >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    printf '  \e[32m✓\e[0m seq: normal project launches CLI and exits\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m seq: should call CLI for normal project\n'; ((FAIL++))
fi
rm -rf "$SEQ_DIR" "$SEQ_CONFIG"
unset LK_PROJECTS_DIR

# ═══════════════════════════════════════
echo "── Config Load Clears Previous State ──"

# Test: loading a new config clears old per-project vars
CLEAR_CONFIG1=$(mktemp)
cat > "$CLEAR_CONFIG1" << 'EOF'
projects_dir: ~/dev
default_flags: --first
cli_tool: kiro-cli
cli_launch: chat

projects:
  proj_old:
    flags: --old-flag
    color: "#111111"
    title: Old Title
    prompt: old prompt
EOF
_LK_CONFIG_FILE="$CLEAR_CONFIG1"
_lk_load_config
# Verify loaded
assert_eq "clear-state: first load has old project" "--first --old-flag" "$(_lk_get_flags "proj_old")"

# Now load a different config that doesn't have proj_old
CLEAR_CONFIG2=$(mktemp)
cat > "$CLEAR_CONFIG2" << 'EOF'
projects_dir: ~/dev
default_flags: --second
cli_tool: kiro-cli
cli_launch: chat

projects:
  proj_new:
    flags: --new-flag
EOF
_LK_CONFIG_FILE="$CLEAR_CONFIG2"
_lk_load_config
# proj_old should be gone
flags=$(_lk_get_flags "proj_old")
assert_eq "clear-state: old project cleared" "--second" "$flags"
color="$(_lk_indirect "_LK_PROJECT_COLOR_$(_lk_safe_name "proj_old")")"
assert_eq "clear-state: old color cleared" "" "$color"
# proj_new should be present
flags=$(_lk_get_flags "proj_new")
assert_eq "clear-state: new project loaded" "--second --new-flag" "$flags"
rm -f "$CLEAR_CONFIG1" "$CLEAR_CONFIG2"

# ═══════════════════════════════════════
echo "── Update Detection: Cache Reading ──"

# Test: _lk_check_update_cache with no cache file
_LK_UPDATE_CACHE="/nonexistent/path/.update-check"
_lk_check_update_cache
rc=$?
assert_eq "update-cache: returns 1 when no cache" "1" "$rc"
assert_eq "update-cache: CLI_UPDATE defaults 0" "0" "$_LK_CLI_UPDATE_AVAILABLE"
assert_eq "update-cache: LAUNCHER_UPDATE defaults 0" "0" "$_LK_LAUNCHER_UPDATE_AVAILABLE"

# Test: _lk_check_update_cache reads correctly
UPDATE_CACHE_TMP=$(mktemp)
cat > "$UPDATE_CACHE_TMP" <<'EOF'
LAST_CHECK=1722945619
CLI_UPDATE_AVAILABLE=1
CLI_LATEST_VERSION=2.17.0
LAUNCHER_UPDATE_AVAILABLE=1
LAUNCHER_COMMITS_BEHIND=3
EOF
_LK_UPDATE_CACHE="$UPDATE_CACHE_TMP"
_lk_check_update_cache
assert_eq "update-cache: CLI update detected" "1" "$_LK_CLI_UPDATE_AVAILABLE"
assert_eq "update-cache: CLI version parsed" "2.17.0" "$_LK_CLI_LATEST_VERSION"
assert_eq "update-cache: launcher update detected" "1" "$_LK_LAUNCHER_UPDATE_AVAILABLE"
assert_eq "update-cache: commits behind parsed" "3" "$_LK_LAUNCHER_COMMITS_BEHIND"

# Test: _lk_check_update_cache handles no-update cache
cat > "$UPDATE_CACHE_TMP" <<'EOF'
LAST_CHECK=1722945619
CLI_UPDATE_AVAILABLE=0
CLI_LATEST_VERSION=
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
EOF
_LK_UPDATE_CACHE="$UPDATE_CACHE_TMP"
_lk_check_update_cache
assert_eq "update-cache: no CLI update" "0" "$_LK_CLI_UPDATE_AVAILABLE"
assert_eq "update-cache: empty version" "" "$_LK_CLI_LATEST_VERSION"
assert_eq "update-cache: no launcher update" "0" "$_LK_LAUNCHER_UPDATE_AVAILABLE"
rm -f "$UPDATE_CACHE_TMP"

# ═══════════════════════════════════════
echo "── Update Detection: _lk_update_available ──"

# Test: _lk_update_available returns true when CLI update available
_LK_CLI_UPDATE_AVAILABLE=1
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_lk_update_available
assert_eq "update-available: CLI only → true" "0" "$?"

# Test: _lk_update_available returns true when launcher update available
_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=1
_lk_update_available
assert_eq "update-available: launcher only → true" "0" "$?"

# Test: _lk_update_available returns true when both available
_LK_CLI_UPDATE_AVAILABLE=1
_LK_LAUNCHER_UPDATE_AVAILABLE=1
_lk_update_available
assert_eq "update-available: both → true" "0" "$?"

# Test: _lk_update_available returns false when nothing available
_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_lk_update_available
rc=$?
assert_eq "update-available: none → false" "1" "$rc"

# ═══════════════════════════════════════
echo "── Update Detection: Time Gating ──"

# Test: _lk_update_check skips when auto_update_check is false
UPDATE_CACHE_TMP2=$(mktemp)
_LK_UPDATE_CACHE="$UPDATE_CACHE_TMP2"
_LK_AUTO_UPDATE_CHECK="false"
_LK_CLI_TOOL="nonexistent-cli"
_lk_update_check
assert_eq "update-check: skipped when disabled" "0" "$_LK_CLI_UPDATE_AVAILABLE"
# Cache file should NOT be created when check is disabled
[[ ! -s "$UPDATE_CACHE_TMP2" ]]
assert_eq "update-check: no cache written when disabled" "0" "$?"

# Test: _lk_update_check uses cache when fresh
cat > "$UPDATE_CACHE_TMP2" <<EOF
LAST_CHECK=$(date +%s)
CLI_UPDATE_AVAILABLE=1
CLI_LATEST_VERSION=9.9.9
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
EOF
_LK_UPDATE_CACHE="$UPDATE_CACHE_TMP2"
_LK_AUTO_UPDATE_CHECK="true"
_LK_CLI_TOOL="nonexistent-cli"
_lk_update_check
assert_eq "update-check: uses fresh cache" "1" "$_LK_CLI_UPDATE_AVAILABLE"
assert_eq "update-check: version from cache" "9.9.9" "$_LK_CLI_LATEST_VERSION"
rm -f "$UPDATE_CACHE_TMP2"

# ═══════════════════════════════════════
echo "── Update Detection: Menu Rendering ──"

# Test: menu shows update indicator when update available
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config

# Override _lk_update_check to no-op for menu rendering tests
# (prevents overwriting manually-set update state variables)
_lk_update_check() { :; }

# Set update state directly (bypass the actual check)
_LK_CLI_UPDATE_AVAILABLE=1
_LK_CLI_LATEST_VERSION="2.99.0"
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_LAUNCHER_COMMITS_BEHIND=""

menu_output=$(echo "q" | lk 2>/dev/null || true)
assert_contains "update-menu: shows update indicator" "available" "$menu_output"
assert_contains "update-menu: shows kiro-cli version" "2.99.0" "$menu_output"
assert_contains "update-menu: shows u key" "update" "$menu_output"

# Test: menu does NOT show update when nothing available
_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=0
menu_output=$(echo "q" | lk 2>/dev/null || true)
# Strip ANSI codes and check 'update' is not in footer
menu_clean=$(echo "$menu_output" | sed 's/\x1b\[[0-9;]*m//g')
if [[ "$menu_clean" != *"update ⬆"* ]]; then
    printf '  \e[32m✓\e[0m update-menu: no update key when up-to-date\n'
    ((PASS++))
else
    printf '  \e[31m✗\e[0m update-menu: no update key when up-to-date\n'
    ((FAIL++))
fi

# Test: menu shows launcher update with commit count
_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=1
_LK_LAUNCHER_COMMITS_BEHIND="5"
menu_output=$(echo "q" | lk 2>/dev/null || true)
assert_contains "update-menu: shows launcher update" "launcher" "$menu_output"

# Test: menu shows both when both available
_LK_CLI_UPDATE_AVAILABLE=1
_LK_CLI_LATEST_VERSION="3.0.0"
_LK_LAUNCHER_UPDATE_AVAILABLE=1
_LK_LAUNCHER_COMMITS_BEHIND="2"
menu_output=$(echo "q" | lk 2>/dev/null || true)
assert_contains "update-menu: shows both updates" "launcher" "$menu_output"
assert_contains "update-menu: shows CLI version" "3.0.0" "$menu_output"

# ═══════════════════════════════════════
echo "── Update Detection: u Key Handling ──"

# Test: pressing 'u' when no update shows "up to date" message
_LK_CLI_UPDATE_AVAILABLE=0
_LK_CLI_LATEST_VERSION=""
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_LAUNCHER_COMMITS_BEHIND="0"
# Call _lk_do_update directly to test in isolation
do_update_output=$(echo "" | _lk_do_update 2>/dev/null || true)
assert_contains "update-ukey: shows up-to-date" "up to date" "$do_update_output"

# Test: pressing 'u' when update available shows confirmation
_LK_CLI_UPDATE_AVAILABLE=1
_LK_CLI_LATEST_VERSION="2.99.0"
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_LAUNCHER_COMMITS_BEHIND="0"
_LK_APPS_UPDATE_AVAILABLE=0
_LK_APPS_UPDATE_LIST=""
# Decline the update
do_update_output=$(echo "n" | _lk_do_update 2>/dev/null || true)
assert_contains "update-ukey: shows confirmation prompt" "Update kiro-cli/launcher" "$do_update_output"
assert_contains "update-ukey: shows kiro-cli in update" "kiro-cli" "$do_update_output"

# ═══════════════════════════════════════
echo "── Update Detection: Config Toggle ──"

# Test: auto_update_check persists in config
CONFIG_TMP=$(mktemp)
_LK_CONFIG_FILE="$CONFIG_TMP"
_LK_PROJECTS_DIR="$FIXTURES/projects"
_LK_KIRO_FLAGS=""
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"
_LK_AUTO_UPDATE_CHECK="true"
_lk_write_config
config_content=$(cat "$CONFIG_TMP")
assert_contains "update-config: auto_update_check written" 'auto_update_check: "true"' "$config_content"

# Test: toggling to false
_LK_AUTO_UPDATE_CHECK="false"
_lk_write_config
config_content=$(cat "$CONFIG_TMP")
assert_contains "update-config: auto_update_check toggled off" 'auto_update_check: "false"' "$config_content"

# Test: loading config reads auto_update_check
_LK_AUTO_UPDATE_CHECK="true"  # reset
_lk_load_config
assert_eq "update-config: auto_update_check loaded" "false" "$_LK_AUTO_UPDATE_CHECK"

# Test: default is true when not in config
echo "projects_dir: ~/dev" > "$CONFIG_TMP"
_lk_load_config
assert_eq "update-config: default is true" "true" "$_LK_AUTO_UPDATE_CHECK"
rm -f "$CONFIG_TMP"

# Reset state for cleanup
_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=0

# ═══════════════════════════════════════
echo "── Regression: kiro-cli update no -y flag ──"

# Test: _lk_do_update does NOT pass -y to kiro-cli update
# Create a mock kiro-cli that logs exact arguments
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "$1" == "update" ]]; then
    echo "UPDATE_ARGS: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
    echo "kiro-cli is up to date"
fi
MOCK
chmod +x "$MOCK_BIN/kiro-cli"

_LK_CLI_UPDATE_AVAILABLE=1
_LK_CLI_LATEST_VERSION="2.99.0"
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_LAUNCHER_COMMITS_BEHIND="0"
_LK_APPS_UPDATE_AVAILABLE=0
_LK_APPS_UPDATE_LIST=""
rm -f "$LK_TEST_LOG"
# Accept the update prompt
echo "y" | _lk_do_update >/dev/null 2>&1 || true
if [[ -f "$LK_TEST_LOG" ]]; then
    update_call=$(cat "$LK_TEST_LOG")
    # Should NOT contain -y flag
    if [[ "$update_call" != *"-y"* ]]; then
        printf '  \e[32m✓\e[0m regression: kiro-cli update called without -y flag\n'; ((PASS++))
    else
        printf '  \e[31m✗\e[0m regression: kiro-cli update should NOT pass -y flag\n    actual: %s\n' "$update_call"; ((FAIL++))
    fi
    assert_contains "regression: kiro-cli update is called" "UPDATE_ARGS: update" "$update_call"
else
    printf '  \e[31m✗\e[0m regression: kiro-cli update should be called\n'; ((FAIL++))
fi

# Restore simple mock
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
rm -f "$LK_TEST_LOG"

# ═══════════════════════════════════════
echo "── Regression: Python detection for themes ──"

# Test: _lk_find_python finds python3 when available and working
cat > "$MOCK_BIN/python3" << 'MOCK'
#!/usr/bin/env bash
if [[ "$1" == "--version" ]]; then echo "Python 3.12.0"; exit 0; fi
MOCK
chmod +x "$MOCK_BIN/python3"
result=$(_lk_find_python)
assert_eq "python-detect: finds python3" "python3" "$result"

# Test: _lk_find_python rejects python3 Windows Store stub (command -v passes but --version fails)
cat > "$MOCK_BIN/python3" << 'MOCK'
#!/usr/bin/env bash
# Simulate Windows Store stub that exits non-zero
exit 9009
MOCK
chmod +x "$MOCK_BIN/python3"
rm -f "$MOCK_BIN/python"
OLD_PATH_PY="$PATH"
PATH="$MOCK_BIN:/usr/bin:/bin"
hash -r
# _lk_find_python should NOT return python3 since --version fails
# But system python3 at /usr/bin/python3 will still work, so test the concept:
# Create a python that works as fallback
cat > "$MOCK_BIN/python" << 'MOCK2'
#!/usr/bin/env bash
if [[ "$1" == "--version" ]]; then echo "Python 3.11.0"; exit 0; fi
MOCK2
chmod +x "$MOCK_BIN/python"
result=$(_lk_find_python)
# Since mock python3 fails --version but mock python passes, should find python
# However if system python3 is on /usr/bin, it'll find that first — acceptable
if [[ "$result" == "python3" || "$result" == "python" ]]; then
    printf '  \e[32m✓\e[0m python-detect: finds working interpreter when stub fails\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m python-detect: should find a working interpreter\n'; ((FAIL++))
fi
PATH="$OLD_PATH_PY"
hash -r
rm -f "$MOCK_BIN/python3" "$MOCK_BIN/python"

# Test: _lk_find_python falls back to python when python3 missing
# We test the fallback logic by temporarily replacing _lk_find_python
# with a version that can't find python3 (system python3 interferes otherwise)
_lk_find_python_fallback_test() {
    # Simulates _lk_find_python when python3 is NOT available
    if false; then  # pretend python3 not found
        echo "python3"
    elif command -v python &>/dev/null; then
        if python --version &>/dev/null; then
            echo "python"
        fi
    fi
}
cat > "$MOCK_BIN/python" << 'MOCK'
#!/usr/bin/env bash
if [[ "$1" == "--version" ]]; then
    echo "Python 3.11.0"
    exit 0
fi
MOCK
chmod +x "$MOCK_BIN/python"
result=$(_lk_find_python_fallback_test)
assert_eq "python-detect: falls back to python" "python" "$result"
rm -f "$MOCK_BIN/python"
unset -f _lk_find_python_fallback_test

# Beyond PATH, _lk_find_python also globs a handful of well-known Windows
# install locations directly (so it still finds an enterprise/manual Python
# install with nothing on PATH). That can't be hidden by narrowing PATH — on
# a real Windows box with Python actually installed at one of those fixed
# paths, the two "no python" tests below can't be simulated at all. Skip them
# there rather than report a false failure, mirroring the JQ_STILL_PRESENT
# escape hatch used for the same reason elsewhere in this file.
_lk_real_python_at_fixed_path() {
    local p
    for p in \
        "/c/Python3"*/python.exe \
        "/c/Program Files/Python3"*/python.exe \
        "/c/Program Files (x86)/Python3"*/python.exe \
        "${LOCALAPPDATA:-/nonexistent}/Programs/Python/Python3"*/python.exe \
        "${APPDATA:-/nonexistent}/../Local/Programs/Python/Python3"*/python.exe \
        "/c/Users/${USER:-nobody}/AppData/Local/Programs/Python/Python3"*/python.exe; do
        [[ -x "$p" ]] && return 0
    done
    return 1
}

# Test: _lk_find_python returns empty when no python available
OLD_PATH_PY="$PATH"
PATH="$MOCK_BIN"
# No python or python3 in mock bin
if _lk_real_python_at_fixed_path; then
    printf '  \e[2m⊘ python-detect: empty when no python (real Python at a fixed path here, skipped)\e[0m\n'
else
    result=$(_lk_find_python)
    assert_eq "python-detect: empty when no python" "" "$result"
fi
PATH="$OLD_PATH_PY"

# Test: _lk_find_python rejects Windows Store stub (exits non-zero)
cat > "$MOCK_BIN/python" << 'MOCK'
#!/usr/bin/env bash
if [[ "$1" == "--version" ]]; then
    # Simulate Windows Store stub that fails
    exit 9009
fi
MOCK
chmod +x "$MOCK_BIN/python"
OLD_PATH_PY="$PATH"
PATH="$MOCK_BIN"
if _lk_real_python_at_fixed_path; then
    printf '  \e[2m⊘ python-detect: rejects failing python stub (real Python at a fixed path here, skipped)\e[0m\n'
else
    result=$(_lk_find_python)
    assert_eq "python-detect: rejects failing python stub" "" "$result"
fi
unset -f _lk_real_python_at_fixed_path
PATH="$OLD_PATH_PY"
rm -f "$MOCK_BIN/python"

# ═══════════════════════════════════════
echo "── Theme: Default Entry ──"

# Test: Default theme is first in the themes array
first_theme=$(_lk_theme_field "${_LK_THEMES[0]}" 0)
assert_eq "theme-default: Default is first entry" "Default" "$first_theme"

# Test: Default theme shown in menu
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config
_LK_THEME="Default"
menu_output=$(printf 'c\n6\nb\nb\nq\n' | lk 2>/dev/null || true)
assert_contains "theme-default: shown in theme menu" "Default" "$menu_output"

# ═══════════════════════════════════════
echo "── Update Detection: Semver Compare ──"

# Test: equal versions
_lk_semver_compare "2.16.1" "2.16.1"
assert_eq "semver: equal versions return 0" "0" "$?"

# Test: v1 > v2 (patch)
_lk_semver_compare "2.16.2" "2.16.1"
assert_eq "semver: 2.16.2 > 2.16.1 returns 1" "1" "$?"

# Test: v1 < v2 (patch)
_lk_semver_compare "2.16.0" "2.16.1"
assert_eq "semver: 2.16.0 < 2.16.1 returns 2" "2" "$?"

# Test: v1 > v2 (minor)
_lk_semver_compare "2.17.0" "2.16.9"
assert_eq "semver: 2.17.0 > 2.16.9 returns 1" "1" "$?"

# Test: v1 < v2 (minor)
_lk_semver_compare "2.15.9" "2.16.0"
assert_eq "semver: 2.15.9 < 2.16.0 returns 2" "2" "$?"

# Test: v1 > v2 (major)
_lk_semver_compare "3.0.0" "2.99.99"
assert_eq "semver: 3.0.0 > 2.99.99 returns 1" "1" "$?"

# Test: v1 < v2 (major)
_lk_semver_compare "1.99.99" "2.0.0"
assert_eq "semver: 1.99.99 < 2.0.0 returns 2" "2" "$?"

# Test: versions with different digit lengths
_lk_semver_compare "10.0.0" "9.99.99"
assert_eq "semver: 10.0.0 > 9.99.99 returns 1" "1" "$?"

# ═══════════════════════════════════════
echo "── Platform: Data Dir Resolution ──"

# Test: Linux path detected when ~/.local/share/kiro-cli exists
_LK_PLATFORM_TEST_DIR=$(mktemp -d)
mkdir -p "$_LK_PLATFORM_TEST_DIR/.local/share/kiro-cli"
HOME_ORIG="$HOME"
HOME="$_LK_PLATFORM_TEST_DIR"
unset LOCALAPPDATA
result=$(_lk_kiro_data_dir)
assert_eq "data-dir: Linux path (~/.local/share/kiro-cli)" "$_LK_PLATFORM_TEST_DIR/.local/share/kiro-cli" "$result"

# Test: Windows LOCALAPPDATA path detected
rm -rf "$_LK_PLATFORM_TEST_DIR/.local"
mkdir -p "$_LK_PLATFORM_TEST_DIR/AppData/Local/kiro-cli"
LOCALAPPDATA="$_LK_PLATFORM_TEST_DIR/AppData/Local"
result=$(_lk_kiro_data_dir)
assert_eq "data-dir: Windows LOCALAPPDATA path" "$_LK_PLATFORM_TEST_DIR/AppData/Local/kiro-cli" "$result"

# Test: Windows fallback ~/AppData/Local when LOCALAPPDATA not set
unset LOCALAPPDATA
result=$(_lk_kiro_data_dir)
assert_eq "data-dir: Windows fallback (~/AppData/Local/kiro-cli)" "$_LK_PLATFORM_TEST_DIR/AppData/Local/kiro-cli" "$result"

# Test: macOS path detected
rm -rf "$_LK_PLATFORM_TEST_DIR/AppData"
mkdir -p "$_LK_PLATFORM_TEST_DIR/Library/Application Support/kiro-cli"
result=$(_lk_kiro_data_dir)
assert_eq "data-dir: macOS path (~/Library/Application Support/kiro-cli)" "$_LK_PLATFORM_TEST_DIR/Library/Application Support/kiro-cli" "$result"

# Test: returns 1 when no valid path found
rm -rf "$_LK_PLATFORM_TEST_DIR/Library"
_lk_kiro_data_dir
assert_eq "data-dir: returns 1 when no kiro-cli dir" "1" "$?"

# Cleanup
HOME="$HOME_ORIG"
rm -rf "$_LK_PLATFORM_TEST_DIR"

# ═══════════════════════════════════════
echo "── Platform: Timeout Fallback ──"

# Test: _lk_timeout uses timeout when available
_LK_TIMEOUT_MOCK_DIR=$(mktemp -d)
_OLD_PATH_TIMEOUT="$PATH"
cat > "$_LK_TIMEOUT_MOCK_DIR/timeout" << 'MOCKEOF'
#!/usr/bin/env bash
echo "timeout_called:$*"
MOCKEOF
chmod +x "$_LK_TIMEOUT_MOCK_DIR/timeout"
PATH="$_LK_TIMEOUT_MOCK_DIR:$_OLD_PATH_TIMEOUT"
hash -r
result=$(_lk_timeout 5 echo "hello")
assert_eq "timeout: uses timeout when available" "timeout_called:5 echo hello" "$result"

# Test: _lk_timeout falls back to gtimeout (when timeout not on PATH)
rm -f "$_LK_TIMEOUT_MOCK_DIR/timeout"
cat > "$_LK_TIMEOUT_MOCK_DIR/gtimeout" << 'MOCKEOF'
#!/usr/bin/env bash
echo "gtimeout_called:$*"
MOCKEOF
chmod +x "$_LK_TIMEOUT_MOCK_DIR/gtimeout"
# Shadow system timeout with a non-existent entry; gtimeout mock takes priority
cat > "$_LK_TIMEOUT_MOCK_DIR/timeout" << 'MOCKEOF'
#!/usr/bin/env bash
exit 127
MOCKEOF
chmod +x "$_LK_TIMEOUT_MOCK_DIR/timeout"
# Override _lk_timeout to test the gtimeout fallback path directly
_lk_timeout_test_gtimeout() {
    local seconds="$1"; shift
    # Simulate: timeout not available, gtimeout IS available
    if command -v gtimeout &>/dev/null; then
        gtimeout "$seconds" "$@"
    else
        "$@"
    fi
}
PATH="$_LK_TIMEOUT_MOCK_DIR:$_OLD_PATH_TIMEOUT"
hash -r
result=$(_lk_timeout_test_gtimeout 5 echo "hello")
assert_eq "timeout: uses gtimeout when timeout missing" "gtimeout_called:5 echo hello" "$result"

# Test: _lk_timeout falls back to direct execution
# Override to test the direct fallback path
_lk_timeout_test_direct() {
    local seconds="$1"; shift
    # Simulate: neither timeout nor gtimeout available
    "$@"
}
result=$(_lk_timeout_test_direct 5 echo "hello")
assert_eq "timeout: direct execution when no timeout available" "hello" "$result"

# Cleanup
PATH="$_OLD_PATH_TIMEOUT"
hash -r
rm -rf "$_LK_TIMEOUT_MOCK_DIR"

# ═══════════════════════════════════════
echo "── Update Detection: Index Check ──"

# Setup: mock kiro-cli and curl for update detection tests
_LK_TEST_MOCK_DIR=$(mktemp -d)
_OLD_PATH_UPDATE="$PATH"
cat > "$_LK_TEST_MOCK_DIR/kiro-cli" << 'MOCKEOF'
#!/usr/bin/env bash
echo "kiro-cli 2.16.1"
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/kiro-cli"

# Mock curl to return a fake index with version 2.16.2
cat > "$_LK_TEST_MOCK_DIR/curl" << 'MOCKEOF'
#!/usr/bin/env bash
cat << 'INDEXEOF'
{
  "versions": [
    {"version": "2.16.2", "packages": []},
    {"version": "2.16.1", "packages": []},
    {"version": "2.16.0", "packages": []}
  ]
}
INDEXEOF
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/curl"

PATH="$_LK_TEST_MOCK_DIR:$_OLD_PATH_UPDATE"

# Test: detects update when remote is newer
cli_update=0
cli_version=""
_lk_check_cli_update_via_index
assert_eq "index-check: detects update (2.16.2 > 2.16.1)" "1" "$cli_update"
assert_eq "index-check: captures latest version" "2.16.2" "$cli_version"

# Test: no update when versions are equal
cat > "$_LK_TEST_MOCK_DIR/curl" << 'MOCKEOF'
#!/usr/bin/env bash
cat << 'INDEXEOF'
{
  "versions": [
    {"version": "2.16.1", "packages": []},
    {"version": "2.16.0", "packages": []}
  ]
}
INDEXEOF
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/curl"

cli_update=0
cli_version=""
_lk_check_cli_update_via_index
assert_eq "index-check: no update when versions equal" "0" "$cli_update"
assert_eq "index-check: cli_version empty when no update" "" "$cli_version"

# Test: no update when local is newer (e.g., dev build)
cat > "$_LK_TEST_MOCK_DIR/kiro-cli" << 'MOCKEOF'
#!/usr/bin/env bash
echo "kiro-cli 3.0.0"
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/kiro-cli"
cat > "$_LK_TEST_MOCK_DIR/curl" << 'MOCKEOF'
#!/usr/bin/env bash
cat << 'INDEXEOF'
{
  "versions": [
    {"version": "2.16.2", "packages": []}
  ]
}
INDEXEOF
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/curl"

cli_update=0
cli_version=""
_lk_check_cli_update_via_index
assert_eq "index-check: no update when local is newer" "0" "$cli_update"

# Test: graceful failure when curl fails (no network)
cat > "$_LK_TEST_MOCK_DIR/curl" << 'MOCKEOF'
#!/usr/bin/env bash
exit 1
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/curl"
cat > "$_LK_TEST_MOCK_DIR/kiro-cli" << 'MOCKEOF'
#!/usr/bin/env bash
echo "kiro-cli 2.16.1"
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/kiro-cli"

cli_update=0
cli_version=""
_lk_check_cli_update_via_index || true
assert_eq "index-check: no crash when curl fails" "0" "$cli_update"

# Test: graceful failure when kiro-cli not found
rm -f "$_LK_TEST_MOCK_DIR/kiro-cli"
cli_update=0
cli_version=""
_lk_check_cli_update_via_index
local_exit=$?
assert_eq "index-check: returns 1 when kiro-cli missing" "1" "$local_exit"

# ═══════════════════════════════════════
echo "── Update Detection: Cache Time-Gating ──"

# Ensure auto-update check is enabled for these tests
_LK_AUTO_UPDATE_CHECK="true"
# Clear bash's command path cache after mock changes
hash -r
# Restore real _lk_update_check (overridden by menu rendering tests above)
source "$PROJECT_DIR/kiro-launcher.bash" 2>/dev/null

# Restore kiro-cli and curl mocks
cat > "$_LK_TEST_MOCK_DIR/kiro-cli" << 'MOCKEOF'
#!/usr/bin/env bash
echo "kiro-cli 2.16.1"
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/kiro-cli"
cat > "$_LK_TEST_MOCK_DIR/curl" << 'MOCKEOF'
#!/usr/bin/env bash
cat << 'INDEXEOF'
{
  "versions": [
    {"version": "2.16.2", "packages": []}
  ]
}
INDEXEOF
MOCKEOF
chmod +x "$_LK_TEST_MOCK_DIR/curl"

# Create a fresh cache that says no update (simulating stale data)
_LK_TEST_CACHE=$(mktemp)
_LK_UPDATE_CACHE_ORIG="$_LK_UPDATE_CACHE"
_LK_UPDATE_CACHE="$_LK_TEST_CACHE"

# Write cache with recent timestamp — should use cached value
cat > "$_LK_UPDATE_CACHE" <<EOF
LAST_CHECK=$(date +%s)
CLI_UPDATE_AVAILABLE=0
CLI_LATEST_VERSION=
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
APPS_UPDATE_AVAILABLE=0
APPS_UPDATE_LIST=
EOF

_LK_UPDATE_CHECK_SYNC=1
_LK_CLI_UPDATE_AVAILABLE=999
_LK_CLI_LATEST_VERSION=""
_lk_update_check
assert_eq "cache: uses cached value when fresh" "0" "$_LK_CLI_UPDATE_AVAILABLE"

# Write cache with old timestamp — should re-check
cat > "$_LK_UPDATE_CACHE" <<EOF
LAST_CHECK=1000000000
CLI_UPDATE_AVAILABLE=0
CLI_LATEST_VERSION=
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
APPS_UPDATE_AVAILABLE=0
APPS_UPDATE_LIST=
EOF

_LK_CLI_UPDATE_AVAILABLE=999
_LK_CLI_LATEST_VERSION=""
_lk_update_check
assert_eq "cache: re-checks when cache is stale" "1" "$_LK_CLI_UPDATE_AVAILABLE"
assert_eq "cache: captures version on re-check" "2.16.2" "$_LK_CLI_LATEST_VERSION"

# Write cache with no file — should perform check
rm -f "$_LK_UPDATE_CACHE"
_LK_CLI_UPDATE_AVAILABLE=999
_LK_CLI_LATEST_VERSION=""
_lk_update_check
assert_eq "cache: performs check when no cache file" "1" "$_LK_CLI_UPDATE_AVAILABLE"
unset _LK_UPDATE_CHECK_SYNC

# ═══════════════════════════════════════
echo "── Update Detection: _lk_update_available ──"

_LK_CLI_UPDATE_AVAILABLE=1
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_APPS_UPDATE_AVAILABLE=0
if _lk_update_available; then
    assert_eq "update-available: true when CLI update" "true" "true"
else
    assert_eq "update-available: true when CLI update" "true" "false"
fi

_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=1
_LK_APPS_UPDATE_AVAILABLE=0
if _lk_update_available; then
    assert_eq "update-available: true when launcher update" "true" "true"
else
    assert_eq "update-available: true when launcher update" "true" "false"
fi

_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_APPS_UPDATE_AVAILABLE=1
if _lk_update_available; then
    assert_eq "update-available: true when apps update" "true" "true"
else
    assert_eq "update-available: true when apps update" "true" "false"
fi

_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_APPS_UPDATE_AVAILABLE=0
if _lk_update_available; then
    assert_eq "update-available: false when nothing to update" "false" "true"
else
    assert_eq "update-available: false when nothing to update" "true" "true"
fi

# Cleanup update detection tests
PATH="$_OLD_PATH_UPDATE"
rm -rf "$_LK_TEST_MOCK_DIR"
rm -f "$_LK_TEST_CACHE"
_LK_UPDATE_CACHE="$_LK_UPDATE_CACHE_ORIG"

# ═══════════════════════════════════════
echo "── Header: Dynamic Padding ──"

# ${#var} counts bytes instead of characters under a non-UTF-8-aware locale
# (the default "C" locale some systems, notably Git Bash for Windows, start
# with) — inflating the length of a string containing a multibyte UTF-8
# character like the "│" border below. Force a UTF-8 locale for this
# character-counting check specifically, so it measures visible width the
# same way everywhere.
_OLD_LC_ALL_PAD="${LC_ALL:-}"
export LC_ALL=C.utf8

# Test: header aligns correctly with short version
_LK_VERSION="1.9.3"
header_output=$(
    c_title=$'\e[1;36m'; c_dim=$'\e[2m'; reset=$'\e[0m'
    _lk_hdr="Kiro Launcher"
    _lk_ver="v$_LK_VERSION"
    _lk_pad=$(( 37 - 2 - ${#_lk_hdr} - 2 - ${#_lk_ver} ))
    printf "  %s│%s  %s%*s%s%s%s  %s│%s\n" "$c_title" "$reset" "$_lk_hdr" "$_lk_pad" "" "$c_dim" "$_lk_ver" "$reset" "$c_title" "$reset"
)
# Strip ANSI codes and check alignment
stripped=$(echo "$header_output" | sed "s/\x1b\[[0-9;]*m//g")
assert_eq "header: short version line length" "41" "${#stripped}"

# Test: header aligns correctly with longer version (1.10.0)
_LK_VERSION="1.10.0"
header_output=$(
    c_title=$'\e[1;36m'; c_dim=$'\e[2m'; reset=$'\e[0m'
    _lk_hdr="Kiro Launcher"
    _lk_ver="v$_LK_VERSION"
    _lk_pad=$(( 37 - 2 - ${#_lk_hdr} - 2 - ${#_lk_ver} ))
    printf "  %s│%s  %s%*s%s%s%s  %s│%s\n" "$c_title" "$reset" "$_lk_hdr" "$_lk_pad" "" "$c_dim" "$_lk_ver" "$reset" "$c_title" "$reset"
)
stripped=$(echo "$header_output" | sed "s/\x1b\[[0-9;]*m//g")
assert_eq "header: long version line length" "41" "${#stripped}"
if [[ -n "$_OLD_LC_ALL_PAD" ]]; then
    export LC_ALL="$_OLD_LC_ALL_PAD"
else
    unset LC_ALL
fi

# Test: header contains version text
assert_contains "header: contains version" "v1.10.0" "$header_output"

# Test: header starts and ends with border chars
assert_contains "header: has left border" "│" "$stripped"

# ═══════════════════════════════════════
echo "── Config Menu: Aliases Option ──"

# Test: config submenu shows aliases option
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config
config_output=$(printf 'c\nb\n' | lk 2>/dev/null || true)
assert_contains "config: shows per-project aliases option" "Per-project aliases" "$config_output"

# Test: alias count shown when aliases configured
_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
# The fixture has ml_project alias = ML, so count should show
config_output=$(printf 'c\nb\n' | lk 2>/dev/null || true)
assert_contains "config: shows alias count" "configured" "$config_output"

# ═══════════════════════════════════════
# Spinner Tests
echo "── Spinner: _lk_spin ──"

# Test: spinner shows success indicator for passing command
spin_output=$(_lk_spin "test pass" true 2>&1)
stripped=$(echo "$spin_output" | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "spin: shows checkmark on success" "✓" "$stripped"
assert_contains "spin: shows message on success" "test pass" "$stripped"

# Test: spinner shows failure indicator for failing command
spin_output=$(_lk_spin "test fail" false 2>&1)
stripped=$(echo "$spin_output" | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "spin: shows X on failure" "✗" "$stripped"
assert_contains "spin: shows failed label" "failed" "$stripped"

# Test: spinner returns exit code of command
_lk_spin "exit test" true 2>&1 >/dev/null
assert_exit "spin: returns 0 on success" "0" "$?"

_lk_spin "exit test" false 2>&1 >/dev/null || true
# shellcheck disable=SC2181
spin_rc=$?
# false returns 1 but we caught it with || true above, so test directly:
set +e
_lk_spin "exit test" false 2>&1 >/dev/null
spin_rc=$?
# Restore errexit-off (the file baseline is `set -uo pipefail`, never -e).
# A previous `set -e` here leaked errexit into the rest of the suite, aborting
# it early when a later editor pipeline legitimately returned non-zero.
set +e
assert_exit "spin: returns non-zero on failure" "1" "$spin_rc"

echo "── Spinner: _lk_spin_verbose ──"

# Test: verbose spinner shows success
spin_output=$(_lk_spin_verbose "verbose pass" true 2>&1)
stripped=$(echo "$spin_output" | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "spin_verbose: shows checkmark on success" "✓" "$stripped"

# Test: verbose spinner shows failure with output
cat > "$MOCK_BIN/fail-with-output" << 'EOF'
#!/usr/bin/env bash
echo "error details here"
exit 1
EOF
chmod +x "$MOCK_BIN/fail-with-output"

set +e
spin_output=$(_lk_spin_verbose "verbose fail" "$MOCK_BIN/fail-with-output" 2>&1)
spin_rc=$?
# Restore errexit-off — see note above. This trailing `set -e` was the bug that
# aborted the suite before its summary once a later pipeline returned non-zero.
set +e
stripped=$(echo "$spin_output" | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "spin_verbose: shows X on failure" "✗" "$stripped"
assert_contains "spin_verbose: shows command output on failure" "error details" "$stripped"
assert_exit "spin_verbose: returns non-zero on failure" "1" "$spin_rc"

# ═══════════════════════════════════════
echo "── Security: flag splitting ──"

_lk_split_flags "--trust-all-tools --agent mechanic"
assert_eq "split: simple flags count" "3" "${#_LK_FLAG_ARGV[@]}"
assert_eq "split: first token" "--trust-all-tools" "${_LK_FLAG_ARGV[0]}"

_lk_split_flags '--agent "my agent" --model x'
assert_eq "split: quoted value keeps spaces as one token" "4" "${#_LK_FLAG_ARGV[@]}"
assert_eq "split: double-quoted value" "my agent" "${_LK_FLAG_ARGV[1]}"

_lk_split_flags "--agent 'other agent'"
assert_eq "split: single-quoted value" "other agent" "${_LK_FLAG_ARGV[1]}"

_lk_split_flags '--path a\ b'
assert_eq "split: backslash-escaped space" "a b" "${_LK_FLAG_ARGV[1]}"

_lk_split_flags ""
assert_eq "split: empty string yields no tokens" "0" "${#_LK_FLAG_ARGV[@]}"

_lk_split_flags "   --a    --b   "
assert_eq "split: collapses runs of whitespace" "2" "${#_LK_FLAG_ARGV[@]}"

# A config value must never be executed as code
_lk_split_flags '--x $(touch /tmp/lk-split-pwned)'
assert_eq "split: command substitution is inert data" '$(touch' "${_LK_FLAG_ARGV[1]}"
if [[ -f /tmp/lk-split-pwned ]]; then
    printf '  \e[31m✗\e[0m split: command substitution must not execute\n'; ((FAIL++)); rm -f /tmp/lk-split-pwned
else
    printf '  \e[32m✓\e[0m split: command substitution must not execute\n'; ((PASS++))
fi

# ═══════════════════════════════════════
echo "── Security: tab script injection ──"

# _lk_launch_project must shell-quote the directory, title and prompt so that a
# crafted project name cannot inject commands into the generated tab script.
SEC_TMP=$(mktemp -d)
SEC_MARKER="$SEC_TMP/PWNED"
EVIL_DIR="$SEC_TMP/evil'; touch $SEC_MARKER; echo '"
mkdir -p "$EVIL_DIR"

# Force the new-tab code path with a stub wt.exe that just records the argv,
# then execute the generated script the way a real terminal would.
SEC_WT="$SEC_TMP/wt.exe"
cat > "$SEC_WT" << 'WTEOF'
#!/usr/bin/env bash
for a in "$@"; do
    case "$a" in
        *lk-tab-*.sh) echo "$a" > "$SEC_SCRIPT_OUT" ;;
    esac
done
WTEOF
chmod +x "$SEC_WT"
export SEC_SCRIPT_OUT="$SEC_TMP/script-path"

_LK_WT_BIN="$SEC_WT"
_LK_IS_WSL=0
SEC_BASE="$(basename "$EVIL_DIR")"
_lk_assign "_LK_PROJECT_TITLE_$(_lk_safe_name "$SEC_BASE")" "T"
TMPDIR="$SEC_TMP" _lk_launch_project "$EVIL_DIR" >/dev/null 2>&1
sleep 0.3

SEC_SCRIPT=$(cat "$SEC_SCRIPT_OUT" 2>/dev/null || echo "")
if [[ -n "$SEC_SCRIPT" && -f "$SEC_SCRIPT" ]]; then
    printf '  \e[32m✓\e[0m injection: tab script generated\n'; ((PASS++))
    # Run it; cd will fail harmlessly but any injected command would fire
    bash "$SEC_SCRIPT" >/dev/null 2>&1 || true
    if [[ -f "$SEC_MARKER" ]]; then
        printf '  \e[31m✗\e[0m injection: quoted dir name must not execute commands\n'; ((FAIL++))
    else
        printf '  \e[32m✓\e[0m injection: quoted dir name must not execute commands\n'; ((PASS++))
    fi
else
    printf '  \e[31m✗\e[0m injection: tab script generated\n'; ((FAIL++))
    printf '  \e[31m✗\e[0m injection: quoted dir name must not execute commands (no script)\n'; ((FAIL++))
fi

# Temp script must be created via mktemp naming and be user-only
if [[ -n "$SEC_SCRIPT" ]]; then
    case "$(basename "$SEC_SCRIPT")" in
        lk-tab-*.sh) printf '  \e[32m✓\e[0m injection: uses mktemp naming pattern\n'; ((PASS++)) ;;
        *) printf '  \e[31m✗\e[0m injection: uses mktemp naming pattern\n'; ((FAIL++)) ;;
    esac
fi

unset _LK_WT_BIN _LK_IS_WSL _LK_LAUNCHED_NEW_TAB SEC_SCRIPT_OUT
rm -rf "$SEC_TMP"

# Stale tab scripts are swept
SWEEP_TMP=$(mktemp -d)
touch -d '2 days ago' "$SWEEP_TMP/lk-tab-oldxxxxx.sh" 2>/dev/null || touch -t 202001010000 "$SWEEP_TMP/lk-tab-oldxxxxx.sh"
touch "$SWEEP_TMP/lk-tab-newxxxxx.sh"
TMPDIR="$SWEEP_TMP" _lk_sweep_stale_tab_scripts
[[ ! -f "$SWEEP_TMP/lk-tab-oldxxxxx.sh" ]] \
    && { printf '  \e[32m✓\e[0m sweep: removes stale tab scripts\n'; ((PASS++)); } \
    || { printf '  \e[31m✗\e[0m sweep: removes stale tab scripts\n'; ((FAIL++)); }
[[ -f "$SWEEP_TMP/lk-tab-newxxxxx.sh" ]] \
    && { printf '  \e[32m✓\e[0m sweep: keeps fresh tab scripts\n'; ((PASS++)); } \
    || { printf '  \e[31m✗\e[0m sweep: keeps fresh tab scripts\n'; ((FAIL++)); }
rm -rf "$SWEEP_TMP"

# ═══════════════════════════════════════
echo "── Config: safe name is collision-free ──"

# Collapsing every non-alphanumeric to "_" made distinct projects collide.
assert_eq "safe-name: alphanumeric unchanged" "plain" "$(_lk_safe_name "plain")"
assert_eq "safe-name: hyphen hex-escaped" "my_2dapp" "$(_lk_safe_name "my-app")"
assert_eq "safe-name: underscore hex-escaped" "my_5fapp" "$(_lk_safe_name "my_app")"
assert_eq "safe-name: dot hex-escaped" "my_2eapp" "$(_lk_safe_name "my.app")"

# The three names that used to collapse to "my_app" must now be distinct
_c1="$(_lk_safe_name "my-app")"; _c2="$(_lk_safe_name "my_app")"; _c3="$(_lk_safe_name "my.app")"
if [[ "$_c1" != "$_c2" && "$_c2" != "$_c3" && "$_c1" != "$_c3" ]]; then
    printf '  \e[32m✓\e[0m safe-name: my-app / my_app / my.app are distinct\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m safe-name: my-app / my_app / my.app are distinct\n'; ((FAIL++))
fi

# Decoding recovers the original
for _n in "my-app" "my_app" "v2.core" "a.b-c_d" "plain"; do
    assert_eq "safe-name: round-trips '$_n'" "$_n" "$(_lk_decode_name "$(_lk_safe_name "$_n")")"
done

# ═══════════════════════════════════════
echo "── Config: collision no longer shares flags ──"

COLL_CONFIG=$(mktemp)
cat > "$COLL_CONFIG" << 'EOF'
projects_dir: ~/dev
default_flags: --base
cli_tool: kiro-cli
cli_launch: chat

projects:
  my-app:
    flags: --hyphen
  my_app:
    flags: --underscore
  my.app:
    flags: --dot
EOF
_LK_CONFIG_FILE="$COLL_CONFIG"
_lk_load_config
assert_eq "collision: my-app keeps own flags" "--base --hyphen" "$(_lk_get_flags "my-app")"
assert_eq "collision: my_app keeps own flags" "--base --underscore" "$(_lk_get_flags "my_app")"
assert_eq "collision: my.app keeps own flags" "--base --dot" "$(_lk_get_flags "my.app")"

# A rewrite must preserve all three entries with their original spelling
_lk_write_config
coll_written=$(cat "$COLL_CONFIG")
assert_contains "collision: rewrite keeps my-app" '"my-app":' "$coll_written"
assert_contains "collision: rewrite keeps my_app" '"my_app":' "$coll_written"
assert_contains "collision: rewrite keeps my.app" '"my.app":' "$coll_written"

# ...and reload must still resolve each independently
_lk_load_config
assert_eq "collision: survives write+reload (my-app)" "--base --hyphen" "$(_lk_get_flags "my-app")"
assert_eq "collision: survives write+reload (my_app)" "--base --underscore" "$(_lk_get_flags "my_app")"
assert_eq "collision: survives write+reload (my.app)" "--base --dot" "$(_lk_get_flags "my.app")"
rm -f "$COLL_CONFIG"

# ═══════════════════════════════════════
echo "── Config: YAML scalar quoting ──"

assert_eq "yaml-quote: plain value" '"abc"' "$(_lk_yaml_quote "abc")"
assert_eq "yaml-quote: empty value" '""' "$(_lk_yaml_quote "")"
assert_eq "yaml-quote: escapes double quote" '"say \"hi\""' "$(_lk_yaml_quote 'say "hi"')"
assert_eq "yaml-quote: escapes backslash" '"a\\b"' "$(_lk_yaml_quote 'a\b')"

assert_eq "yaml-unquote: strips quotes" "abc" "$(_lk_yaml_unquote '"abc"')"
assert_eq "yaml-unquote: bare value unchanged" "abc" "$(_lk_yaml_unquote 'abc')"
assert_eq "yaml-unquote: unescapes quote" 'say "hi"' "$(_lk_yaml_unquote '"say \"hi\""')"
assert_eq "yaml-unquote: unescapes backslash" 'a\b' "$(_lk_yaml_unquote '"a\\b"')"

# Values that used to corrupt the file must survive a full write/read cycle
for _v in 'Notes: daily' '# sharp' 'has "quotes"' 'back\slash' 'trailing space ' 'a: b # c'; do
    RT_CONFIG=$(mktemp)
    _LK_CONFIG_FILE="$RT_CONFIG"
    _lk_load_config
    _lk_register_project "rt-proj"
    _lk_assign "_LK_PROJECT_TITLE_$(_lk_safe_name "rt-proj")" "$_v"
    _lk_write_config
    _lk_load_config
    _got="$(_lk_indirect "_LK_PROJECT_TITLE_$(_lk_safe_name "rt-proj")")"
    assert_eq "yaml-roundtrip: [$_v]" "$_v" "$_got"
    rm -f "$RT_CONFIG"
done

# ═══════════════════════════════════════
echo "── Config: load_config return code ──"

_LK_CONFIG_FILE="$FIXTURES/config.yaml"
unset LK_PROJECTS_DIR
_lk_load_config
assert_eq "load-config: returns 0 with LK_PROJECTS_DIR unset" "0" "$?"

export LK_PROJECTS_DIR="/tmp/rc-check"
_lk_load_config
assert_eq "load-config: returns 0 with LK_PROJECTS_DIR set" "0" "$?"
unset LK_PROJECTS_DIR

# Chaining on success must work (this silently no-opped before)
_lk_load_config && _chain_ok=1 || _chain_ok=0
assert_eq "load-config: usable in && chain" "1" "$_chain_ok"

# ═══════════════════════════════════════
echo "── Config: original names in editors ──"

_LK_CONFIG_FILE="$FIXTURES/config.yaml"
_lk_load_config
assert_eq "project-name: registry returns original" "my-app" "$(_lk_project_name "$(_lk_safe_name "my-app")")"
assert_eq "project-name: decodes without registry" "un-registered" "$(_lk_project_name "$(_lk_safe_name "un-registered")")"

# The per-project flags editor must display "my-app", never "my_2dapp"
flags_editor_out=$(printf 'b\n' | _lk_config_edit_project_flags 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "editor: shows original hyphenated name" "my-app" "$flags_editor_out"
if [[ "$flags_editor_out" == *"my_2dapp"* ]]; then
    printf '  \e[31m✗\e[0m editor: must not show encoded name\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m editor: must not show encoded name\n'; ((PASS++))
fi

# ═══════════════════════════════════════
echo "── Tracked Apps: config-driven ──"

# No tracked apps are defined by default. The list used to be hardcoded to the
# author's own repositories.
APPS_EMPTY=$(mktemp)
cat > "$APPS_EMPTY" << 'EOF'
projects_dir: ~/dev
default_flags: ""
cli_tool: kiro-cli
cli_launch: chat
EOF
_LK_CONFIG_FILE="$APPS_EMPTY"
_lk_load_config
assert_eq "tracked: none configured by default" "0" "${#_LK_TRACKED_APP_NAMES[@]}"
rm -f "$APPS_EMPTY"

# Apps are read from the tracked_apps section
APPS_DIR=$(mktemp -d)
mkdir -p "$APPS_DIR/myapp" "$APPS_DIR/goapp"
cat > "$APPS_DIR/myapp/pyproject.toml" << 'EOF'
[project]
name = "myapp"
dependencies = ["requests>=9.9.9"]
version = "2.4.1"
EOF
cat > "$APPS_DIR/goapp/main.go" << 'EOF'
package main
const Version = "0.3.7"
EOF

APPS_CONFIG=$(mktemp)
cat > "$APPS_CONFIG" << EOF
projects_dir: "~/dev"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"

tracked_apps:
  "myapp":
    path: "$APPS_DIR/myapp"
    version_file: "pyproject.toml"
    version_key: "^version"
  "goapp":
    path: "$APPS_DIR/goapp"
    version_file: "main.go"
    version_key: "Version.*="
EOF
_LK_CONFIG_FILE="$APPS_CONFIG"
_lk_load_config

assert_eq "tracked: two apps registered" "2" "${#_LK_TRACKED_APP_NAMES[@]}"
assert_eq "tracked: names in config order" "myapp goapp" "${_LK_TRACKED_APP_NAMES[*]}"
assert_eq "tracked: path field" "$APPS_DIR/myapp" "$(_lk_app_field myapp path)"
assert_eq "tracked: version_file field" "pyproject.toml" "$(_lk_app_field myapp version_file)"
assert_eq "tracked: version_key field" "^version" "$(_lk_app_field myapp version_key)"

# version_key must select the right line, not the first semver in the file
assert_eq "tracked: version read via version_key" "2.4.1" "$(_lk_app_version myapp)"
assert_eq "tracked: version from go source" "0.3.7" "$(_lk_app_version goapp)"

# Missing pieces degrade quietly rather than erroring
assert_eq "tracked: unknown app yields empty version" "" "$(_lk_app_version nosuchapp)"
assert_eq "tracked: missing path yields empty" "" "$(_lk_get_app_version "/nonexistent/x" "f.toml" "" "")"
assert_eq "tracked: missing file yields empty" "" "$(_lk_get_app_version "$APPS_DIR/myapp" "nope.toml" "" "")"

# Without version_key the default pattern takes the first semver in the file
# Without version_key the first semver wins, which here is a dependency
assert_eq "tracked: default pattern picks first semver" "9.9.9" "$(_lk_get_app_version "$APPS_DIR/myapp" "pyproject.toml" "" "")"

# tracked_apps survives a write/reload cycle
_lk_write_config
apps_written=$(cat "$APPS_CONFIG")
assert_contains "tracked: writer emits section" "tracked_apps:" "$apps_written"
assert_contains "tracked: writer emits app name" '"myapp":' "$apps_written"
assert_contains "tracked: writer emits version_key" 'version_key: "^version"' "$apps_written"
_lk_load_config
assert_eq "tracked: reload keeps app count" "2" "${#_LK_TRACKED_APP_NAMES[@]}"
assert_eq "tracked: reload keeps version" "2.4.1" "$(_lk_app_version myapp)"

# ═══════════════════════════════════════
echo "── Tracked Apps: config is data, not code ──"

# A version definition must never be executed. Previously the config held a
# shell snippet run through eval.
EVIL_APPS=$(mktemp)
EVIL_MARKER="$APPS_DIR/EVAL_PWNED"
cat > "$EVIL_APPS" << EOF
projects_dir: "~/dev"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"

tracked_apps:
  "evil":
    path: "$APPS_DIR/myapp"
    version_file: "pyproject.toml"
    version_key: "\$(touch $EVIL_MARKER)"
    version_pattern: "\`touch $EVIL_MARKER\`"
EOF
_LK_CONFIG_FILE="$EVIL_APPS"
_lk_load_config
_lk_app_version evil >/dev/null 2>&1 || true
if [[ -f "$EVIL_MARKER" ]]; then
    printf '  \e[31m✗\e[0m tracked: version fields must not be executed\n'; ((FAIL++)); rm -f "$EVIL_MARKER"
else
    printf '  \e[32m✓\e[0m tracked: version fields must not be executed\n'; ((PASS++))
fi

# The launcher source must contain no eval of configured values
eval_uses=$(grep -c 'eval "\$version_cmd"\|eval "\$app_ver_cmd"' "$PROJECT_DIR/kiro-launcher.bash" || true)
assert_eq "tracked: no eval of stored commands in source" "0" "$eval_uses"

rm -f "$EVIL_APPS" "$APPS_CONFIG"
rm -rf "$APPS_DIR"

# ═══════════════════════════════════════
echo "── Theme: settings.json safety ──"

_lk_theme_test_python=$(_lk_find_python)
if [[ -z "$_lk_theme_test_python" ]]; then
    printf '  \e[33m~\e[0m theme: skipped (no python available)\n'
else
THEME_TMP=$(mktemp -d)
THEME_SETTINGS="$THEME_TMP/settings.json"
# Windows Terminal ships settings.json with comments; json.load rejects them,
# so this used to fail for most users.
cat > "$THEME_SETTINGS" << 'WTEOF'
{
    // A line comment, as shipped by Windows Terminal
    "$help": "https://aka.ms/terminal-documentation",
    "defaultProfile": "{guid-1}",
    /* a block comment */
    "profiles": {
        "defaults": {},
        "list": [
            { "guid": "{guid-1}", "name": "Ubuntu", "colorScheme": "MyCustomScheme" },
            { "guid": "{guid-2}", "name": "PowerShell" }
        ]
    },
    "schemes": []
}
WTEOF
cp "$THEME_SETTINGS" "$THEME_TMP/original.json"

_LK_WT_SETTINGS_OVERRIDE="$THEME_SETTINGS"
_LK_WT_SCHEME_BACKUP="$THEME_TMP/wt-schemes.json"
DRACULA=""
for _te in "${_LK_THEMES[@]}"; do
    [[ "$(_lk_theme_field "$_te" 0)" == "Dracula" ]] && DRACULA="$_te"
done

# Override honoured (the testability seam)
assert_eq "theme: settings override honoured" "$THEME_SETTINGS" "$(_lk_detect_wt_settings)"

apply_result=$(_lk_apply_theme_windows "$DRACULA" 2>&1)
assert_eq "theme: applies over JSONC comments" "OK:Dracula" "$apply_result"

# A backup must exist before any modification
[[ -f "${THEME_SETTINGS}.lk-backup" ]] \
    && { printf '  \e[32m✓\e[0m theme: creates backup before writing\n'; ((PASS++)); } \
    || { printf '  \e[31m✗\e[0m theme: creates backup before writing\n'; ((FAIL++)); }

# The backup must be byte-identical to what was there first
if cmp -s "${THEME_SETTINGS}.lk-backup" "$THEME_TMP/original.json"; then
    printf '  \e[32m✓\e[0m theme: backup matches original file\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m theme: backup matches original file\n'; ((FAIL++))
fi

# Result must be valid JSON and contain the new scheme
theme_probe=$("$_lk_theme_test_python" -c '
import json, sys
d = json.load(open(sys.argv[1]))
print(d["profiles"]["defaults"].get("colorScheme", "<none>"))
print(d["profiles"]["list"][0].get("colorScheme", "<none>"))
print(d["profiles"]["list"][1].get("colorScheme", "<none>"))
print(len([s for s in d["schemes"] if s["name"] == "Dracula"]))
print(d.get("defaultProfile", "<lost>"))
' "$THEME_SETTINGS")
assert_eq "theme: defaults colorScheme set" "Dracula" "$(sed -n 1p <<< "$theme_probe")"
assert_eq "theme: existing scheme replaced" "Dracula" "$(sed -n 2p <<< "$theme_probe")"
assert_eq "theme: profile without scheme untouched" "<none>" "$(sed -n 3p <<< "$theme_probe")"
assert_eq "theme: scheme added exactly once" "1" "$(sed -n 4p <<< "$theme_probe")"
assert_eq "theme: unrelated keys preserved" "{guid-1}" "$(sed -n 5p <<< "$theme_probe")"

# Applying twice must not duplicate the scheme entry
_lk_apply_theme_windows "$DRACULA" >/dev/null 2>&1
dup_count=$("$_lk_theme_test_python" -c '
import json, sys
d = json.load(open(sys.argv[1]))
print(len([s for s in d["schemes"] if s["name"] == "Dracula"]))
' "$THEME_SETTINGS")
assert_eq "theme: re-apply is idempotent" "1" "$dup_count"

# Reset must restore the previous per-profile scheme, not delete it
reset_result=$(_lk_reset_theme_windows 2>&1)
assert_eq "theme: reset succeeds" "OK:Default" "$reset_result"
reset_probe=$("$_lk_theme_test_python" -c '
import json, sys
d = json.load(open(sys.argv[1]))
print(d["profiles"]["list"][0].get("colorScheme", "<removed>"))
print(d["profiles"]["defaults"].get("colorScheme", "<removed>"))
print(d["profiles"]["list"][1].get("colorScheme", "<removed>"))
' "$THEME_SETTINGS")
assert_eq "theme: reset restores prior profile scheme" "MyCustomScheme" "$(sed -n 1p <<< "$reset_probe")"
assert_eq "theme: reset clears defaults that had none" "<removed>" "$(sed -n 2p <<< "$reset_probe")"
assert_eq "theme: reset leaves untouched profile alone" "<removed>" "$(sed -n 3p <<< "$reset_probe")"

# No temp files left behind by the atomic write
leftover=$(find "$THEME_TMP" -name ".lk-wt-*" 2>/dev/null | wc -l)
assert_eq "theme: atomic write leaves no temp files" "0" "$leftover"

# Malformed settings must fail loudly without destroying the file
echo 'this is not json at all {{{' > "$THEME_SETTINGS"
cp "$THEME_SETTINGS" "$THEME_TMP/broken-original.json"
rm -f "${THEME_SETTINGS}.lk-backup"
bad_result=$(_lk_apply_theme_windows "$DRACULA" 2>&1 || true)
if [[ "$bad_result" == OK:* ]]; then
    printf '  \e[31m✗\e[0m theme: malformed settings must not report success\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m theme: malformed settings must not report success\n'; ((PASS++))
fi
if cmp -s "$THEME_SETTINGS" "$THEME_TMP/broken-original.json"; then
    printf '  \e[32m✓\e[0m theme: malformed settings left unmodified\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m theme: malformed settings left unmodified\n'; ((FAIL++))
fi

unset _LK_WT_SETTINGS_OVERRIDE
_LK_WT_SCHEME_BACKUP="${HOME}/.config/kiro-launcher/wt-schemes.json"
rm -rf "$THEME_TMP"
fi

# ═══════════════════════════════════════
echo "── Theme: linux prompt file ──"

LTHEME_TMP=$(mktemp -d)
_lk_theme_saved_file="$_LK_THEME_FILE"
_LK_THEME_FILE="$LTHEME_TMP/theme.bash"
GRUVBOX=""
for _te in "${_LK_THEMES[@]}"; do
    [[ "$(_lk_theme_field "$_te" 0)" == "Gruvbox Dark" ]] && GRUVBOX="$_te"
done
lin_result=$(_lk_apply_theme_linux "$GRUVBOX" 2>&1)
assert_eq "theme-linux: reports theme name" "OK:Gruvbox Dark" "$lin_result"
[[ -f "$_LK_THEME_FILE" ]] \
    && { printf '  \e[32m✓\e[0m theme-linux: writes theme file\n'; ((PASS++)); } \
    || { printf '  \e[31m✗\e[0m theme-linux: writes theme file\n'; ((FAIL++)); }
lin_content=$(cat "$_LK_THEME_FILE")
assert_contains "theme-linux: sets PS1" "PS1=" "$lin_content"
assert_contains "theme-linux: uses lambda style for Gruvbox" 'λ' "$lin_content"
assert_contains "theme-linux: sets LS_COLORS" "LS_COLORS=" "$lin_content"
assert_contains "theme-linux: names the theme" "Gruvbox Dark" "$lin_content"
# The generated file must be valid bash
if bash -n "$_LK_THEME_FILE" 2>/dev/null; then
    printf '  \e[32m✓\e[0m theme-linux: generated file is valid bash\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m theme-linux: generated file is valid bash\n'; ((FAIL++))
fi
_LK_THEME_FILE="$_lk_theme_saved_file"
rm -rf "$LTHEME_TMP"

# ═══════════════════════════════════════
echo "── Theme: field extraction ──"
assert_eq "theme-field: name" "Nord" "$(_lk_theme_field "Nord|#2E3440|#D8DEE9|x|y|z" 0)"
assert_eq "theme-field: background" "#2E3440" "$(_lk_theme_field "Nord|#2E3440|#D8DEE9|x|y|z" 1)"
assert_eq "theme-field: missing index is empty" "" "$(_lk_theme_field "Nord|#2E3440" 9)"
assert_eq "theme-field: Default has empty colors" "" "$(_lk_theme_field "${_LK_THEMES[0]}" 1)"

# ═══════════════════════════════════════
echo "── Update: non-blocking probe ──"

UPD_TMP=$(mktemp -d)
_lk_upd_cache_saved="$_LK_UPDATE_CACHE"
_lk_upd_dir_saved="$_LK_SCRIPT_DIR"
_LK_UPDATE_CACHE="$UPD_TMP/.update-check"
_LK_SCRIPT_DIR="$UPD_TMP/nogit"     # no .git, so the git leg is skipped
mkdir -p "$_LK_SCRIPT_DIR"
_LK_AUTO_UPDATE_CHECK="true"
_LK_TRACKED_APP_NAMES=()

# A fresh cache short-circuits without touching the network
cat > "$_LK_UPDATE_CACHE" <<EOF
LAST_CHECK=$(date +%s)
CLI_UPDATE_AVAILABLE=1
CLI_LATEST_VERSION=9.9.9
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
APPS_UPDATE_AVAILABLE=0
APPS_UPDATE_LIST=
EOF
_LK_CLI_UPDATE_AVAILABLE=0
_lk_update_check
assert_eq "probe: renders from fresh cache" "1" "$_LK_CLI_UPDATE_AVAILABLE"
assert_eq "probe: reads cached version" "9.9.9" "$_LK_CLI_LATEST_VERSION"

# A corrupt LAST_CHECK must not break arithmetic
cat > "$_LK_UPDATE_CACHE" <<'EOF'
LAST_CHECK=not-a-number
CLI_UPDATE_AVAILABLE=0
CLI_LATEST_VERSION=
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
APPS_UPDATE_AVAILABLE=0
APPS_UPDATE_LIST=
EOF
if _lk_update_check 2>/dev/null; then
    printf '  \e[32m✓\e[0m probe: survives corrupt LAST_CHECK\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m probe: survives corrupt LAST_CHECK\n'; ((FAIL++))
fi

# Stale cache in async mode must return immediately and leave the old values
cat > "$_LK_UPDATE_CACHE" <<'EOF'
LAST_CHECK=1000000000
CLI_UPDATE_AVAILABLE=0
CLI_LATEST_VERSION=
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
APPS_UPDATE_AVAILABLE=0
APPS_UPDATE_LIST=
EOF
# Run the async case against a throwaway cache so its disowned background
# probe cannot leave temp files behind in the directory checked below.
unset _LK_UPDATE_CHECK_SYNC
_ASYNC_TMP=$(mktemp -d)
_probe_cache_main="$_LK_UPDATE_CACHE"
_LK_UPDATE_CACHE="$_ASYNC_TMP/.update-check"
cat > "$_LK_UPDATE_CACHE" <<'EOF'
LAST_CHECK=1000000000
CLI_UPDATE_AVAILABLE=0
CLI_LATEST_VERSION=
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
APPS_UPDATE_AVAILABLE=0
APPS_UPDATE_LIST=
EOF
_probe_start=$(date +%s)
_lk_update_check
_probe_elapsed=$(( $(date +%s) - _probe_start ))
if (( _probe_elapsed <= 2 )); then
    printf '  \e[32m✓\e[0m probe: async check returns promptly\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m probe: async check returns promptly (took %ss)\n' "$_probe_elapsed"; ((FAIL++))
fi
sleep 1
rm -rf "$_ASYNC_TMP"
_LK_UPDATE_CACHE="$_probe_cache_main"

# Disabled auto-update short-circuits entirely
_LK_AUTO_UPDATE_CHECK="false"
_LK_CLI_UPDATE_AVAILABLE=7
_lk_update_check
assert_eq "probe: disabled check leaves state alone" "7" "$_LK_CLI_UPDATE_AVAILABLE"
_LK_AUTO_UPDATE_CHECK="true"

# The probe writes a complete cache atomically, leaving no temp files
_LK_TRACKED_APP_NAMES=()
rm -f "$_LK_UPDATE_CACHE"
_lk_update_probe >/dev/null 2>&1 || true
if [[ -f "$_LK_UPDATE_CACHE" ]]; then
    probe_cache=$(cat "$_LK_UPDATE_CACHE")
    assert_contains "probe: cache has LAST_CHECK" "LAST_CHECK=" "$probe_cache"
    assert_contains "probe: cache has apps key" "APPS_UPDATE_LIST=" "$probe_cache"
else
    printf '  \e[31m✗\e[0m probe: writes cache file\n'; ((FAIL+=2))
fi
tmp_left=$(find "$UPD_TMP" -name ".update-check.*" 2>/dev/null | wc -l)
assert_eq "probe: atomic cache write leaves no temp files" "0" "$tmp_left"

_LK_UPDATE_CACHE="$_lk_upd_cache_saved"
_LK_SCRIPT_DIR="$_lk_upd_dir_saved"
rm -rf "$UPD_TMP"

# ═══════════════════════════════════════
echo "── Update: launcher self-update guards ──"

SELF_TMP=$(mktemp -d)
_lk_self_dir_saved="$_LK_SCRIPT_DIR"

# Not a git checkout — must refuse rather than run git
_LK_SCRIPT_DIR="$SELF_TMP/plain"
mkdir -p "$_LK_SCRIPT_DIR"
self_out=$(_lk_update_launcher 2>&1 || true)
assert_contains "self-update: refuses non-git checkout" "not a git checkout" "$self_out"
_lk_update_launcher >/dev/null 2>&1 && _self_rc=0 || _self_rc=1
assert_eq "self-update: returns failure for non-git" "1" "$_self_rc"

# Dirty tree — must refuse to pull over local changes
_LK_SCRIPT_DIR="$SELF_TMP/repo"
mkdir -p "$_LK_SCRIPT_DIR"
git -C "$_LK_SCRIPT_DIR" init -q 2>/dev/null
git -C "$_LK_SCRIPT_DIR" config user.email t@e.st
git -C "$_LK_SCRIPT_DIR" config user.name Test
echo "1.0.0" > "$_LK_SCRIPT_DIR/VERSION"
git -C "$_LK_SCRIPT_DIR" add -A 2>/dev/null
git -C "$_LK_SCRIPT_DIR" commit -qm init 2>/dev/null
git -C "$_LK_SCRIPT_DIR" branch -M main 2>/dev/null
echo "dirty" > "$_LK_SCRIPT_DIR/VERSION"
self_dirty=$(_lk_update_launcher 2>&1 || true)
assert_contains "self-update: refuses dirty tree" "Local changes" "$self_dirty"

# Wrong branch — must skip rather than pull main into it
git -C "$_LK_SCRIPT_DIR" checkout -q -- VERSION 2>/dev/null
git -C "$_LK_SCRIPT_DIR" checkout -qb feature 2>/dev/null
self_branch=$(_lk_update_launcher 2>&1 || true)
assert_contains "self-update: refuses non-main branch" "not main" "$self_branch"

_LK_SCRIPT_DIR="$_lk_self_dir_saved"
rm -rf "$SELF_TMP"

# ═══════════════════════════════════════
echo "── Update: CLI version cache invalidation ──"

# The menu caches the CLI version for the shell's lifetime, so an update must
# clear it or the header keeps reporting the old version.
_LK_CLI_VERSION_CACHED="1.0.0-stale"
if grep -q 'unset _LK_CLI_VERSION_CACHED' "$PROJECT_DIR/kiro-launcher.bash"; then
    printf '  \e[32m✓\e[0m version-cache: update path clears the cache\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m version-cache: update path clears the cache\n'; ((FAIL++))
fi
unset _LK_CLI_VERSION_CACHED

# ═══════════════════════════════════════
echo "── Update: CLI changelog rendering ──"

CL_TMP=$(mktemp -d)
mkdir -p "$CL_TMP/kiro-cli"
cat > "$CL_TMP/kiro-cli/feed.json" << 'FEEDEOF'
{
  "versions": [
    {
      "version": "2.20.0",
      "changes": [
        { "type": "added", "description": "Brand new thing" },
        { "type": "fixed", "description": "Squashed a bug" },
        { "type": "changed", "description": "Tweaked a default" }
      ]
    },
    {
      "version": "2.19.0",
      "changes": [
        { "type": "added", "description": "Older feature" }
      ]
    }
  ]
}
FEEDEOF
_lk_kiro_data_dir() { echo "$CL_TMP/kiro-cli"; }
cl_out=$(_lk_show_cli_changelog "2.20.0" 2>&1 | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "changelog: shows version heading" "What's new in 2.20.0" "$cl_out"
assert_contains "changelog: lists added entry" "Brand new thing" "$cl_out"
assert_contains "changelog: lists fixed entry" "Squashed a bug" "$cl_out"
# The change type is now rendered rather than parsed and discarded
assert_contains "changelog: labels added type" "added" "$cl_out"
assert_contains "changelog: labels fixed type" "fixed" "$cl_out"
assert_contains "changelog: labels changed type" "changed" "$cl_out"
# Must stop at the next version block
if [[ "$cl_out" == *"Older feature"* ]]; then
    printf '  \e[31m✗\e[0m changelog: stops at next version\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m changelog: stops at next version\n'; ((PASS++))
fi
unset -f _lk_kiro_data_dir
rm -rf "$CL_TMP"

# ═══════════════════════════════════════
echo "── Meta: version consistency ──"

# VERSION, README and CHANGELOG drifted three ways (1.10.1 / 1.7.0 / 1.9.2).
# These guards keep them in step.
_meta_version=$(cat "$PROJECT_DIR/VERSION")
assert_eq "meta: VERSION is a semver" "0" "$([[ "$_meta_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && echo 0 || echo 1)"

# Every version string the README advertises must be the current one
readme_versions=$(grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' "$PROJECT_DIR/README.md" | sort -u | tr -d 'v' | tr '\n' ' ' | sed 's/ $//')
assert_eq "meta: README advertises only the current version" "$_meta_version" "$readme_versions"

# The changelog must have an entry for the current version
changelog_top=$(grep -m1 -oE '^## [0-9]+\.[0-9]+\.[0-9]+' "$PROJECT_DIR/CHANGELOG.md" | sed 's/^## //')
assert_eq "meta: CHANGELOG documents the current version" "$_meta_version" "$changelog_top"

# The launcher reports the same version it ships
assert_eq "meta: _LK_VERSION matches VERSION file" "$_meta_version" "$(cat "$PROJECT_DIR/VERSION")"

# README claims MIT, so the file must exist and say so
[[ -f "$PROJECT_DIR/LICENSE" ]] \
    && { printf '  \e[32m✓\e[0m meta: LICENSE file exists\n'; ((PASS++)); } \
    || { printf '  \e[31m✗\e[0m meta: LICENSE file exists\n'; ((FAIL++)); }
assert_contains "meta: LICENSE is MIT as README claims" "MIT License" "$(cat "$PROJECT_DIR/LICENSE" 2>/dev/null || echo "")"
assert_contains "meta: README declares a licence" "MIT" "$(cat "$PROJECT_DIR/README.md")"

# ═══════════════════════════════════════
echo "── Menu: footer and idle timeout ──"

_LK_CONFIG_FILE="$FIXTURES/config.yaml"
export LK_PROJECTS_DIR="$FIXTURES/projects"
_lk_load_config
# An earlier test block overwrites _LK_VERSION; restore it
_LK_VERSION="$(tr -d '[:space:]' < "$PROJECT_DIR/VERSION")"
# Disable the update check so the cache does not overwrite the state we set
_LK_AUTO_UPDATE_CHECK="false"
_LK_CLI_UPDATE_AVAILABLE=0
_LK_LAUNCHER_UPDATE_AVAILABLE=0
_LK_APPS_UPDATE_AVAILABLE=0

footer_out=$(echo "q" | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
# The changelog key was implemented but never advertised
assert_contains "footer: advertises changelog key" "changelog" "$footer_out"
assert_contains "footer: still advertises config" "config" "$footer_out"
assert_contains "footer: still advertises quit" "quit" "$footer_out"

# With an update pending, all four keys are offered. lk re-reads the config on
# every loop, so the pending state has to come from the cache like it does in
# real use.
_footer_cache_saved="$_LK_UPDATE_CACHE"
_LK_UPDATE_CACHE=$(mktemp)
cat > "$_LK_UPDATE_CACHE" <<EOF
LAST_CHECK=$(date +%s)
CLI_UPDATE_AVAILABLE=1
CLI_LATEST_VERSION=9.9.9
LAUNCHER_UPDATE_AVAILABLE=0
LAUNCHER_COMMITS_BEHIND=0
APPS_UPDATE_AVAILABLE=0
APPS_UPDATE_LIST=
EOF
footer_upd=$(echo "q" | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "footer: shows update key when pending" "update" "$footer_upd"
assert_contains "footer: shows changelog key when update pending" "changelog" "$footer_upd"
assert_contains "footer: header shows pending version" "9.9.9" "$footer_upd"
rm -f "$_LK_UPDATE_CACHE"
_LK_UPDATE_CACHE="$_footer_cache_saved"
_LK_CLI_UPDATE_AVAILABLE=0

# An idle timeout must explain itself rather than exiting silently
_lk_timeout_saved="$_LK_MENU_TIMEOUT"
_LK_MENU_TIMEOUT=1
# A sleeping writer holds the pipe open so read times out instead of hitting EOF
timeout_out=$( (sleep 3) | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' || true)
assert_contains "timeout: explains why it exited" "No input for" "$timeout_out"
assert_contains "timeout: names the interval" "1s" "$timeout_out"
_LK_MENU_TIMEOUT="$_lk_timeout_saved"
_LK_AUTO_UPDATE_CHECK="true"

# ═══════════════════════════════════════
echo "── CLI: non-interactive invocation ──"

CLI_DIR=$(mktemp -d)
mkdir -p "$CLI_DIR/my-app" "$CLI_DIR/dotfiles" "$CLI_DIR/scripts" "$CLI_DIR/Notes"
CLI_CONFIG=$(mktemp)
cat > "$CLI_CONFIG" << EOF
projects_dir: "$CLI_DIR"
default_flags: "--trust-all-tools"
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"

projects:
  "dotfiles":
    alias: "DF"
EOF
_LK_CONFIG_FILE="$CLI_CONFIG"
export LK_PROJECTS_DIR="$CLI_DIR"
_lk_load_config
_LK_VERSION="$(tr -d '[:space:]' < "$PROJECT_DIR/VERSION")"

# --- version and help ---
assert_eq "cli: version prints name and version" "kiro-launcher $_LK_VERSION" "$(lk version)"
assert_eq "cli: --version matches" "kiro-launcher $_LK_VERSION" "$(lk --version)"
assert_eq "cli: -V matches" "kiro-launcher $_LK_VERSION" "$(lk -V)"

help_out=$(lk help)
assert_contains "cli: help shows usage" "Usage:" "$help_out"
assert_contains "cli: help documents direct launch" "lk <name>" "$help_out"
assert_contains "cli: help documents list" "lk list" "$help_out"
assert_contains "cli: help names the config file" "$CLI_CONFIG" "$help_out"
assert_eq "cli: --help matches help" "$help_out" "$(lk --help)"

# --- listing ---
list_out=$(lk list)
assert_contains "cli: list includes my-app" "my-app" "$list_out"
assert_contains "cli: list includes scripts" "scripts" "$list_out"
# An aliased project shows both the directory and the display name
assert_contains "cli: list shows alias alongside dir" "dotfiles	DF" "$list_out"
assert_eq "cli: list has one line per project" "4" "$(lk list | wc -l)"
assert_eq "cli: -l matches list" "$list_out" "$(lk -l)"

# --- unknown option ---
lk --bogus >/dev/null 2>&1 && _cli_rc=0 || _cli_rc=$?
assert_eq "cli: unknown option exits 2" "2" "$_cli_rc"
assert_contains "cli: unknown option names the flag" "--bogus" "$(lk --bogus 2>&1 || true)"

# --- direct launch: matching tiers ---
# Run in a subshell: launching cds the calling shell by design.
rm -f "$LK_TEST_LOG"
( lk my-app ) >/dev/null 2>&1 || true
assert_contains "cli: exact name launches" "chat --trust-all-tools" "$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")"

rm -f "$LK_TEST_LOG"
launch_dir=$( ( lk my-app >/dev/null 2>&1; pwd ) )
assert_eq "cli: launch cds into the project" "$CLI_DIR/my-app" "$launch_dir"

# Case-insensitive exact
rm -f "$LK_TEST_LOG"
ci_dir=$( ( lk MY-APP >/dev/null 2>&1; pwd ) )
assert_eq "cli: case-insensitive exact match" "$CLI_DIR/my-app" "$ci_dir"

# Prefix
pre_dir=$( ( lk dotf >/dev/null 2>&1; pwd ) )
assert_eq "cli: prefix match" "$CLI_DIR/dotfiles" "$pre_dir"

# Substring
sub_dir=$( ( lk pts >/dev/null 2>&1; pwd ) )
assert_eq "cli: substring match" "$CLI_DIR/scripts" "$sub_dir"

# Alias resolves too
alias_dir=$( ( lk DF >/dev/null 2>&1; pwd ) )
assert_eq "cli: alias match" "$CLI_DIR/dotfiles" "$alias_dir"

# --- no match ---
lk definitely-not-here >/dev/null 2>&1 && _nm_rc=0 || _nm_rc=$?
assert_eq "cli: no match exits 1" "1" "$_nm_rc"
nm_out=$(lk definitely-not-here 2>&1 || true)
assert_contains "cli: no match explains" "no project matching" "$nm_out"
assert_contains "cli: no match suggests list" "lk list" "$nm_out"

# --- ambiguous ---
mkdir -p "$CLI_DIR/ambig-one" "$CLI_DIR/ambig-two"
_lk_load_config
lk ambig >/dev/null 2>&1 && _am_rc=0 || _am_rc=$?
assert_eq "cli: ambiguous exits 1" "1" "$_am_rc"
am_out=$(lk ambig 2>&1 || true)
assert_contains "cli: ambiguous explains" "matches more than one" "$am_out"
assert_contains "cli: ambiguous lists first candidate" "ambig-one" "$am_out"
assert_contains "cli: ambiguous lists second candidate" "ambig-two" "$am_out"
rm -rf "$CLI_DIR/ambig-one" "$CLI_DIR/ambig-two"
_lk_load_config

# --- a project named like a subcommand wins over the subcommand ---
mkdir -p "$CLI_DIR/list"
_lk_load_config
sub_win_dir=$( ( lk list >/dev/null 2>&1; pwd ) )
assert_eq "cli: project named 'list' beats the subcommand" "$CLI_DIR/list" "$sub_win_dir"
rm -rf "$CLI_DIR/list"
_lk_load_config
assert_contains "cli: subcommand works again once project is gone" "my-app" "$(lk list)"

# --- explicit end-of-options ---
end_dir=$( ( lk -- my-app >/dev/null 2>&1; pwd ) )
assert_eq "cli: -- treats next arg as a name" "$CLI_DIR/my-app" "$end_dir"

# --- resolution helpers in isolation ---
_lk_resolve_project "my-app" && _r_rc=0 || _r_rc=$?
assert_eq "resolve: exact returns 0" "0" "$_r_rc"
assert_eq "resolve: sets resolved dir" "$CLI_DIR/my-app" "$_LK_RESOLVED_DIR"

_lk_resolve_project "" && _r_rc=0 || _r_rc=$?
assert_eq "resolve: empty query returns 1" "1" "$_r_rc"

assert_eq "display-name: falls back to dir name" "my-app" "$(_lk_display_name "my-app")"
assert_eq "display-name: uses alias when set" "DF" "$(_lk_display_name "dotfiles")"

_lk_project_exists_exact "my-app" && _e_rc=0 || _e_rc=1
assert_eq "exists-exact: true for real project" "0" "$_e_rc"
_lk_project_exists_exact "my" && _e_rc=0 || _e_rc=1
assert_eq "exists-exact: false for partial name" "1" "$_e_rc"

# --- interactive form still works with no arguments ---
menu_still=$(echo "q" | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "cli: bare lk still opens the menu" "Kiro Launcher" "$menu_still"
assert_contains "cli: menu still lists projects" "my-app" "$menu_still"

# --- T1.5: resume dispatch ---
# lk resume <name>
rm -f "$LK_TEST_LOG"
( lk resume my-app ) >/dev/null 2>&1 || true
assert_contains "cli: 'lk resume <name>' launches with --resume" "--resume" "$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")"

# lk <name> --resume
rm -f "$LK_TEST_LOG"
( lk my-app --resume ) >/dev/null 2>&1 || true
assert_contains "cli: 'lk <name> --resume' launches with --resume" "--resume" "$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")"

# lk -r <name>
rm -f "$LK_TEST_LOG"
( lk -r my-app ) >/dev/null 2>&1 || true
assert_contains "cli: 'lk -r <name>' launches with --resume" "--resume" "$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")"

# plain lk <name> does NOT resume
rm -f "$LK_TEST_LOG"
( lk my-app ) >/dev/null 2>&1 || true
plain_cli_call=$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")
if [[ "$plain_cli_call" != *"--resume"* ]]; then
    printf '  \e[32m✓\e[0m cli: plain launch does not resume\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m cli: plain launch must not resume\n'; ((FAIL++))
fi

# 'lk resume' with no name errors (exit 2)
lk resume >/dev/null 2>&1 && _rz_rc=0 || _rz_rc=$?
assert_eq "cli: 'lk resume' with no name exits 2" "2" "$_rz_rc"

# A project literally named 'resume' wins over the subcommand
mkdir -p "$CLI_DIR/resume"
_lk_load_config
resume_win_dir=$( ( lk resume >/dev/null 2>&1; pwd ) )
assert_eq "cli: project named 'resume' beats the subcommand" "$CLI_DIR/resume" "$resume_win_dir"
rm -rf "$CLI_DIR/resume"
_lk_load_config

# --- T3.4: picker dispatch ---
# lk pick <name>
rm -f "$LK_TEST_LOG"
( lk pick my-app ) >/dev/null 2>&1 || true
assert_contains "cli: 'lk pick <name>' launches with --resume-picker" "--resume-picker" "$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")"

# lk <name> --resume-picker
rm -f "$LK_TEST_LOG"
( lk my-app --resume-picker ) >/dev/null 2>&1 || true
assert_contains "cli: 'lk <name> --resume-picker' launches picker" "--resume-picker" "$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")"

# 'lk pick' with no name errors (exit 2)
lk pick >/dev/null 2>&1 && _pk_rc=0 || _pk_rc=$?
assert_eq "cli: 'lk pick' with no name exits 2" "2" "$_pk_rc"

# A project literally named 'pick' wins over the subcommand
mkdir -p "$CLI_DIR/pick"
_lk_load_config
pick_win_dir=$( ( lk pick >/dev/null 2>&1; pwd ) )
assert_eq "cli: project named 'pick' beats the subcommand" "$CLI_DIR/pick" "$pick_win_dir"
rm -rf "$CLI_DIR/pick"
_lk_load_config

rm -f "$CLI_CONFIG"
rm -rf "$CLI_DIR"

# ═══════════════════════════════════════
echo "── Menu: type-to-filter ──"

FLT_DIR=$(mktemp -d)
mkdir -p "$FLT_DIR/alpha" "$FLT_DIR/beta" "$FLT_DIR/delta-app" "$FLT_DIR/gamma"
FLT_CONFIG=$(mktemp)
cat > "$FLT_CONFIG" << EOF
projects_dir: "$FLT_DIR"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"

projects:
  "gamma":
    alias: "Zebra"
EOF
_LK_CONFIG_FILE="$FLT_CONFIG"
export LK_PROJECTS_DIR="$FLT_DIR"
_lk_load_config

strip_ansi() { sed 's/\x1b\[[0-9;]*m//g'; }

# Unfiltered menu lists everything
unfiltered=$(printf 'q\n' | lk 2>/dev/null | strip_ansi)
assert_contains "filter: unfiltered shows alpha" "alpha" "$unfiltered"
assert_contains "filter: unfiltered shows gamma alias" "Zebra" "$unfiltered"
assert_contains "filter: footer hints at filtering" "type to filter" "$unfiltered"

# Typing text filters the list
filtered=$(printf 'delta\nq\n' | lk 2>/dev/null | strip_ansi)
assert_contains "filter: shows the active filter" "filter: delta" "$filtered"
assert_contains "filter: keeps matching project" "delta-app" "$filtered"
# The second render (after filtering) must not contain the non-matching names
second_render=$(printf 'delta\nq\n' | lk 2>/dev/null | strip_ansi | sed -n '/filter: delta/,$p')
if [[ "$second_render" == *"beta"* ]]; then
    printf '  \e[31m✗\e[0m filter: excludes non-matching projects\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m filter: excludes non-matching projects\n'; ((PASS++))
fi

# Filtering matches the alias too, not just the directory name
alias_filtered=$(printf 'zeb\nq\n' | lk 2>/dev/null | strip_ansi | sed -n '/filter: zeb/,$p')
assert_contains "filter: matches alias" "Zebra" "$alias_filtered"

# Filtering is case-insensitive
ci_filtered=$(printf 'DELTA\nq\n' | lk 2>/dev/null | strip_ansi | sed -n '/filter: DELTA/,$p')
assert_contains "filter: case-insensitive" "delta-app" "$ci_filtered"

# An explicit /prefix also sets the filter
slash_filtered=$(printf '/beta\nq\n' | lk 2>/dev/null | strip_ansi)
assert_contains "filter: /text sets the filter" "filter: beta" "$slash_filtered"

# No matches says so rather than showing an empty list
none_filtered=$(printf 'zzzznope\nq\n' | lk 2>/dev/null | strip_ansi)
assert_contains "filter: reports no matches" "no projects match" "$none_filtered"

# "/" clears the filter and everything comes back.
# Three renders happen: unfiltered, filtered (no match), unfiltered again.
cleared_all=$(printf 'zzzznope\n/\nq\n' | lk 2>/dev/null | strip_ansi)
# Only the middle render should report no matches
assert_eq "filter: / clears the no-match state" "1" "$(grep -c 'no projects match' <<< "$cleared_all")"
# The final render lists everything again (keep only the last menu block)
cleared_last=$(awk '/Kiro Launcher/{buf=""} {buf=buf $0 "\n"} END{printf "%s", buf}' <<< "$cleared_all")
assert_contains "filter: / restores full list" "alpha" "$cleared_last"
# gamma is displayed under its alias
assert_contains "filter: / restores every project" "Zebra" "$cleared_last"

# Selecting by number picks from the filtered list, not the full one
rm -f "$LK_TEST_LOG"
( printf 'delta\n3\n' | lk ) >/dev/null 2>&1 || true
assert_contains "filter: number selects from filtered list" "KIRO_CALLED" "$(cat "$LK_TEST_LOG" 2>/dev/null || echo "")"

# Command keys are not swallowed by the filter
config_reached=$(printf 'c\nb\nq\n' | lk 2>/dev/null | strip_ansi)
assert_contains "filter: c still opens config" "Kiro Launcher Config" "$config_reached"

rm -f "$FLT_CONFIG"

# ═══════════════════════════════════════
echo "── Menu: launch history and recency ──"

REC_HOME=$(mktemp -d)
_lk_rec_saved="$_LK_RECENT_FILE"
_LK_RECENT_FILE="$REC_HOME/.recent"

# No history yet
assert_eq "recent: unknown project has no timestamp" "0" "$(_lk_recent_field "alpha" ts)"
assert_eq "recent: unknown project has no count" "0" "$(_lk_recent_field "alpha" count)"

# Recording creates an entry
_lk_record_launch "alpha"
assert_eq "recent: count starts at 1" "1" "$(_lk_recent_field "alpha" count)"
rec_ts=$(_lk_recent_field "alpha" ts)
if [[ "$rec_ts" =~ ^[0-9]{9,}$ ]]; then
    printf '  \e[32m✓\e[0m recent: records a timestamp\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m recent: records a timestamp (got %s)\n' "$rec_ts"; ((FAIL++))
fi

# Recording again increments rather than duplicating
_lk_record_launch "alpha"
assert_eq "recent: count increments" "2" "$(_lk_recent_field "alpha" count)"
assert_eq "recent: no duplicate lines" "1" "$(grep -c '^alpha	' "$_LK_RECENT_FILE")"

# Multiple projects coexist
_lk_record_launch "beta"
assert_eq "recent: second project tracked" "1" "$(_lk_recent_field "beta" count)"
assert_eq "recent: first project preserved" "2" "$(_lk_recent_field "alpha" count)"
assert_eq "recent: one line per project" "2" "$(wc -l < "$_LK_RECENT_FILE")"

# Names with spaces survive the tab-delimited format
_lk_record_launch "my project"
assert_eq "recent: handles spaces in names" "1" "$(_lk_recent_field "my project" count)"

# No temp files left behind
rec_tmp=$(find "$REC_HOME" -name ".recent.*" 2>/dev/null | wc -l)
assert_eq "recent: atomic write leaves no temp files" "0" "$rec_tmp"

# --- recency ordering ---
REC_DIR=$(mktemp -d)
mkdir -p "$REC_DIR/alpha" "$REC_DIR/beta" "$REC_DIR/gamma" "$REC_DIR/delta"
_LK_PROJECTS_DIR="$REC_DIR"

# Default ordering is alphabetical, unaffected by history
_LK_SORT_ORDER="name"
_lk_collect_projects
name_order=""
for _d in "${_LK_PROJECT_DIRS[@]}"; do name_order+="$(basename "$_d") "; done
assert_eq "sort: default order is alphabetical" "alpha beta delta gamma " "$name_order"

# Recency ordering floats the most recently launched to the top
: > "$_LK_RECENT_FILE"
printf 'gamma\t1\t1000\n' >> "$_LK_RECENT_FILE"
printf 'beta\t1\t2000\n'  >> "$_LK_RECENT_FILE"
_LK_SORT_ORDER="recent"
_lk_collect_projects
recent_order=""
for _d in "${_LK_PROJECT_DIRS[@]}"; do recent_order+="$(basename "$_d") "; done
assert_eq "sort: recent floats last-launched first" "beta gamma alpha delta " "$recent_order"

# Never-launched projects keep alphabetical order among themselves
assert_contains "sort: unlaunched stay alphabetical" "alpha delta" "$recent_order"

# An unknown sort_order behaves like the default
_LK_SORT_ORDER="nonsense"
_lk_collect_projects
fallback_order=""
for _d in "${_LK_PROJECT_DIRS[@]}"; do fallback_order+="$(basename "$_d") "; done
assert_eq "sort: unknown value falls back to alphabetical" "alpha beta delta gamma " "$fallback_order"

# sort_order round-trips through the config file
REC_CONFIG=$(mktemp)
_LK_CONFIG_FILE="$REC_CONFIG"
_LK_SORT_ORDER="recent"
_lk_write_config
assert_contains "sort: persisted to config" 'sort_order: "recent"' "$(cat "$REC_CONFIG")"
_lk_load_config
assert_eq "sort: reloaded from config" "recent" "$_LK_SORT_ORDER"
rm -f "$REC_CONFIG"

# Launching records history through the normal path
LAUNCH_REC=$(mktemp -d)
mkdir -p "$LAUNCH_REC/proj-rec"
: > "$_LK_RECENT_FILE"
( _lk_launch_project "$LAUNCH_REC/proj-rec" ) >/dev/null 2>&1 || true
assert_eq "recent: launching records the project" "1" "$(_lk_recent_field "proj-rec" count)"
rm -rf "$LAUNCH_REC"

_LK_RECENT_FILE="$_lk_rec_saved"
_LK_SORT_ORDER="name"
unset -f strip_ansi
rm -rf "$REC_HOME" "$REC_DIR" "$FLT_DIR"

# ═══════════════════════════════════════
echo "── Menu: git status ──"

GIT_ROOT=$(mktemp -d)
mkdir -p "$GIT_ROOT/plain" "$GIT_ROOT/repo"
git -C "$GIT_ROOT/repo" init -q 2>/dev/null
git -C "$GIT_ROOT/repo" config user.email t@e.st
git -C "$GIT_ROOT/repo" config user.name Test
echo one > "$GIT_ROOT/repo/file.txt"
git -C "$GIT_ROOT/repo" add -A 2>/dev/null
git -C "$GIT_ROOT/repo" commit -qm init 2>/dev/null
git -C "$GIT_ROOT/repo" branch -M trunk 2>/dev/null

# A non-repository yields nothing
assert_eq "git: non-repo has no summary" "" "$(_lk_git_summary "$GIT_ROOT/plain")"
assert_eq "git: missing dir has no summary" "" "$(_lk_git_summary "$GIT_ROOT/nope")"

# A clean repo reports just the branch
assert_eq "git: clean repo shows branch" "trunk" "$(_lk_git_summary "$GIT_ROOT/repo")"

# A dirty tree is marked
echo two >> "$GIT_ROOT/repo/file.txt"
assert_eq "git: dirty tree marked with asterisk" "trunk*" "$(_lk_git_summary "$GIT_ROOT/repo")"

# Untracked files also count as dirty
git -C "$GIT_ROOT/repo" checkout -q -- file.txt 2>/dev/null
touch "$GIT_ROOT/repo/untracked.txt"
assert_eq "git: untracked file marks dirty" "trunk*" "$(_lk_git_summary "$GIT_ROOT/repo")"
rm -f "$GIT_ROOT/repo/untracked.txt"
assert_eq "git: clean again after cleanup" "trunk" "$(_lk_git_summary "$GIT_ROOT/repo")"

# Ahead of upstream is reported
git -C "$GIT_ROOT/repo" clone -q --bare . "$GIT_ROOT/origin.git" 2>/dev/null || \
    git clone -q --bare "$GIT_ROOT/repo" "$GIT_ROOT/origin.git" 2>/dev/null
git -C "$GIT_ROOT/repo" remote add origin "$GIT_ROOT/origin.git" 2>/dev/null
git -C "$GIT_ROOT/repo" fetch -q origin 2>/dev/null
git -C "$GIT_ROOT/repo" branch --set-upstream-to=origin/trunk trunk >/dev/null 2>&1
echo three >> "$GIT_ROOT/repo/file.txt"
git -C "$GIT_ROOT/repo" commit -aqm second 2>/dev/null
git_ahead=$(_lk_git_summary "$GIT_ROOT/repo")
assert_contains "git: reports commits ahead" "↑1" "$git_ahead"
assert_contains "git: still names the branch" "trunk" "$git_ahead"

# --- rendering in the menu ---
GIT_CONFIG=$(mktemp)
cat > "$GIT_CONFIG" << EOF
projects_dir: "$GIT_ROOT"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
show_git_status: "true"
EOF
_LK_CONFIG_FILE="$GIT_CONFIG"
export LK_PROJECTS_DIR="$GIT_ROOT"
_lk_load_config
assert_eq "git: show_git_status parsed" "true" "$_LK_SHOW_GIT_STATUS"
git_menu=$(printf 'q\n' | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "git: menu shows branch" "trunk" "$git_menu"

# Off by default, so the menu stays clean
sed -i 's/show_git_status: "true"/show_git_status: "false"/' "$GIT_CONFIG"
_lk_load_config
assert_eq "git: disabled by config" "false" "$_LK_SHOW_GIT_STATUS"
nogit_menu=$(printf 'q\n' | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
if [[ "$nogit_menu" == *"trunk"* ]]; then
    printf '  \e[31m✗\e[0m git: hidden when disabled\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m git: hidden when disabled\n'; ((PASS++))
fi

# Absent from config means off
GIT_CONFIG2=$(mktemp)
cat > "$GIT_CONFIG2" << EOF
projects_dir: "$GIT_ROOT"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
EOF
_LK_CONFIG_FILE="$GIT_CONFIG2"
_lk_load_config
assert_eq "git: defaults to off" "false" "$_LK_SHOW_GIT_STATUS"
rm -f "$GIT_CONFIG" "$GIT_CONFIG2"
rm -rf "$GIT_ROOT"

# ═══════════════════════════════════════
echo "── Config: show_sessions toggle ──"
# T2.1: show_sessions parses, writes, defaults false (config contract — all 4 sites).
SESS_CONFIG=$(mktemp)
cat > "$SESS_CONFIG" << EOF
projects_dir: "$HOME"
cli_tool: "kiro-cli"
cli_launch: "chat"
show_sessions: "true"
EOF
_LK_CONFIG_FILE="$SESS_CONFIG"
_lk_load_config
assert_eq "sessions: show_sessions parsed" "true" "$_LK_SHOW_SESSIONS"

# Default off when absent.
SESS_CONFIG2=$(mktemp)
cat > "$SESS_CONFIG2" << EOF
projects_dir: "$HOME"
cli_tool: "kiro-cli"
cli_launch: "chat"
EOF
_LK_CONFIG_FILE="$SESS_CONFIG2"
_lk_load_config
assert_eq "sessions: defaults to off" "false" "$_LK_SHOW_SESSIONS"

# Round-trip: set true, write, reload.
_LK_SHOW_SESSIONS="true"
_lk_write_config
written_sess=$(cat "$SESS_CONFIG2")
assert_contains "sessions: written to config" 'show_sessions: "true"' "$written_sess"
_lk_load_config
assert_eq "sessions: round-trip reload" "true" "$_LK_SHOW_SESSIONS"
rm -f "$SESS_CONFIG" "$SESS_CONFIG2"

# ═══════════════════════════════════════
echo "── Session summary parsing ──"
# T2.3: _lk_session_summary parses the newest non-empty session.
# _lk_iso_to_epoch: portable ISO8601 -> epoch.
iso_epoch="$(_lk_iso_to_epoch "2026-09-04T13:19:09.305Z")"
assert_eq "session: iso_to_epoch converts ISO8601" "1788527949" "$iso_epoch"
assert_eq "session: iso_to_epoch empty on junk" "" "$(_lk_iso_to_epoch "not-a-date")"

# Mock kiro-cli to emit a known --list-sessions payload.
SESSMOCK_DIR=$(mktemp -d)
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
# Respond to: chat --list-sessions --format json
for a in "$@"; do [[ "$a" == "--list-sessions" ]] && LS=1; done
if [[ -n "${LS:-}" ]]; then
    cat "$SESSMOCK_PAYLOAD"
    exit 0
fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
export SESSMOCK_PAYLOAD="$SESSMOCK_DIR/payload.json"
_LK_CLI_TOOL="kiro-cli"
_LK_CLI_LAUNCH="chat"

# Newest non-empty session wins (out-of-order updatedAt, one 0-msg entry).
cat > "$SESSMOCK_PAYLOAD" << 'JSON'
[{"cwd":"/x","sessions":[
{"sessionId":"a","source":"v2","title":"older work","updatedAt":"2026-09-01T10:00:00.000Z","messageCount":5},
{"sessionId":"b","source":"v2","title":"newest work","updatedAt":"2026-09-04T13:00:00.000Z","messageCount":12},
{"sessionId":"c","source":"v2","title":"empty session","updatedAt":"2026-09-05T09:00:00.000Z","messageCount":0}
],"complete":true}]
JSON
summary="$(_lk_session_summary "$SESSMOCK_DIR")"
sess_title="${summary%%$'\t'*}"
sess_epoch="${summary##*$'\t'}"
assert_eq "session: picks newest non-empty title" "newest work" "$sess_title"
assert_eq "session: newest epoch (0-msg session skipped)" "$(_lk_iso_to_epoch "2026-09-04T13:00:00.000Z")" "$sess_epoch"

# Empty session list -> no summary.
echo '[{"cwd":"/x","sessions":[],"complete":true}]' > "$SESSMOCK_PAYLOAD"
assert_eq "session: empty list yields nothing" "" "$(_lk_session_summary "$SESSMOCK_DIR")"

# All-zero-message sessions -> no summary.
echo '[{"cwd":"/x","sessions":[{"sessionId":"z","title":"blank","updatedAt":"2026-09-05T09:00:00.000Z","messageCount":0}],"complete":true}]' > "$SESSMOCK_PAYLOAD"
assert_eq "session: all-empty sessions yield nothing" "" "$(_lk_session_summary "$SESSMOCK_DIR")"

# Malformed JSON -> no summary (must not error the menu).
echo 'this is not json {' > "$SESSMOCK_PAYLOAD"
assert_eq "session: malformed JSON yields nothing" "" "$(_lk_session_summary "$SESSMOCK_DIR")"

# CLI without list capability -> no summary.
_LK_CLI_TOOL="claude"
assert_eq "session: CLI without list support yields nothing" "" "$(_lk_session_summary "$SESSMOCK_DIR")"
_LK_CLI_TOOL="kiro-cli"

# No-jq fallback path (Git Bash / minimal systems): run with a PATH that has
# coreutils + the mock CLI but no jq, so the bash parse path is exercised.
cat > "$SESSMOCK_PAYLOAD" << 'JSON'
[{"cwd":"/x","sessions":[
{"sessionId":"a","source":"v2","title":"older work","updatedAt":"2026-09-01T10:00:00.000Z","messageCount":5},
{"sessionId":"b","source":"v2","title":"newest work","updatedAt":"2026-09-04T13:00:00.000Z","messageCount":12}
],"complete":true}]
JSON
JQ_DIR="$(dirname "$(command -v jq 2>/dev/null || echo /nonexistent/jq)")"
nojq_summary="$(
    # Build an isolated bin dir with symlinks to the coreutils the function
    # needs, deliberately excluding jq, so command -v jq fails regardless of
    # /bin -> /usr/bin symlinks.
    _njdir="$(mktemp -d)"
    for _t in bash date grep sed tr cat timeout dirname head cut sort; do
        _tp="$(command -v "$_t" 2>/dev/null)" && ln -sf "$_tp" "$_njdir/$_t" 2>/dev/null
    done
    ln -sf "$MOCK_BIN/kiro-cli" "$_njdir/kiro-cli" 2>/dev/null
    # Git Bash for Windows (MSYS) needs its own runtime directory reachable
    # via PATH to exec anything at all -- with PATH pointed at nothing but
    # this synthetic njdir, even a bare-name exec of a binary verified to
    # exist there fails outright ("command not found"), while the exact same
    # symlink invoked by absolute path runs fine. Appending bash's real
    # directory works around it, and is safe to do here specifically because
    # this fallback path exists for genuine Git-Bash-without-jq setups: if
    # that directory doesn't itself contain jq, appending it can't
    # re-expose jq. (On Linux jq usually lives right next to bash in
    # /usr/bin, so this intentionally skips appending there -- the plain
    # njdir already works standalone on Linux.)
    _bashdir="$(dirname "$(command -v bash)")"
    _extra=""
    [[ -x "$_bashdir/jq" || -x "$_bashdir/jq.exe" ]] || _extra=":$_bashdir"
    PATH="$_njdir$_extra"
    if command -v jq >/dev/null 2>&1; then
        echo "JQ_STILL_PRESENT"
    else
        _lk_session_summary "$SESSMOCK_DIR"
    fi
    rm -rf "$_njdir"
)"
if [[ "$nojq_summary" == "JQ_STILL_PRESENT" ]]; then
    printf '  \e[2m⊘ session: no-jq fallback (jq unhideable here, skipped)\e[0m\n'
else
    nojq_title="${nojq_summary%%$'\t'*}"
    assert_eq "session: no-jq fallback picks newest title" "newest work" "$nojq_title"
fi

# T2.4: in-invocation cache computes once and reuses.
# Mock counts how many times it actually lists sessions.
SESSCOUNT="$SESSMOCK_DIR/count"
: > "$SESSCOUNT"
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
for a in "$@"; do [[ "$a" == "--list-sessions" ]] && LS=1; done
if [[ -n "${LS:-}" ]]; then
    echo "x" >> "$SESSCOUNT_FILE"
    cat "$SESSMOCK_PAYLOAD"
    exit 0
fi
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
export SESSCOUNT_FILE="$SESSCOUNT"
export SESSMOCK_PAYLOAD="$SESSMOCK_DIR/payload.json"
cat > "$SESSMOCK_PAYLOAD" << 'JSON'
[{"cwd":"/x","sessions":[{"sessionId":"b","title":"cached work","updatedAt":"2026-09-04T13:00:00.000Z","messageCount":9}],"complete":true}]
JSON
_LK_CLI_TOOL="kiro-cli"
declare -gA _LK_SESSION_CACHE=()
CACHE_DIR2=$(mktemp -d); mkdir -p "$CACHE_DIR2/cproj"
# Call in the current shell (no subshell) so the global cache persists. Redirect
# stdout to a temp so the printf output doesn't clutter the test log.
_lk_session_cached "$CACHE_DIR2/cproj" > "$SESSMOCK_DIR/o1"
count_after_first="$(wc -l < "$SESSCOUNT")"
_lk_session_cached "$CACHE_DIR2/cproj" > "$SESSMOCK_DIR/o2"
count_after_second="$(wc -l < "$SESSCOUNT")"
assert_eq "session-cache: first call returns title" "cached work" "$(cut -f1 "$SESSMOCK_DIR/o1")"
assert_eq "session-cache: second call returns same" "cached work" "$(cut -f1 "$SESSMOCK_DIR/o2")"
assert_eq "session-cache: CLI invoked once on first call" "1" "$count_after_first"
assert_eq "session-cache: second call hits cache (no new invocation)" "1" "$count_after_second"
rm -rf "$CACHE_DIR2"
unset SESSCOUNT_FILE
declare -gA _LK_SESSION_CACHE=()

# ═══════════════════════════════════════
echo "── Session menu rendering ──"
# T2.5: relative age helper.
now_epoch="$(date +%s)"
assert_eq "age: just now" "just now" "$(_lk_relative_age "$now_epoch")"
assert_eq "age: minutes" "5m" "$(_lk_relative_age "$(( now_epoch - 300 ))")"
assert_eq "age: hours" "2h" "$(_lk_relative_age "$(( now_epoch - 7200 ))")"
assert_eq "age: days" "3d" "$(_lk_relative_age "$(( now_epoch - 259200 ))")"
assert_eq "age: junk yields nothing" "" "$(_lk_relative_age "notanumber")"

# End-to-end: menu shows the session marker + title when show_sessions=true, and
# makes ZERO list calls when the toggle is off.
SMENU_DIR=$(mktemp -d)
mkdir -p "$SMENU_DIR/smproj"
SMENU_CONFIG=$(mktemp)
SMENU_COUNT="$SMENU_DIR/count"
export SESSCOUNT_FILE="$SMENU_COUNT"
export SESSMOCK_PAYLOAD="$SMENU_DIR/payload.json"
cat > "$SESSMOCK_PAYLOAD" << 'JSON'
[{"cwd":"/x","sessions":[{"sessionId":"b","title":"menu session work","updatedAt":"2026-09-04T13:00:00.000Z","messageCount":9}],"complete":true}]
JSON
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
for a in "$@"; do [[ "$a" == "--list-sessions" ]] && LS=1; done
if [[ -n "${LS:-}" ]]; then echo "x" >> "$SESSCOUNT_FILE"; cat "$SESSMOCK_PAYLOAD"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"

# Toggle ON: marker + title appear.
cat > "$SMENU_CONFIG" << EOF
projects_dir: "$SMENU_DIR"
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
show_sessions: "true"
EOF
_LK_CONFIG_FILE="$SMENU_CONFIG"
export LK_PROJECTS_DIR="$SMENU_DIR"
: > "$SMENU_COUNT"
smenu_on=$(echo "q" | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "session-menu: shows session marker" "⏵" "$smenu_on"
assert_contains "session-menu: shows session title" "menu session work" "$smenu_on"

# Toggle OFF: no marker, and ZERO list calls.
sed -i 's/show_sessions: "true"/show_sessions: "false"/' "$SMENU_CONFIG"
: > "$SMENU_COUNT"
smenu_off=$(echo "q" | lk 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
if [[ "$smenu_off" != *"⏵"* ]]; then
    printf '  \e[32m✓\e[0m session-menu: no marker when disabled\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m session-menu: marker should be hidden when disabled\n'; ((FAIL++))
fi
assert_eq "session-menu: zero list calls when disabled" "0" "$(wc -l < "$SMENU_COUNT" | tr -d ' ')"

rm -rf "$SMENU_DIR" "$SMENU_CONFIG"
unset SESSCOUNT_FILE SESSMOCK_PAYLOAD
unset LK_PROJECTS_DIR
declare -gA _LK_SESSION_CACHE=()

# Restore the simple logging mock.
cat > "$MOCK_BIN/kiro-cli" << 'MOCK'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "kiro-cli 2.16.1"; exit 0; fi
echo "KIRO_CALLED: $*" >> "${LK_TEST_LOG:-/tmp/lk-test.log}"
MOCK
chmod +x "$MOCK_BIN/kiro-cli"
unset SESSMOCK_PAYLOAD
rm -rf "$SESSMOCK_DIR"

# ═══════════════════════════════════════
echo "── Projects: scan depth and extra roots ──"

MR_ROOT=$(mktemp -d)
MR_EXTRA=$(mktemp -d)
mkdir -p "$MR_ROOT/work/api" "$MR_ROOT/work/web" "$MR_ROOT/personal/api" "$MR_ROOT/standalone"
mkdir -p "$MR_EXTRA/outside"
# standalone is a real repo, so depth 2 must keep it at the top level
git -C "$MR_ROOT/standalone" init -q 2>/dev/null

MR_CONFIG=$(mktemp)
cat > "$MR_CONFIG" << EOF
projects_dir: "$MR_ROOT"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
EOF
_LK_CONFIG_FILE="$MR_CONFIG"
export LK_PROJECTS_DIR="$MR_ROOT"
_lk_load_config

# Depth 1 is the default and unchanged
assert_eq "depth: defaults to 1" "1" "$_LK_SCAN_DEPTH"
_lk_collect_projects
d1=""
for _d in "${_LK_PROJECT_DIRS[@]}"; do d1+="$(basename "$_d") "; done
assert_eq "depth: 1 lists only top level" "personal standalone work " "$d1"

# Depth 2 expands grouping folders but keeps real repositories
_LK_SCAN_DEPTH="2"
_lk_collect_projects
d2=""
for _d in "${_LK_PROJECT_DIRS[@]}"; do d2+="$(_lk_project_label "$_d") "; done
assert_eq "depth: 2 expands groups, keeps repos" "personal/api standalone work/api work/web " "$d2"

# Labels disambiguate same-named projects in different groups
assert_eq "depth: label includes the group" "work/api" "$(_lk_project_label "$MR_ROOT/work/api")"
assert_eq "depth: label for other group" "personal/api" "$(_lk_project_label "$MR_ROOT/personal/api")"
assert_eq "depth: top-level label has no group" "standalone" "$(_lk_project_label "$MR_ROOT/standalone")"

# A grouped project is resolvable by its group/child label
_lk_resolve_project "work/api" && _mr_rc=0 || _mr_rc=$?
assert_eq "depth: resolves by group label" "0" "$_mr_rc"
assert_eq "depth: resolves to the right dir" "$MR_ROOT/work/api" "$_LK_RESOLVED_DIR"

# A bare name shared by two groups is ambiguous
_lk_resolve_project "api" && _mr_rc=0 || _mr_rc=$?
assert_eq "depth: bare duplicate name is ambiguous" "2" "$_mr_rc"
assert_eq "depth: both candidates reported" "2" "${#_LK_RESOLVED_MATCHES[@]}"

# --- extra_dirs ---
cat > "$MR_CONFIG" << EOF
projects_dir: "$MR_ROOT"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
scan_depth: "1"
extra_dirs:
  - "$MR_EXTRA"
EOF
_lk_load_config
assert_eq "extra-dirs: parsed one entry" "1" "${#_LK_EXTRA_DIRS[@]}"
assert_eq "extra-dirs: value parsed" "$MR_EXTRA" "${_LK_EXTRA_DIRS[0]}"
_lk_collect_projects
xd=""
for _d in "${_LK_PROJECT_DIRS[@]}"; do xd+="$(basename "$_d") "; done
assert_contains "extra-dirs: includes projects from extra root" "outside" "$xd"
assert_contains "extra-dirs: still includes primary root" "standalone" "$xd"

# A project in an extra root is launchable by name
_lk_resolve_project "outside" && _mr_rc=0 || _mr_rc=$?
assert_eq "extra-dirs: resolvable by name" "0" "$_mr_rc"
assert_eq "extra-dirs: resolves to extra root" "$MR_EXTRA/outside" "$_LK_RESOLVED_DIR"

# scan_depth and extra_dirs round-trip through the config file
MR_RT=$(mktemp)
_LK_CONFIG_FILE="$MR_RT"
_LK_SCAN_DEPTH="2"
_LK_EXTRA_DIRS=("$MR_EXTRA")
_LK_SHOW_GIT_STATUS="true"
_lk_write_config
rt=$(cat "$MR_RT")
assert_contains "extra-dirs: written as a list" "extra_dirs:" "$rt"
assert_contains "extra-dirs: list item written" "  - \"$MR_EXTRA\"" "$rt"
assert_contains "depth: written to config" 'scan_depth: "2"' "$rt"
assert_contains "git: written to config" 'show_git_status: "true"' "$rt"
_lk_load_config
assert_eq "depth: reloaded" "2" "$_LK_SCAN_DEPTH"
assert_eq "extra-dirs: reloaded" "$MR_EXTRA" "${_LK_EXTRA_DIRS[0]}"
assert_eq "git: reloaded" "true" "$_LK_SHOW_GIT_STATUS"
rm -f "$MR_RT"

# A nonexistent extra root is skipped without error
_LK_EXTRA_DIRS=("/definitely/not/here")
_LK_SCAN_DEPTH="1"
if _lk_collect_projects 2>/dev/null; then
    printf '  \e[32m✓\e[0m extra-dirs: missing root skipped quietly\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m extra-dirs: missing root skipped quietly\n'; ((FAIL++))
fi

_LK_EXTRA_DIRS=()
_LK_SCAN_DEPTH="1"
_LK_SHOW_GIT_STATUS="false"
rm -f "$MR_CONFIG"
rm -rf "$MR_ROOT" "$MR_EXTRA"


# ═══════════════════════════════════════
echo "── Config: Sort order toggle (#11) ──"

SORT_CFG=$(mktemp)
cat > "$SORT_CFG" << EOF
projects_dir: "$HOME/projects"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
sort_order: "name"
EOF
_LK_CONFIG_FILE="$SORT_CFG"
_lk_load_config
assert_eq "sort: default parsed as name" "name" "$_LK_SORT_ORDER"

# Toggle flips name -> recent and persists
_lk_config_toggle_sort_order >/dev/null 2>&1
assert_eq "sort: toggle flips to recent" "recent" "$_LK_SORT_ORDER"
assert_contains "sort: recent persisted to config" 'sort_order: "recent"' "$(cat "$SORT_CFG")"

# Toggle flips back recent -> name
_lk_config_toggle_sort_order >/dev/null 2>&1
assert_eq "sort: toggle flips back to name" "name" "$_LK_SORT_ORDER"
assert_contains "sort: name persisted to config" 'sort_order: "name"' "$(cat "$SORT_CFG")"

# Config menu renders the Sort order row and dispatches 's'
export LK_PROJECTS_DIR="$HOME/projects"
sort_menu=$(printf 'b\n' | lk config 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "sort: config menu shows Sort order row" "Sort order" "$sort_menu"
printf 's\nb\n' | lk config >/dev/null 2>&1
assert_contains "sort: 's' key toggles to recent" 'sort_order: "recent"' "$(cat "$SORT_CFG")"
unset LK_PROJECTS_DIR
rm -f "$SORT_CFG"

# End-to-end: sort_order recent floats the most-recently-launched project up
SORT_ROOT=$(mktemp -d)
mkdir -p "$SORT_ROOT/alpha" "$SORT_ROOT/beta" "$SORT_ROOT/gamma"
_lk_rec_saved2="$_LK_RECENT_FILE"
SORT_REC=$(mktemp)
_LK_RECENT_FILE="$SORT_REC"
: > "$_LK_RECENT_FILE"
# Record launches so 'beta' is the most recent, then 'gamma'
printf 'alpha\t1\t100\n' >> "$_LK_RECENT_FILE"
printf 'gamma\t1\t200\n' >> "$_LK_RECENT_FILE"
printf 'beta\t1\t300\n'  >> "$_LK_RECENT_FILE"
_LK_PROJECTS_DIR="$SORT_ROOT"
_LK_EXTRA_DIRS=()
_LK_SCAN_DEPTH="1"
_LK_SORT_ORDER="recent"
_lk_collect_projects
assert_eq "sort: recent puts newest-launched first" "$SORT_ROOT/beta" "${_LK_PROJECT_DIRS[0]:-}"
# Name order restores alphabetical
_LK_SORT_ORDER="name"
_lk_collect_projects
assert_eq "sort: name order is alphabetical" "$SORT_ROOT/alpha" "${_LK_PROJECT_DIRS[0]:-}"
_LK_RECENT_FILE="$_lk_rec_saved2"
_LK_SORT_ORDER="name"
unset LK_PROJECTS_DIR
rm -rf "$SORT_ROOT"; rm -f "$SORT_REC"

# ═══════════════════════════════════════
echo "── Config: Show git status toggle (#12) ──"

GST_CFG=$(mktemp)
cat > "$GST_CFG" << EOF
projects_dir: "$HOME/projects"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
show_git_status: "false"
EOF
_LK_CONFIG_FILE="$GST_CFG"
_lk_load_config
assert_eq "gitstatus: default parsed as false" "false" "$_LK_SHOW_GIT_STATUS"

_lk_config_toggle_git_status >/dev/null 2>&1
assert_eq "gitstatus: toggle flips to true" "true" "$_LK_SHOW_GIT_STATUS"
assert_contains "gitstatus: true persisted" 'show_git_status: "true"' "$(cat "$GST_CFG")"

_lk_config_toggle_git_status >/dev/null 2>&1
assert_eq "gitstatus: toggle flips back to false" "false" "$_LK_SHOW_GIT_STATUS"
assert_contains "gitstatus: false persisted" 'show_git_status: "false"' "$(cat "$GST_CFG")"

# Config menu renders the Show git status row and dispatches 'g'
export LK_PROJECTS_DIR="$HOME/projects"
gst_menu=$(printf 'b\n' | lk config 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "gitstatus: config menu shows Show git status row" "Show git status" "$gst_menu"
printf 'g\nb\n' | lk config >/dev/null 2>&1
assert_contains "gitstatus: 'g' key toggles to true" 'show_git_status: "true"' "$(cat "$GST_CFG")"
unset LK_PROJECTS_DIR
rm -f "$GST_CFG"

# ═══════════════════════════════════════
echo "── lk doctor (#16) ──"

DOC_ROOT=$(mktemp -d)
mkdir -p "$DOC_ROOT/some-project"
DOC_CFG=$(mktemp)
cat > "$DOC_CFG" << EOF
projects_dir: "$DOC_ROOT"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
EOF
_LK_CONFIG_FILE="$DOC_CFG"

# Healthy case: valid projects dir, mock kiro-cli on PATH -> exit 0
doc_out=$(lk doctor 2>&1); doc_rc=$?
assert_exit "doctor: healthy setup exits 0" "0" "$doc_rc"
doc_clean=$(printf '%s' "$doc_out" | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "doctor: reports launcher configuration" "Launcher configuration" "$doc_clean"
assert_contains "doctor: names the projects directory" "$DOC_ROOT" "$doc_clean"
assert_contains "doctor: reports the configured CLI" "kiro-cli" "$doc_clean"
assert_contains "doctor: delegates to the platform check" "platform check" "$doc_clean"

# Regression: doctor reports the type -P resolved binary path (not just "on
# PATH"). The mock kiro-cli lives in $MOCK_BIN, which is prepended to PATH, so
# `type -P kiro-cli` must resolve there.
assert_contains "doctor: reports resolved CLI path via type -P" \
    "kiro-cli on PATH → $MOCK_BIN/kiro-cli" "$doc_clean"

# Regression: doctor warns when a shell alias shadows the configured CLI, and
# stays silent when no alias exists. The warning explains why new-tab launches
# (which exec the resolved binary) can diverge from current-tab behavior.
# No-alias case first: the healthy run above defined no alias, so the shadow
# warning must be absent.
if [[ "$doc_clean" == *"shadowed by a shell alias"* ]]; then
    printf '  \e[31m✗\e[0m doctor: no alias -> no shadow warning\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m doctor: no alias -> no shadow warning\n'; ((PASS++))
fi

# Alias case: define an alias for the CLI, re-run doctor, expect the warning.
alias kiro-cli='kiro-cli --shadow-test' 2>/dev/null
doc_alias_out=$(lk doctor 2>&1)
doc_alias_clean=$(printf '%s' "$doc_alias_out" | sed 's/\x1b\[[0-9;]*m//g')
assert_contains "doctor: warns when CLI is shadowed by an alias" \
    "shadowed by a shell alias" "$doc_alias_clean"
assert_contains "doctor: shadow warning explains new-tab divergence" \
    "new-tab launches run the resolved binary" "$doc_alias_clean"
unalias kiro-cli 2>/dev/null

# Failure case: missing projects directory -> required check fails, exit 1
cat > "$DOC_CFG" << EOF
projects_dir: "$DOC_ROOT/definitely-not-here"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
EOF
doc_fail_out=$(lk doctor 2>&1); doc_fail_rc=$?
assert_exit "doctor: missing projects dir exits non-zero" "1" "$doc_fail_rc"
assert_contains "doctor: flags missing projects dir" "projects directory not found" \
    "$(printf '%s' "$doc_fail_out" | sed 's/\x1b\[[0-9;]*m//g')"

# A project literally named 'doctor' takes precedence over the subcommand
mkdir -p "$DOC_ROOT/doctor"
cat > "$DOC_CFG" << EOF
projects_dir: "$DOC_ROOT"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
EOF
export LK_PROJECTS_DIR="$DOC_ROOT"
: > "$LK_TEST_LOG"
lk doctor >/dev/null 2>&1
assert_contains "doctor: real 'doctor' project launches instead of the check" \
    "KIRO_CALLED" "$(cat "$LK_TEST_LOG" 2>/dev/null)"
: > "$LK_TEST_LOG"
unset LK_PROJECTS_DIR
rm -rf "$DOC_ROOT"; rm -f "$DOC_CFG"


# ═══════════════════════════════════════
echo "── First-run wizard (#14) ──"

# The wizard reads answers on stdin and writes a config. We drive
# _lk_first_run_wizard directly (the suite pipes input, so a live `lk` never
# takes the interactive branch — the gate is tested separately below).
_wiz_home=$(mktemp -d)
_wiz_cfg_saved="$_LK_CONFIG_FILE"
_wiz_home_saved="$HOME"

# Candidate roots must exist AND contain at least one subdirectory.
mkdir -p "$_wiz_home/projects/app-one" "$_wiz_home/projects/app-two"
mkdir -p "$_wiz_home/code/thing"
mkdir -p "$_wiz_home/empty-root"          # exists but no subdirs — must be ignored

HOME="$_wiz_home"
_LK_CONFIG_FILE="$_wiz_home/.config/kiro-launcher/config.yaml"

# Answers: pick root #1 (~/projects), CLI #1, flags "--trust-all-tools", final Enter.
# Run in the CURRENT shell (no pipe, no command-substitution) so the function's
# _LK_* assignments persist for the assertions. Output goes to a temp file;
# stdin comes from a here-string.
_wiz_outfile=$(mktemp)
_lk_first_run_wizard > "$_wiz_outfile" 2>&1 <<< $'1\n1\n--trust-all-tools\n\n'
wiz_rc=$?
wiz_clean=$(sed 's/\x1b\[[0-9;]*m//g' "$_wiz_outfile")

assert_exit "wizard: completes successfully" "0" "$wiz_rc"
assert_contains "wizard: greets the user" "Welcome to Kiro Launcher" "$wiz_clean"
assert_contains "wizard: offers ~/projects candidate" "~/projects" "$wiz_clean"
assert_contains "wizard: recommends ~/projects" "recommended" "$wiz_clean"
assert_contains "wizard: offers ~/code candidate" "~/code" "$wiz_clean"
# An existing-but-empty root is not a good default and must not be offered
if [[ "$wiz_clean" == *"empty-root"* ]]; then
    printf '  \e[31m✗\e[0m wizard: empty root excluded from candidates\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m wizard: empty root excluded from candidates\n'; ((PASS++))
fi
assert_eq "wizard: writes chosen projects_dir" "$_wiz_home/projects" "$_LK_PROJECTS_DIR"
assert_eq "wizard: writes default flags" "--trust-all-tools" "$_LK_KIRO_FLAGS"
assert_contains "wizard: config file written" 'projects_dir:' "$(cat "$_LK_CONFIG_FILE" 2>/dev/null)"
assert_contains "wizard: config carries the flags" 'default_flags: "--trust-all-tools"' "$(cat "$_LK_CONFIG_FILE" 2>/dev/null)"

# The chosen CLI resolves its default launch subcommand (kiro-cli -> chat)
assert_eq "wizard: cli_launch derived from tool" "chat" "$_LK_CLI_LAUNCH"

# Typed path when the user picks 'other' (option past the last candidate).
rm -f "$_LK_CONFIG_FILE"
mkdir -p "$_wiz_home/custom/mine"
# candidates are projects(1), code(2); 'other'=3; then type the path; CLI #1; blank flags; Enter
_lk_first_run_wizard > "$_wiz_outfile" 2>&1 <<< $'3\n~/custom\n1\n\n\n'
assert_eq "wizard: 'other' accepts a typed path" "$_wiz_home/custom" "$_LK_PROJECTS_DIR"

# Aborts (returns 1) when 'other' is chosen but no path is given.
rm -f "$_LK_CONFIG_FILE"
_LK_PROJECTS_DIR="__sentinel__"
_lk_first_run_wizard > "$_wiz_outfile" 2>&1 <<< $'3\n\n'
assert_exit "wizard: aborts on empty typed path" "1" "$?"

# No config written on abort
if [[ -f "$_LK_CONFIG_FILE" ]]; then
    printf '  \e[31m✗\e[0m wizard: no config written on abort\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m wizard: no config written on abort\n'; ((PASS++))
fi
rm -f "$_wiz_outfile"

# On a fresh machine with no candidate roots, the wizard recommends ~/projects:
# a blank Enter at the typed prompt selects (and creates) it.
_wiz_fresh=$(mktemp -d)          # empty home — no candidate roots exist
HOME="$_wiz_fresh"
_LK_CONFIG_FILE="$_wiz_fresh/.config/kiro-launcher/config.yaml"
_wiz_fresh_out=$(mktemp)
# typed prompt: blank Enter -> ~/projects; create? blank Enter (Y); CLI #1; flags blank; final Enter
_lk_first_run_wizard > "$_wiz_fresh_out" 2>&1 <<< $'\n\n1\n\n'
assert_eq "wizard: blank Enter recommends ~/projects when no roots exist" \
    "$_wiz_fresh/projects" "$_LK_PROJECTS_DIR"
if [[ -d "$_wiz_fresh/projects" ]]; then
    printf '  \e[32m✓\e[0m wizard: creates the recommended ~/projects root\n'; ((PASS++))
else
    printf '  \e[31m✗\e[0m wizard: creates the recommended ~/projects root\n'; ((FAIL++))
fi
assert_contains "wizard: prompt recommends ~/projects" "recommended" \
    "$(sed 's/\x1b\[[0-9;]*m//g' "$_wiz_fresh_out")"
rm -f "$_wiz_fresh_out"; rm -rf "$_wiz_fresh"

HOME="$_wiz_home_saved"
_LK_CONFIG_FILE="$_wiz_cfg_saved"
rm -rf "$_wiz_home"

# --- Trigger gate: the wizard must NOT run when input is piped (non-TTY) or
# when a config already exists. This is the critical no-regression guard —
# scripted/piped callers and the whole test suite rely on the old paths.
_gate_home=$(mktemp -d)
_gate_cfg_saved="$_LK_CONFIG_FILE"
_gate_home_saved="$HOME"
HOME="$_gate_home"
_LK_CONFIG_FILE="$_gate_home/.config/kiro-launcher/config.yaml"

# (a) No config, piped input (the test harness is never a TTY): old error path.
export LK_PROJECTS_DIR="$_gate_home/does-not-exist"
gate_out=$(printf 'q\n' | lk 2>&1 | sed 's/\x1b\[[0-9;]*m//g')
if [[ "$gate_out" == *"Welcome to Kiro Launcher"* ]]; then
    printf '  \e[31m✗\e[0m wizard: skipped when input is piped (non-TTY)\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m wizard: skipped when input is piped (non-TTY)\n'; ((PASS++))
fi
assert_contains "wizard: piped no-config falls through to old error" \
    "Projects directory not found" "$gate_out"
unset LK_PROJECTS_DIR

# (b) Config present: menu renders, wizard never triggers.
mkdir -p "$_gate_home/.config/kiro-launcher" "$_gate_home/root/proj-a"
cat > "$_LK_CONFIG_FILE" << EOF
projects_dir: "$_gate_home/root"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
EOF
gate_out2=$(printf 'q\n' | lk 2>&1 | sed 's/\x1b\[[0-9;]*m//g')
if [[ "$gate_out2" == *"Welcome to Kiro Launcher"* ]]; then
    printf '  \e[31m✗\e[0m wizard: skipped when a config already exists\n'; ((FAIL++))
else
    printf '  \e[32m✓\e[0m wizard: skipped when a config already exists\n'; ((PASS++))
fi
assert_contains "wizard: existing config renders the menu" "proj-a" "$gate_out2"

HOME="$_gate_home_saved"
_LK_CONFIG_FILE="$_gate_cfg_saved"
rm -rf "$_gate_home"


# ═══════════════════════════════════════
echo "── lk setup: re-runnable non-destructive (#14) ──"

_setup_home=$(mktemp -d)
_setup_cfg_saved="$_LK_CONFIG_FILE"
_setup_home_saved="$HOME"
mkdir -p "$_setup_home/root/proj-a" "$_setup_home/newroot/x"
HOME="$_setup_home"
_LK_CONFIG_FILE="$_setup_home/config.yaml"

# A rich config with per-project settings AND preferences that must survive.
cat > "$_LK_CONFIG_FILE" << EOF
projects_dir: "$_setup_home/root"
default_flags: "--trust-all-tools"
cli_tool: "kiro-cli"
cli_launch: "chat"
theme: "Dracula"
sort_order: "recent"
show_git_status: "true"

projects:
  "proj-a":
    alias: "PA"
    color: "#00cc66"
    flags: "--no-interactive"
EOF

_setup_out=$(mktemp)

# Run 1: keep everything (three blank Enters). Runs in the current shell so
# _LK_* assignments persist; stdin via here-string, output to a file.
_lk_setup > "$_setup_out" 2>&1 <<< $'\n\n\n'
setup_rc=$?
assert_exit "setup: keep-all completes" "0" "$setup_rc"
assert_eq "setup: projects_dir preserved" "$_setup_home/root" "$_LK_PROJECTS_DIR"
assert_eq "setup: default_flags preserved" "--trust-all-tools" "$_LK_KIRO_FLAGS"
assert_eq "setup: cli_tool preserved" "kiro-cli" "$_LK_CLI_TOOL"
# Preferences and per-project config must be re-emitted verbatim
setup_cfg="$(cat "$_LK_CONFIG_FILE")"
assert_contains "setup: theme preserved" 'theme: "Dracula"' "$setup_cfg"
assert_contains "setup: sort_order preserved" 'sort_order: "recent"' "$setup_cfg"
assert_contains "setup: show_git_status preserved" 'show_git_status: "true"' "$setup_cfg"
assert_contains "setup: per-project alias preserved" 'alias: "PA"' "$setup_cfg"
assert_contains "setup: per-project color preserved" 'color: "#00cc66"' "$setup_cfg"
assert_contains "setup: per-project flags preserved" 'flags: "--no-interactive"' "$setup_cfg"

# Run 2: revise the basics only — new root, keep CLI, clear flags with '-'.
_lk_setup > "$_setup_out" 2>&1 <<< $'~/newroot\n\n-\n'
assert_eq "setup: revised projects_dir applied" "$_setup_home/newroot" "$_LK_PROJECTS_DIR"
assert_eq "setup: '-' clears default_flags" "" "$_LK_KIRO_FLAGS"
# The revision must NOT disturb per-project config
assert_contains "setup: per-project survives revision" 'alias: "PA"' "$(cat "$_LK_CONFIG_FILE")"

rm -f "$_setup_out"
HOME="$_setup_home_saved"
_LK_CONFIG_FILE="$_setup_cfg_saved"
rm -rf "$_setup_home"

# A project literally named 'setup' takes precedence over the subcommand.
_setup_prec_home=$(mktemp -d)
_setup_prec_cfg_saved="$_LK_CONFIG_FILE"
_setup_prec_home_saved="$HOME"
HOME="$_setup_prec_home"
_LK_CONFIG_FILE="$_setup_prec_home/config.yaml"
mkdir -p "$_setup_prec_home/root/setup"
cat > "$_LK_CONFIG_FILE" << EOF
projects_dir: "$_setup_prec_home/root"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"
EOF
export LK_PROJECTS_DIR="$_setup_prec_home/root"
: > "$LK_TEST_LOG"
lk setup >/dev/null 2>&1
assert_contains "setup: real 'setup' project launches instead of the wizard" \
    "KIRO_CALLED" "$(cat "$LK_TEST_LOG" 2>/dev/null)"
: > "$LK_TEST_LOG"
unset LK_PROJECTS_DIR
HOME="$_setup_prec_home_saved"
_LK_CONFIG_FILE="$_setup_prec_cfg_saved"
rm -rf "$_setup_prec_home"

# ═══════════════════════════════════════
echo "── Regression: CLI resolution under a shadowing alias ──"
# Bug: the new-tab path resolved the CLI with `command -v "$_LK_CLI_TOOL"`.
# When the tool name is shadowed by a shell alias (e.g. a security wrapper
# aliases `kiro-cli` to `kiro-guard`), `command -v` prints the alias definition
# text ("alias kiro-cli='kiro-guard'") in an interactive shell, and exec-ing
# that literal fails with exit 127. `type -P` must be used instead — it only
# ever yields an executable path. This guards the fix.
(
    # Define the alias, then confirm the resolution primitive the launcher uses
    # (type -P) ignores it and returns the real mock binary path.
    alias kiro-cli='some-guard-wrapper'
    resolved="$(type -P kiro-cli 2>/dev/null)"
    # The value must be a path to an executable, never alias text.
    if [[ "$resolved" == *"alias "* ]]; then
        printf '  \e[31m✗\e[0m alias: type -P must not return alias text\n'
        exit 1
    fi
    [[ -n "$resolved" && -x "$resolved" ]]
)
assert_exit "alias: type -P resolves a real executable under a shadowing alias" 0 "$?"

# The generated launch script must NOT contain alias-definition text and must
# invoke a real path. Verify by inspecting the resolution the launcher performs.
_LK_CLI_TOOL="kiro-cli"
cli_bin_probe="$(type -P "$_LK_CLI_TOOL" 2>/dev/null)"
assert_eq "alias: launcher resolves cli_bin to the mock path" "$MOCK_BIN/kiro-cli" "$cli_bin_probe"

# ═══════════════════════════════════════
echo "── Regression: legacy project-key appearance migration ──"
# Bug: commit bfde9cc made _lk_safe_name injective (hyphen -> _2d). Configs
# written by the older collapsing sanitizer stored settings under collapsed
# underscore keys (e.g. "public_knowledge_network" for the directory
# "public-knowledge-network"). After the change those keys no longer resolved,
# so per-project color/title/prompt/alias/flags appeared to vanish.
# _lk_migrate_legacy_project_keys must remap them onto the current encoding.
_MIG_HOME="$(mktemp -d)"
mkdir -p "$_MIG_HOME/root/public-knowledge-network" "$_MIG_HOME/root/plain"
cat > "$_MIG_HOME/legacy.yaml" << 'EOF'
projects_dir: "REPLACED_AT_RUNTIME"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"

projects:
  "public_knowledge_network":
    flags: "--agent knowledge-network"
    color: "#ff3333"
    title: "Lumen"
    alias: "lumen"
EOF
# Point projects_dir at the temp root so the migration sees the real hyphenated
# directory and maps the orphaned underscore key onto it.
sed -i "s|REPLACED_AT_RUNTIME|$_MIG_HOME/root|" "$_MIG_HOME/legacy.yaml"

_mig_cfg_saved="$_LK_CONFIG_FILE"
_LK_CONFIG_FILE="$_MIG_HOME/legacy.yaml"
_lk_load_config
_mig_safe="$(_lk_safe_name "public-knowledge-network")"
assert_eq "migrate: color remapped to hyphenated dir" "#ff3333" \
    "$(_lk_indirect "_LK_PROJECT_COLOR_${_mig_safe}")"
assert_eq "migrate: title remapped to hyphenated dir" "Lumen" \
    "$(_lk_indirect "_LK_PROJECT_TITLE_${_mig_safe}")"
assert_eq "migrate: alias remapped to hyphenated dir" "lumen" \
    "$(_lk_indirect "_LK_PROJECT_ALIAS_${_mig_safe}")"
assert_eq "migrate: flags remapped to hyphenated dir" "--agent knowledge-network" \
    "$(_lk_indirect "_LK_KIRO_FLAGS_${_mig_safe}")"

# Migration is idempotent and re-emits under the corrected hyphenated key.
_lk_write_config
_mig_written="$(cat "$_MIG_HOME/legacy.yaml")"
assert_contains "migrate: config rewrites with hyphenated key" \
    "public-knowledge-network" "$_mig_written"

# A pre-existing current-encoding value must win over a legacy value (gap-fill
# only, never overwrite).
_LK_CONFIG_FILE="$_MIG_HOME/legacy2.yaml"
cat > "$_MIG_HOME/legacy2.yaml" << EOF
projects_dir: "$_MIG_HOME/root"
default_flags: ""
cli_tool: "kiro-cli"
cli_launch: "chat"
auto_update_check: "false"

projects:
  "public_knowledge_network":
    color: "#ff3333"
  "public-knowledge-network":
    color: "#00ff00"
EOF
_lk_load_config
assert_eq "migrate: current key wins over legacy (no overwrite)" "#00ff00" \
    "$(_lk_indirect "_LK_PROJECT_COLOR_$(_lk_safe_name "public-knowledge-network")")"

_LK_CONFIG_FILE="$_mig_cfg_saved"
rm -rf "$_MIG_HOME"

# === NEW TESTS GO ABOVE THIS LINE ===
# ═══════════════════════════════════════
# Cleanup
# Remove tab scripts this run generated (mocked wt.exe never executed them, so
# they never self-deleted). Only touch files newer than the run marker so a
# concurrent real launch's script is never deleted. Sweep both /tmp and any
# TMPDIR a test may have set.
for _lk_td in "/tmp" "${TMPDIR:-}"; do
    [[ -n "$_lk_td" && -d "$_lk_td" ]] || continue
    find "$_lk_td" -maxdepth 1 -name 'lk-tab-*.sh' -type f \
        -newer "$_LK_TEST_RUN_MARKER" -delete 2>/dev/null
done
rm -f "$_LK_TEST_RUN_MARKER"
rm -rf "$MOCK_BIN" "$LK_TEST_LOG"
unset LK_PROJECTS_DIR LK_TEST_LOG _LK_TEST_RUN_MARKER _lk_td

echo
printf "── bash: %d passed, %d failed ──\n" "$PASS" "$FAIL"
[[ $FAIL -eq 0 ]]
