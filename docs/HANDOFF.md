# Handoff — anycli-launcher

**What:** `ac` shell launcher: numbered project menu -> cd -> launch AI CLI.
v3 = 5k-line bash (`anycli-launcher.bash`, branch `v3` + main until v4 lands).
v4 = Go rewrite in progress, see `docs/V4-PLAN.md` (approved).

**Test v3:** `tests/run-tests.sh` (needs bash 4+). CI: `.github/workflows/cross-platform-tests.yml`.
**Go:** not installed locally; use docker (`golang:1`), see plan.

**Open items (priority):**
1. v4 phase 1 core (branch `v4`).
2. Issue #10: macOS bash-suite 39 failures (BSD tools). Low value if v4 lands; fix only on `v3`.
3. Phases 2-5 per plan.

**Decisions that may be revisited:** Go over modular bash; TOML over YAML; dropping
self-update/themes. **Gotchas:** see LESSONS.md (shellcheck pin, macOS bash 3.2, WSL hides non-WSL branches).
