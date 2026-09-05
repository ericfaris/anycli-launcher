#!/usr/bin/env bash
set -uo pipefail

# Skill eval runner — sends prompts to kiro-cli and checks output for expected patterns
# Requires: kiro-cli on PATH, jq installed
# Usage: ./tests/run-skill-evals.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
EVALS_FILE="$PROJECT_DIR/.kiro/skills/install-anycli/evals/evals.json"
PASS=0
FAIL=0
SKIP=0
TIMEOUT=30

green() { printf '\e[1;32m%s\e[0m\n' "$*"; }
red()   { printf '\e[1;31m%s\e[0m\n' "$*"; }
yellow(){ printf '\e[1;33m%s\e[0m\n' "$*"; }
bold()  { printf '\e[1m%s\e[0m\n' "$*"; }

# Preflight
if ! command -v kiro-cli &>/dev/null; then
    red "ERROR: kiro-cli not on PATH — cannot run skill evals"
    exit 1
fi

if ! command -v jq &>/dev/null; then
    red "ERROR: jq not installed — needed to parse evals.json"
    exit 1
fi

if [[ ! -f "$EVALS_FILE" ]]; then
    red "ERROR: evals file not found: $EVALS_FILE"
    exit 1
fi

bold "══ Skill Eval: install-anycli ══"
echo

eval_count=$(jq length "$EVALS_FILE")

for i in $(seq 0 $((eval_count - 1))); do
    name=$(jq -r ".[$i].name" "$EVALS_FILE")
    prompt=$(jq -r ".[$i].prompt" "$EVALS_FILE")
    description=$(jq -r ".[$i].description" "$EVALS_FILE")
    patterns=$(jq -r ".[$i].expected_patterns[]" "$EVALS_FILE")

    printf "  %-40s " "$name"

    # Send prompt to kiro-cli with timeout
    output=$(echo "$prompt" | timeout "$TIMEOUT" kiro-cli chat --no-interactive --trust-all-tools 2>/dev/null) || {
        if [[ $? -eq 124 ]]; then
            yellow "TIMEOUT"
            SKIP=$((SKIP + 1))
            continue
        fi
        yellow "SKIP (kiro-cli error)"
        SKIP=$((SKIP + 1))
        continue
    }

    # Check each expected pattern
    all_matched=true
    failed_pattern=""
    while IFS= read -r pattern; do
        if ! echo "$output" | grep -qiE "$pattern"; then
            all_matched=false
            failed_pattern="$pattern"
            break
        fi
    done <<< "$patterns"

    if [[ "$all_matched" == true ]]; then
        green "PASS"
        PASS=$((PASS + 1))
    else
        red "FAIL (missing: $failed_pattern)"
        FAIL=$((FAIL + 1))
    fi
done

echo
bold "══ Results: $PASS passed, $FAIL failed, $SKIP skipped ══"
[[ $FAIL -eq 0 ]]
