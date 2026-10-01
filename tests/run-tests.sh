#!/usr/bin/env bash
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

printf '\e[1m══ anycli-launcher Test Suite ══\e[0m\n\n'

# --- Lint gate: shellcheck (skips gracefully if not installed) ---
# Gates the production script and test scripts. Uses the repo .shellcheckrc, which documents
# every suppressed check. If shellcheck is present and finds an issue, the suite
# fails; if it is absent, we skip so contributors without it can still run tests.
if command -v shellcheck >/dev/null 2>&1; then
    printf '\e[1m── Lint: shellcheck ──\e[0m\n'
    _lint_ok=1
    if shellcheck -s bash "$PROJECT_DIR/anycli-launcher.bash"; then
        printf '  \e[32m✓\e[0m shellcheck: anycli-launcher.bash clean\n'
    else
        printf '  \e[31m✗\e[0m shellcheck: anycli-launcher.bash has findings (see above)\n'
        _lint_ok=0
    fi
    if shellcheck -s bash "$SCRIPT_DIR/check-platform.sh"; then
        printf '  \e[32m✓\e[0m shellcheck: check-platform.sh clean\n\n'
    else
        printf '  \e[31m✗\e[0m shellcheck: check-platform.sh has findings (see above)\n\n'
        _lint_ok=0
    fi
    if shellcheck -s bash -x "$SCRIPT_DIR/test-bash.sh"; then
        printf '  \e[32m✓\e[0m shellcheck: test-bash.sh clean\n\n'
    else
        printf '  \e[31m✗\e[0m shellcheck: test-bash.sh has findings (see above)\n\n'
        _lint_ok=0
    fi
    (( _lint_ok )) || exit 1
else
    printf '\e[1m── Lint: shellcheck ──\e[0m\n'
    printf '  \e[2m⊘ shellcheck not installed — lint skipped\e[0m\n\n'
fi

bash "$SCRIPT_DIR/test-bash.sh"
_suite_rc=$?

printf '\n\e[2mTip: run ./tests/check-platform.sh to verify runtime dependencies on this machine\n'
printf '(useful on Windows Git Bash / WSL2 / macOS before relying on session features).\e[0m\n'

exit "$_suite_rc"
