#!/usr/bin/env bash
#
# check-platform.sh — verify kiro-launcher's runtime dependencies and platform
# capabilities on the current machine. Run this on any OS (Linux, macOS, WSL2,
# Git Bash) to confirm the launcher — and especially the session-resume feature
# — will work before relying on it.
#
# It checks the REAL environment (not mocks): required commands, optional
# commands with graceful fallbacks, and the specific capabilities the code
# depends on (ISO->epoch date conversion, bash version, the configured AI CLI's
# session support). Nothing here modifies your system or config.
#
# Exit status: 0 if all REQUIRED checks pass (warnings allowed), 1 otherwise.

set -uo pipefail

# --- Output helpers ---------------------------------------------------------
if [[ -t 1 ]]; then
    C_OK=$'\e[32m'; C_WARN=$'\e[33m'; C_ERR=$'\e[31m'; C_DIM=$'\e[2m'; C_RST=$'\e[0m'
else
    C_OK=""; C_WARN=""; C_ERR=""; C_DIM=""; C_RST=""
fi

FAILURES=0
WARNINGS=0

pass() { printf '  %s✓%s %s\n' "$C_OK" "$C_RST" "$1"; }
warn() { printf '  %s!%s %s%s%s\n' "$C_WARN" "$C_RST" "$1" "${2:+ — }" "${2:-}"; ((WARNINGS++)); }
fail() { printf '  %s✗%s %s%s%s\n' "$C_ERR" "$C_RST" "$1" "${2:+ — }" "${2:-}"; ((FAILURES++)); }
head_line() { printf '\n%s── %s ──%s\n' "$C_DIM" "$1" "$C_RST"; }

have() { command -v "$1" >/dev/null 2>&1; }

# --- Platform detection -----------------------------------------------------
detect_platform() {
    if [[ -f /proc/version ]] && grep -qi microsoft /proc/version 2>/dev/null; then
        echo "WSL2"
    elif [[ "${MSYSTEM:-}" =~ ^(MINGW|MSYS|UCRT) || "$(uname -s 2>/dev/null)" == MINGW* ]]; then
        echo "Git Bash (Windows)"
    elif [[ "$(uname -s 2>/dev/null)" == "Darwin" ]]; then
        echo "macOS"
    elif [[ "$(uname -s 2>/dev/null)" == "Linux" ]]; then
        echo "Linux"
    else
        echo "unknown ($(uname -s 2>/dev/null || echo '?'))"
    fi
}

PLATFORM="$(detect_platform)"

printf '%skiro-launcher platform check%s\n' "$C_DIM" "$C_RST"
printf 'Platform: %s\n' "$PLATFORM"
printf 'Shell: bash %s\n' "${BASH_VERSION:-unknown}"

# --- Bash version -----------------------------------------------------------
head_line "Shell"
if [[ -n "${BASH_VERSINFO:-}" ]] && (( BASH_VERSINFO[0] >= 4 )); then
    pass "bash ${BASH_VERSINFO[0]}.${BASH_VERSINFO[1]} (4+ required)"
else
    fail "bash 4+ required" "found ${BASH_VERSION:-none} — associative arrays and \${var,,} will not work"
fi

# --- Required commands ------------------------------------------------------
# These are used on the core launcher path; the launcher will not work without
# them.
head_line "Required commands"
for cmd in grep sed mktemp find sort date head wc; do
    if have "$cmd"; then
        pass "$cmd"
    else
        fail "$cmd" "required by the launcher"
    fi
done

# --- Optional commands (graceful fallbacks) ---------------------------------
# The launcher degrades safely without these.
head_line "Optional commands (graceful fallback)"
if have jq; then
    pass "jq $(jq --version 2>/dev/null || true) — robust session-JSON parsing"
else
    warn "jq not found" "session titles use a best-effort text parser instead (still works)"
fi
if have timeout; then
    pass "timeout — session lookups are time-bounded"
else
    warn "timeout not found" "session lookups run without a hard time limit"
fi
if have tr; then
    pass "tr"
else
    warn "tr not found" "the no-jq session fallback needs tr; sessions may not display without jq"
fi
if have stty; then
    pass "stty — clean prompt after new-tab spawn"
else
    warn "stty not found" "the tab-spawn input drain is skipped (may see stray input like 2c/3434)"
fi

