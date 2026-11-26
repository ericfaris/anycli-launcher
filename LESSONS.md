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

## 2026-11-26: v4 phase 1 (Go rewrite)

- **Bubble Tea v1 queries the terminal in `init()`** (OSC 11 + `ESC[6n`, 5s
  termenv timeout) for every process that imports it. That is harmless in real
  terminals, but a pty harness that doesn't answer looks like a hang. Drive the
  binary with a 6s+ settle, or test the model directly (goldens).
- **Golden TUI tests need a fixed color profile**: build themes on
  `lipgloss.NewRenderer(io.Discard)` + `SetColorProfile(termenv.Ascii|TrueColor)`,
  never the default renderer. `.gitattributes` keeps `*.golden` LF on Windows.
- **One test binary, many roles**: `cmd/ac` TestMain dispatches on
  `os.Args[0]` (copied to `bin/ac` and `bin/fakecli`), so the shim e2e test
  needs no `go build` and records the CLI's real cwd/argv.
- **Remove guards**: Go's `os.RemoveAll` on a symlink unlinks it, but
  `removeDir` checks `Lstat` explicitly anyway. A regression test asserts that
  the symlink target survives.
- **Local `~/.local/go` was broken** by extracting 1.27.4 over an older tree,
  which left stale `src/internal/strconv` files. Always `rm -rf` the old GOROOT
  before extracting.

## 2026-11-26: pwsh install line broke on Windows and macOS

- `ac init pwsh` can't bootstrap via the name `ac`: Windows PowerShell has a built-in `ac` alias (Add-Content), and macOS has `/usr/sbin/ac`. Resolve the binary with `Get-Command ac -CommandType Application | Select-Object -First 1`.
- A "green locally" shim test proved nothing: only the Windows/macOS CI runners exposed it. Keep the OS matrix for the shim smoke test.
- Extracting a Go tarball over an existing `~/.local/go` leaves stale files that break builds; remove the old tree first.

## 2026-11-26: v4 phase 2 (frecency + filter)

- **Type-to-filter vs single-letter commands**: keep the commands (`n c q ? j k`)
  at an empty prompt, add `/` for an explicit filter, and let any other letter
  start it. Turning `r`/`p`/`x` + a letter into filter text keeps `r4` and
  still lets you type `rust`. This kept every phase 1 key test passing as-is.
- **Isolate per-user state in tests on every OS**: Windows' `os.UserHomeDir`
  reads `USERPROFILE`, not `HOME`. A subprocess test that only overrides
  `HOME` would write the runner's real history. Give state files an explicit
  env override (`AC_FRECENCY_FILE`).
- **Reading a path under a regular file** fails with ENOTDIR on Unix
  (not `ErrNotExist`) but path-not-found on Windows. Tests for an unwritable
  location must accept either.
- CI's push trigger listed only `main`/`v4`, so a feature branch got no run
  until `v4-*` was added.
