---
name: install-anycli-launcher
description: Interactive install of anycli-launcher — detects available shells, asks which to install into, and wires up the source line. Use when the user says "install", "set up", or "configure anycli-launcher".
---

# Install anycli-launcher

Interactive installation skill for anycli-launcher. Guides the user through shell selection and installation; the tool's own first-run wizard handles configuration.

## Trigger

User says "install", "set up", "configure anycli-launcher", or similar.

## Procedure

### Step 1: Detect Environment

Before asking anything, gather context silently:

1. Check which shells are available: `zsh --version`, `bash --version`
2. Check if already installed by looking for `anycli-launcher.bash` in `~/.zshrc` or `~/.bashrc`
3. Check if `~/.config/anycli-launcher/config.yaml` already exists
4. Check whether any known AI CLI (`claude`, `kiro-cli`, `aider`, `goose`, `codex`, `amp`, or another the user names) is on PATH — anycli-launcher is CLI-agnostic, so this is informational, not a hard requirement

### Step 2: Shell Selection

Present available options based on what's installed:

```
Which shell do you use?

  1  zsh   (source from ~/.zshrc)
  2  bash  (source from ~/.bashrc)
```

Only show shells that are actually installed on the system. If only one shell is available, confirm it rather than presenting a menu.

If already installed for the selected shell, inform the user and ask if they want to reinstall or skip to just running `ac`.

### Step 3: Install

Based on selection, append the source line to the appropriate RC file:

**zsh** — append to `~/.zshrc`:
```bash
# anycli-launcher
source ~/projects/anycli-launcher/anycli-launcher.bash
```

**bash** — append to `~/.bashrc`:
```bash
# anycli-launcher
source ~/projects/anycli-launcher/anycli-launcher.bash
```

Adjust the path if the user cloned anycli-launcher somewhere other than `~/projects/anycli-launcher`.

After appending, confirm success and tell the user to reload their shell (or offer to reload for them).

### Step 4: Hand off to the built-in wizard

Don't collect configuration in this skill — anycli-launcher's own first-run wizard does that (projects directory, which AI CLI to launch, default flags) the first time `ac` is run with no config file present, and writes `~/.config/anycli-launcher/config.yaml` directly.

Just tell the user to run `ac` to go through it. If they want to change something later, `ac setup` re-runs the same three questions with current values pre-filled, and `ac` → `c` opens the full config menu.

### Step 5: Verify

After installation:

1. Confirm the source line exists in the RC file
2. Tell the user to run `ac` — this both verifies the install and (on first run) starts the setup wizard
3. If they hit a problem, `ac doctor` prints a full health check (config, projects directory, configured CLI on PATH, resume/picker support) they can paste into a bug report

### Output Format

Keep responses conversational and brief. One question at a time. Don't dump all steps at once — guide the user through sequentially.

## Error Handling

| Situation | Action |
|-----------|--------|
| RC file doesn't exist | Create it (e.g., `~/.bashrc` may not exist on some systems) |
| Source line already present | Skip, inform user it's already installed |
| No AI CLI found on PATH | Not fatal — the first-run wizard will ask the user to type the command name manually |
| No write permission to RC file | Error with clear message, suggest manual installation |
