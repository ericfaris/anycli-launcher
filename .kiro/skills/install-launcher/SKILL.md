# Install Launcher

Interactive installation skill for kiro-launcher. Guides the user through shell selection, installation, and optional RC file configuration.

## Trigger

User says "install", "set up", "configure kiro-launcher", or similar.

## Procedure

### Step 1: Detect Environment

Before asking anything, gather context silently:

1. Check which shells are available: `zsh --version`, `bash --version`, `pwsh --version`
2. Check if already installed by looking for `kiro-launcher` in `~/.zshrc`, `~/.bashrc`, or PowerShell `$PROFILE`
3. Check if `~/.config/kiro-launcher/config` already exists
4. Check if `kiro-cli` is on PATH

### Step 2: Shell Selection

Present available options based on what's installed:

```
Which shell do you use?

  1  zsh   (source from ~/.zshrc)
  2  bash  (source from ~/.bashrc)
  3  PowerShell  (dot-source from $PROFILE)
```

Only show shells that are actually installed on the system. If only one shell is available, confirm it rather than presenting a menu.

If already installed for the selected shell, inform the user and ask if they want to reinstall or skip to configuration.

### Step 3: Install

Based on selection, append the source line to the appropriate RC file:

**zsh** — append to `~/.zshrc`:
```bash
# Kiro Launcher
source ~/projects/kiro-launcher/kiro-launcher.sh
```

**bash** — append to `~/.bashrc`:
```bash
# Kiro Launcher
source ~/projects/kiro-launcher/kiro-launcher.bash
```

**PowerShell** — append to `$PROFILE` (create if it doesn't exist):
```powershell
# Kiro Launcher
. ~/projects/kiro-launcher/kiro-launcher.ps1
```

After appending, confirm success and tell the user to reload their shell (or offer to reload for them).

### Step 4: RC File Configuration (Optional)

Ask:

> "Want to configure a custom projects directory or per-project Kiro flags? (y/n)"

If **no** — done. Inform them defaults will be used (`~/projects`, no flags).

If **yes** — walk through each setting:

**4a: Projects directory**

First, auto-detect likely candidates by checking if these directories exist:
- `~/projects`
- `~/code`
- `~/dev`
- `~/repos`
- `~/src`
- `~/workspace`

If exactly one exists, suggest it:
> "I found `~/projects` — use that as your root? (y/n)"

If multiple exist, present them as options:
> "I found a few candidates:"
> ```
>   1  ~/projects  (4 subdirectories)
>   2  ~/code      (7 subdirectories)
>   3  other (type a path)
> ```

If none exist, ask:
> "Where do your projects live? (e.g., ~/projects, ~/code)"

Validate the chosen directory exists and contains at least one subdirectory. If empty, warn but allow it.

**4b: Default Kiro flags**

Before asking, present the available flags reference table:

```
Available kiro-cli chat flags:

| Flag | Description |
|------|-------------|
| --trust-all-tools (-a) | Allow all tools without confirmation prompts |
| --trust-tools <TOOLS> | Trust only specific tools (comma-separated, e.g., fs_read,fs_write) |
| --no-interactive | Run without expecting user input (autonomous mode) |
| --agent <AGENT> | Load a specific agent profile (e.g., knowledge-network, mechanic) |
| --model <MODEL> | Override the default model |
| --effort <EFFORT> | Set effort level (low, medium, high, xhigh, max) |
| --require-mcp-startup | Exit if any MCP server fails to start |
| --tui | Use the new terminal UI |
| --v3 | Launch the next-generation Kiro agent |
```

Then ask:

> "Default flags to pass to `kiro-cli chat` for all projects? (e.g., --trust-all-tools)"
> "Leave blank for none."

**4c: Per-project flags**

> "Any projects that need different flags from the defaults above?"

If yes, loop:
> "Directory name (e.g., my-app):"
> "Flags for that project:"

Repeat until user says done.

**4d: Write the config file**

Create `~/.config/kiro-launcher/config` with the collected values:

```bash
# Kiro Launcher configuration
PROJECTS_DIR=~/dev

# Default flags
KIRO_FLAGS=--trust-all-tools

# Per-project overrides
KIRO_FLAGS_my-app=--trust-all-tools --no-interactive
```

Only include lines for values the user provided. Omit sections that use defaults.

### Step 5: Verify

After installation:

1. Confirm the source line exists in the RC file
2. Confirm `kiro-cli` is on PATH (warn if not — launcher will fail)
3. If config was created, show a summary of what was written
4. Tell the user to run `lk` to test

### Output Format

Keep responses conversational and brief. One question at a time. Don't dump all steps at once — guide the user through sequentially.

## Error Handling

| Situation | Action |
|-----------|--------|
| RC file doesn't exist | Create it (e.g., `~/.bashrc` may not exist on some systems) |
| Source line already present | Skip, inform user it's already installed |
| Projects directory doesn't exist | Ask if they want to create it or pick a different path |
| `kiro-cli` not on PATH | Warn but continue — they may install it later |
| No write permission to RC file | Error with clear message, suggest manual installation |
