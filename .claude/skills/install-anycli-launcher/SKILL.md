---
name: install-anycli-launcher
description: Interactive install of anycli-launcher v4 (the Go `ac` binary) — picks an install method (Homebrew, scoop, install.sh/install.ps1, go install), detects available shells, asks which to wire up, adds the `ac init <shell>` line, and moves a v3 setup over. Use when the user says "install", "set up", or "configure anycli-launcher".
---

# Install anycli-launcher (v4)

v4 is a single `ac` binary plus one shell-integration line per shell. The
tool's own first-run wizard handles configuration; this skill only gets the
binary on PATH and the integration line in place.

## Step 1: Detect (silently, before asking)

1. OS / arch (`uname -sm`; Windows: PowerShell `$env:PROCESSOR_ARCHITECTURE`).
2. Existing binary: `command -v ac` alone is misleading — macOS ships
   `/usr/sbin/ac` and PowerShell has an `ac` alias. Check
   `ac --version 2>/dev/null | grep -q anycli-launcher` (pwsh:
   `(Get-Command ac -CommandType Application | Select-Object -First 1).Source`).
   If installed, `ac --version` also prints how it was installed and the
   update command.
3. Package managers present: `brew`, `scoop`, `go` (needs Go 1.26+).
4. Shells installed: `bash`, `zsh`, `fish`, `pwsh`.
5. v3 leftovers: a `source .../anycli-launcher.bash` line in `~/.bashrc` /
   `~/.zshrc`, and `~/.config/anycli-launcher/config.yaml`.
6. Known AI CLIs on PATH (`claude`, `codex`, `aider`, `goose`, `kiro-cli`,
   `amp`) — informational only.

## Step 2: Install the binary (ask which method; recommend the first that fits)

| Method | Command |
|---|---|
| Homebrew (macOS/Linux) | `brew install --cask ericfaris/tap/anycli-launcher` |
| scoop (Windows) | `scoop bucket add ericfaris https://github.com/ericfaris/scoop-bucket` then `scoop install anycli-launcher` |
| Script (Linux/macOS) | `curl -fsSL https://github.com/ericfaris/anycli-launcher/releases/latest/download/install.sh \| sh` |
| Script (Windows) | `irm https://github.com/ericfaris/anycli-launcher/releases/latest/download/install.ps1 \| iex` |
| Go | `go install github.com/ericfaris/anycli-launcher/v4/cmd/ac@latest` |

The scripts verify SHA-256 against the release's `checksums.txt` and fail
closed. `install.sh` installs to `~/.local/bin` (`--dir` / `$AC_INSTALL_DIR`),
`install.ps1` to `%LOCALAPPDATA%\Programs\anycli-launcher`. Neither touches rc
files unless passed `--setup` / `-Setup`; prefer doing Step 3 here so the user
sees exactly what is added. If the install dir is not on PATH, add it in the
same rc file, before the init line.

## Step 3: Shell integration (ask which shells; only offer installed ones)

Append once (skip if already present) to each chosen shell's rc file:

| Shell | File | Line |
|---|---|---|
| bash | `~/.bashrc` | `eval "$(ac init bash)"` |
| zsh | `${ZDOTDIR:-~}/.zshrc` | `eval "$(ac init zsh)"` |
| fish | `~/.config/fish/config.fish` | `ac init fish \| source` |
| pwsh | `$PROFILE` | `Invoke-Expression (& (Get-Command ac -CommandType Application \| Select-Object -First 1).Source init pwsh \| Out-String)` |

Never use a bare `ac init pwsh` for PowerShell: the built-in `ac` alias
(Add-Content) or macOS `/usr/sbin/ac` would run instead.

Coming from v3: remove (or comment out) the old
`source .../anycli-launcher.bash` line in the same rc file, or the v3
function shadows the v4 one. Ask before editing it.

## Step 4: Hand off to the wizard

Tell the user to open a new shell and run `ac`. With no `config.toml`, the
first run imports a v3 `config.yaml` automatically (or `ac migrate` does it
explicitly and lists what was dropped), otherwise asks for the projects
directory, the AI CLI and default flags. `ac setup` re-runs those questions.

## Step 5: Verify

- `ac --version` prints `anycli-launcher 4.x.y` plus the update command.
- `ac doctor` should say "shell integration active" when run from the new
  shell; paste its output for any bug report.

Keep it conversational: one question at a time.

## Error handling

| Situation | Action |
|---|---|
| Checksum mismatch / download failed in the script | Stop; don't retry with checks disabled. Suggest another method or a release archive |
| Install dir not on PATH | Add it to the rc file before the init line |
| RC file missing | Create it |
| Integration line already present | Skip and say so |
| No AI CLI found | Not fatal; the wizard lets the user type a command |
