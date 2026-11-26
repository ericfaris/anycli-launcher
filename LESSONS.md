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

## 2026-11-26: v4 phase 3 (backends)

- **tmux expands `-c` and `-n` as formats**, so a project dir or title with
  `#(cmd)` runs cmd, and `#{...}` gets substituted (a bad `-c` silently falls
  back to `$HOME`). Escape `#` as `##`. shell-command argv is *not* expanded,
  but a single-element argv goes through `sh -c`, so shell-quote it. Found by
  probing real tmux 3.4; the integration test locks it in.
- **Don't put user data on a terminal's command line.** wt.exe splits on `;`,
  WSL interop re-quotes args for a Windows command line, AppleScript and
  `write text` need their own quoting. Instead, the tab runs `ac __tab-exec
  <spec>` and the data goes in a JSON file. Only two vetted paths cross those
  layers.
- **A fake terminal makes the tab path e2e-testable**: the test binary
  plays `kitty` and runs the trampoline the way a terminal would. That covers
  the spec file, login re-exec, cd and exec without a GUI.
- `gofmt` turns `''` in a doc comment into `”`.

## 2026-11-26: v4 phase 4 (task worktrees + running view)

- **Match git's paths by file identity.** `git rev-parse --show-toplevel` and
  `worktree list` print paths with symlinks resolved, long Windows names and
  `/`. Comparing strings against `t.TempDir()` breaks on macOS
  (`/private/var`) and Windows (8.3 names). `os.SameFile` (and walking up
  with it to get a subdir) works everywhere; CI was green on all three OSes
  on the first run.
- **Deleting through git, not the filesystem**: finish only calls `git
  worktree remove`, after checking the path is inside worktree_dir (as
  written and resolved, not a symlink) and in `git worktree list`. git
  refuses non-worktrees on its own, which the tests also lock in.
- **Liveness without lying**: the pid file is written by whoever runs the
  CLI (Here after Start, the tab trampoline right before exec, so on Unix it
  is the CLI's own pid). A live pid whose `/proc/<pid>/cwd` is outside the
  worktree is a reused pid, so "finished". No pid file means "unknown".
- **Adding a key over type-to-filter**: binding `w` broke "type `web` to
  filter". Falling back to the filter when the first key after `w` is a
  non-view letter kept it, and the footer hint only shows with tasks, so the
  phase 1-3 goldens stayed byte-identical (only the help goldens changed).
- **Isolate git in tests**: `GIT_CONFIG_GLOBAL` to a temp file plus
  `GIT_CONFIG_NOSYSTEM=1`, or a dev box's `commit.gpgsign` breaks them.

## 2026-11-26: v4 phase 5 (release tooling)

- **goreleaser 2.19+ deprecates `brews` and `hooks.post.install`.** Use
  `homebrew_casks` (it emits on_macos and on_linux blocks) and
  `hooks.post.install_steps` with the declarative `run` DSL (`{{ .StagedPath }}`).
  `goreleaser check` exits non-zero on a deprecated key, so CI catches it.
- **Optional secrets in goreleaser templates:** `{{ .Env.X }}` fails when X is
  unset; `{{ index .Env "X" }}` gives "". Gate `skip_upload` on it so a
  release without the tap token still publishes.
- **Version-less archive names** let the install scripts use
  `releases/latest/download/<asset>` with no GitHub API call or redirect parsing.
- **Test installers without the network:** serve a fake release from
  `httptest` and put a fake `uname` first on PATH, which covers every OS/arch
  branch from one host. A PATH of symlinks without sha256 tools proves the
  fail-closed path.
- **`irm | iex` runs in the caller's session:** `exit` in install.ps1 would
  close the user's PowerShell. Throw instead (`-File` still exits 1).
- A `/vN` module suffix is required for `go install ...@latest` to see vN tags.