# --- Windows / new-tab helpers ----------------------------------------------
head_line "New-tab launch (Windows Terminal)"
case "$PLATFORM" in
    "Git Bash (Windows)")
        if have cygpath; then pass "cygpath — path translation for wt.exe"
        else warn "cygpath not found" "new-tab launch may fail; current-tab still works"; fi
        if have wt.exe; then pass "wt.exe — Windows Terminal available"
        else warn "wt.exe not on PATH" "projects with color/title fall back to current-tab launch"; fi
        ;;
    "WSL2")
        if have wslpath; then pass "wslpath — path translation for wt.exe"
        else warn "wslpath not found" "new-tab launch may fall back to current-tab"; fi
        if have wt.exe; then pass "wt.exe reachable from WSL"
        else warn "wt.exe not on PATH" "projects with color/title fall back to current-tab launch"; fi
        ;;
    *)
        printf '  %s(new-tab via wt.exe is Windows-only; not applicable on %s)%s\n' "$C_DIM" "$PLATFORM" "$C_RST"
        ;;
esac

# --- Capability: ISO8601 -> epoch -------------------------------------------
# The session-aware menu converts session timestamps to a relative age. This
# must work with whatever `date` this platform ships (GNU or BSD/macOS).
head_line "Capabilities"
_iso="2026-09-04T13:19:09.305Z"
_epoch=""
if _epoch="$(date -d "$_iso" +%s 2>/dev/null)" && [[ "$_epoch" =~ ^[0-9]+$ ]]; then
    pass "ISO8601 -> epoch via GNU 'date -d' ($_epoch)"
elif _epoch="$(date -j -u -f "%Y-%m-%dT%H:%M:%S" "${_iso%%.*}" +%s 2>/dev/null)" && [[ "$_epoch" =~ ^[0-9]+$ ]]; then
    pass "ISO8601 -> epoch via BSD 'date -j' ($_epoch)"
else
    fail "ISO8601 -> epoch" "neither GNU nor BSD date parsed the timestamp; session ages will not display"
fi

# --- Configured AI CLI ------------------------------------------------------
head_line "AI CLI"
# Try to read the configured tool from the user's config; default to kiro-cli.
CLI_TOOL="kiro-cli"
CFG="${HOME}/.config/kiro-launcher/config.yaml"
if [[ -f "$CFG" ]]; then
    _t="$(grep -E '^cli_tool:' "$CFG" 2>/dev/null | head -1 | sed -E 's/^cli_tool:[[:space:]]*"?([^"]*)"?.*/\1/')"
    [[ -n "$_t" ]] && CLI_TOOL="$_t"
fi
printf '  %sconfigured CLI: %s%s\n' "$C_DIM" "$CLI_TOOL" "$C_RST"
if have "$CLI_TOOL"; then
    pass "$CLI_TOOL is on PATH"
    if [[ "$CLI_TOOL" == "kiro-cli" ]]; then
        # Confirm the session-listing capability the menu relies on.
        if kiro-cli chat --help 2>/dev/null | grep -q -- '--list-sessions'; then
            pass "kiro-cli supports --list-sessions (session-aware menu works)"
        else
            warn "kiro-cli has no --list-sessions" "session-aware menu will show nothing; resume/picker may still work"
        fi
        if kiro-cli chat --help 2>/dev/null | grep -q -- '--resume'; then
            pass "kiro-cli supports --resume (r<N>, lk resume)"
        else
            warn "kiro-cli has no --resume" "resume launch will start fresh"
        fi
        if kiro-cli chat --help 2>/dev/null | grep -q -- '--resume-picker'; then
            pass "kiro-cli supports --resume-picker (p<N>, lk pick)"
        else
            warn "kiro-cli has no --resume-picker" "the session picker will start fresh"
        fi
    fi
else
    warn "$CLI_TOOL not found on PATH" "the launcher will cd but cannot launch the CLI"
fi

# --- Summary ----------------------------------------------------------------
printf '\n'
if (( FAILURES == 0 )); then
    printf '%s✓ All required checks passed%s' "$C_OK" "$C_RST"
    (( WARNINGS > 0 )) && printf ' %s(%d warning%s — optional features degraded)%s' \
        "$C_WARN" "$WARNINGS" "$([[ $WARNINGS -eq 1 ]] && echo '' || echo 's')" "$C_RST"
    printf '\n'
    exit 0
else
    printf '%s✗ %d required check%s failed%s' "$C_ERR" "$FAILURES" \
        "$([[ $FAILURES -eq 1 ]] && echo '' || echo 's')" "$C_RST"
    (( WARNINGS > 0 )) && printf ' %s(+%d warning%s)%s' "$C_WARN" "$WARNINGS" \
        "$([[ $WARNINGS -eq 1 ]] && echo '' || echo 's')" "$C_RST"
    printf '\n'
    exit 1
fi
