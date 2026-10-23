# Lessons

## 2026-10-22: CI red since Sep 5, unnoticed

- **Pin shellcheck.** Ubuntu's apt/preinstalled shellcheck (0.9.0) flags things
  local 0.11.0 doesn't (SC2218, SC2002, SC2317). "Clean locally" meant nothing
  in CI. CI now installs the release binary at `SHELLCHECK_VERSION`; bump it in
  step with your local copy.
- **macOS `/bin/bash` is 3.2.** It can't source the launcher (`declare -A`), and
  the suite died partway but exited 0, so macOS was a false green for months.
  CI now brew-installs bash; `test-bash.sh` refuses bash < 4 and an EXIT trap
  fails any run that doesn't reach the summary line. That exposed 39 macOS
  failures (#10); the macOS job is `continue-on-error` until they're fixed.
- **Don't time races with fixed sleeps.** The idle-timeout test's `sleep 3`
  writer exited before Windows runners finished rendering the menu (EOF, not
  timeout). Keep the writer alive until the code under test returns.
- **`$(fn)` discards state.** Testing a cache through command substitution can't
  work; call the function in the current shell to populate it.
- **WSL hides branches.** `/proc/version` says microsoft locally (Docker too,
  since it shares the kernel), so non-WSL Linux paths only run in CI.
