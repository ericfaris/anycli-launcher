#!/usr/bin/env bash

# anycli-launcher: interactive project launcher for any AI CLI
# Requires: bash 4+ (source from ~/.bashrc)
#
# Source: source /path/to/anycli-launcher.bash

# Version and paths
_AC_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
_AC_VERSION="$(cat "$_AC_SCRIPT_DIR/VERSION" 2>/dev/null || echo "dev")"

# Portable helpers
#
# zsh compatibility note: this file is bash-authored (BASH_REMATCH, 0-based
# arrays, indirect expansion via ${!name}) but is also sourced from ~/.zshrc.
# `ac()` below scopes `emulate -L bash` for its whole call tree so the bulk of
# the file behaves like bash there, but that only changes *options* — it can't
# add syntax zsh's parser never learned (${!name}, ${x,,}, compgen, mapfile,
# read -a). Those specific spots are hand-shimmed with $ZSH_VERSION checks;
# everything else relies on the emulate scoping.
_ac_vars_with_prefix() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        # shellcheck disable=SC2296,SC2086  # zsh-only syntax; this branch never runs under bash, which shellcheck lints as
        print -l -- ${(k)parameters[(I)$1*]} 2>/dev/null
    else
        compgen -v "$1" 2>/dev/null
    fi
}

# Read the value of the variable named "$1" — a portable stand-in for bash's
# ${!1} indirect expansion, which zsh's parser rejects outright (no amount of
# `emulate -L bash` teaches it that syntax; eval is the only common ground).
#
# This is the hottest primitive in the file (every flag/color/title/prompt/
# alias/app-field read goes through it, several times per project per menu
# render), so the bash branch uses bash's own ${!1} indirect expansion
# directly rather than eval — no subshell, no string-parsing a second script.
# zsh still needs eval; it has no equivalent syntax at all.
_ac_indirect() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        local _ac_ind_val
        eval "_ac_ind_val=\"\${$1:-}\""
        printf '%s' "$_ac_ind_val"
    else
        printf '%s' "${!1:-}"
    fi
}

_ac_indirect_set() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        eval "[[ -n \"\${$1+set}\" ]]"
    else
        [[ -n "${!1+set}" ]]
    fi
}

# Print the keys of the associative array named "$1", one per line — a
# portable stand-in for bash's "${!arrname[@]}", which zsh's parser rejects.
# Every caller's keys pass through _ac_safe_name first (alphanumeric only),
# so unquoted word-splitting on the result is safe.
_ac_assoc_keys() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        eval "print -l -- \"\${(k)$1}\"" 2>/dev/null
    else
        eval "printf '%s\n' \"\${!$1[@]}\"" 2>/dev/null
    fi
}

# Lowercase "$1" — a portable stand-in for bash's ${var,,}, which zsh has no
# equivalent syntax for (it uses ${(L)var} instead).
_ac_lower() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        # shellcheck disable=SC2296  # zsh-only syntax; this branch never runs under bash, which shellcheck lints as
        printf '%s' "${(L)1}"
    else
        printf '%s' "${1,,}"
    fi
}

# "Press any key to continue" — read exactly one raw keypress with no
# newline required. Under bash, `read -n 1` does this directly. Under zsh,
# `read -n 1` is NOT the raw-keypress equivalent -- it silently hangs waiting
# for a newline that never comes (confirmed interactively: the keystroke
# echoes but read never returns). zsh's actual single-raw-character read is
# `read -k 1`, but `-k` needs a real terminal device: over a plain pipe (no
# tty at all -- piped/scripted invocations, tests, CI) it fails outright
# with "not interactive and can't open terminal" instead of reading
# anything, confirmed both interactively via a pty and over a plain pipe.
# So `-k` is used only when stdin is actually a terminal; otherwise this
# falls back to a plain line read, which works the same as bash's `-n 1`
# does over a non-tty pipe (reads to the next newline or EOF, whichever
# comes first -- there is no raw single-keypress option for non-tty input in
# either shell).
_ac_pause() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        if [[ -t 0 ]]; then
            read -k 1 -r _ac_pause_key 2>/dev/null
        else
            read -r _ac_pause_key 2>/dev/null
        fi
    else
        read -r -n 1 _ac_pause_key 2>/dev/null
    fi
}

_ac_assign() {
    # Safely assign a value to a dynamically-named variable. Under bash,
    # printf -v does this without eval (no subshell, no re-parsing a second
    # script); zsh's printf lacks -v, so it keeps the eval form.
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        eval "$1=\"\$2\""
    else
        printf -v "$1" '%s' "$2"
    fi
}

# Resolve "$1" to a real executable path, ignoring shell aliases and
# functions — a portable stand-in for bash's `type -P`. When a tool name is
# shadowed by a shell alias or function (e.g. a security wrapper aliasing
# `kiro-cli` to `kiro-guard`), `command -v` prints the alias/function
# definition text instead of a path, and exec-ing that literal fails with
# "exec: alias ...: not found". `type -P` (bash) / `whence -p` (zsh) only
# ever yield an executable path or nothing.
#
# zsh's `type` builtin has no -P flag at all ("bad option: -P" — it isn't a
# semantics difference, the flag flat out doesn't exist there), so this
# can't be a `type -P` call guarded by `emulate -L bash` the way other
# bash/zsh differences in this file are; `whence -p` is zsh's equivalent.
_ac_resolve_bin() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        whence -p "$1" 2>/dev/null
    else
        type -P "$1" 2>/dev/null
    fi
}

# Map a project name to a variable-name suffix.
#
# Non-alphanumeric bytes are hex-escaped as _<hh> rather than collapsed to a
# single underscore. Collapsing made distinct projects collide: "my-app",
# "my_app" and "my.app" all produced "my_app", so they silently shared flags
# and a config rewrite dropped all but one of them. Escaping the underscore
# too keeps the mapping injective, so no two names can ever collide.
#
# Pure-alphanumeric names are returned unchanged, which keeps the common case
# readable and preserves existing variable names.
_ac_safe_name() {
    local s="${1:-}"
    # Fast path: an all-alphanumeric name (the common case for project
    # directory names) needs no escaping at all -- skip the char-by-char
    # loop, and with it the per-non-alnum-byte `printf` subshell below,
    # entirely. (A memoizing cache was considered too -- every call site
    # invokes this via command substitution, `safe="$(_ac_safe_name ...)"`,
    # so any cache this function itself populated would be written inside
    # that subshell and discarded the instant the substitution completes,
    # same trap _ac_find_python hit; making memoization work for real would
    # mean changing the calling convention at every one of this function's
    # ~15 call sites, not just here.)
    [[ "$s" =~ ^[a-zA-Z0-9]*$ ]] && { printf '%s' "$s"; return; }
    local out="" i c
    for (( i = 0; i < ${#s}; i++ )); do
        c="${s:$i:1}"
        case "$c" in
            [a-zA-Z0-9]) out+="$c" ;;
            *) out+="$(printf '_%02x' "'$c")" ;;
        esac
    done
    printf '%s' "$out"
}

# Variable-name prefixes for the five per-project properties. Shared by
# _ac_load_config (clearing them before a reparse), _ac_write_config
# (collecting which projects have any config to emit), and _ac_config_menu
# (counting configured entries for the summary lines) -- previously each of
# those repeated the same five-prefix list as five near-identical blocks.
_AC_PROJ_PROP_PREFIXES=(_AC_CLI_FLAGS_ _AC_PROJECT_COLOR_ _AC_PROJECT_TITLE_ _AC_PROJECT_PROMPT_ _AC_PROJECT_ALIAS_)

# Registry mapping a safe suffix back to the project name as the user wrote it.
# Without this, _ac_write_config rewrote every project key in its sanitized
# form, corrupting names that contained hyphens or dots.
declare -gA _AC_PROJ_NAME 2>/dev/null || declare -A _AC_PROJ_NAME

# Record the original spelling for a project name.
_ac_register_project() {
    local name="${1:-}" safe
    [[ -z "$name" ]] && return 0
    safe="$(_ac_safe_name "$name")"
    _AC_PROJ_NAME["$safe"]="$name"
    return 0
}

# Record a tracked app name in configuration order.
_ac_register_app() {
    local name="${1:-}"
    [[ -z "$name" ]] && return 0
    local existing
    for existing in "${_AC_TRACKED_APP_NAMES[@]+"${_AC_TRACKED_APP_NAMES[@]}"}"; do
        [[ "$existing" == "$name" ]] && return 0
    done
    _AC_TRACKED_APP_NAMES+=("$name")
    return 0
}

# Inverse of _ac_safe_name for ASCII names. Because the encoding is injective
# it can be decoded directly, so a project name survives a write/read cycle
# even when no registry entry exists (for example when a caller assigns a
# per-project variable without going through the config parser).
_ac_decode_name() {
    local s="${1:-}"
    local out="" i=0 c n
    n=${#s}
    while (( i < n )); do
        c="${s:$i:1}"
        if [[ "$c" == "_" && "${s:$((i + 1)):2}" =~ ^[0-9a-f][0-9a-f]$ ]]; then
            out+="$(printf "\\x${s:$((i + 1)):2}")"
            i=$(( i + 3 ))
        else
            out+="$c"
            i=$(( i + 1 ))
        fi
    done
    printf '%s' "$out"
}

# Resolve a safe suffix back to the original project name.
_ac_project_name() {
    local safe="${1:-}"
    if [[ -n "${_AC_PROJ_NAME[$safe]+set}" ]]; then
        printf '%s' "${_AC_PROJ_NAME[$safe]}"
    else
        _ac_decode_name "$safe"
    fi
}

# Reproduce the pre-bfde9cc "collapsing" name sanitizer: every byte that is not
# alphanumeric became a single underscore. Used only to locate orphaned legacy
# config keys so their values can be migrated onto the current injective keys.
_ac_legacy_safe_name() {
    local s="${1:-}" out="" i c
    for (( i = 0; i < ${#s}; i++ )); do
        c="${s:$i:1}"
        case "$c" in
            [a-zA-Z0-9]) out+="$c" ;;
            *) out+="_" ;;
        esac
    done
    printf '%s' "$out"
}

# Migrate per-project settings stored under legacy collapsed keys onto the
# current injective keys. Iterates the ACTUAL project directories (not just
# config-registered names) because the corruption typically leaves settings on
# an orphan underscore key while the real directory name is hyphenated and has
# no entry of its own. For each real directory whose current-encoding variables
# are empty but whose legacy-encoding variables hold a value, copy them across.
# Only fills gaps — a value already present under the current key always wins,
# so re-running is safe and idempotent.
_ac_migrate_legacy_project_keys() {
    local prop safe legacy cur_var leg_var d name
    local -A seen=()
    local -a names=()

    # Candidate names: every real subdirectory of the configured roots ...
    local root
    for root in "$_AC_PROJECTS_DIR" "${_AC_EXTRA_DIRS[@]+"${_AC_EXTRA_DIRS[@]}"}"; do
        [[ -n "$root" && -d "$root" ]] || continue
        for d in "$root"/*/; do
            [[ -d "$d" ]] || continue
            name="$(basename "$d")"
            [[ -n "${seen[$name]+x}" ]] && continue
            seen[$name]=1; names+=("$name")
        done
    done
    # ... plus every name recorded during config parsing (covers projects whose
    # directory is not currently present but whose settings should still map).
    for safe in $(_ac_assoc_keys _AC_PROJ_NAME); do
        name="${_AC_PROJ_NAME[$safe]}"
        [[ -n "${seen[$name]+x}" ]] && continue
        seen[$name]=1; names+=("$name")
    done

    for name in "${names[@]+"${names[@]}"}"; do
        safe="$(_ac_safe_name "$name")"
        # The legacy config key was the collapsed name (non-alphanumerics -> "_").
        # The parser stores every key through the CURRENT _ac_safe_name, so the
        # legacy key lives in the variable table under _ac_safe_name(collapsed).
        legacy="$(_ac_safe_name "$(_ac_legacy_safe_name "$name")")"
        # If the collapsed form equals the name itself, current == legacy: skip.
        [[ "$legacy" == "$safe" ]] && continue
        for prop in CLI_FLAGS_ PROJECT_COLOR_ PROJECT_TITLE_ PROJECT_PROMPT_ PROJECT_ALIAS_; do
            cur_var="_AC_${prop}${safe}"
            leg_var="_AC_${prop}${legacy}"
            if [[ -z "$(_ac_indirect "$cur_var")" && -n "$(_ac_indirect "$leg_var")" ]]; then
                _ac_assign "$cur_var" "$(_ac_indirect "$leg_var")"
                # Ensure the migrated project is registered under its real name so
                # _ac_write_config emits it with the correct (hyphenated) key.
                _ac_register_project "$name"
            fi
        done
    done
}

_ac_clear() {
    printf '\e[2J\e[H'
}

# Split a flag string into the global array _AC_FLAG_ARGV, honouring single
# quotes, double quotes, and backslash escapes.
#
# Deliberately implemented without `eval`: flag strings come from the config
# file, which is data, never code. This lets a flag value legitimately contain
# spaces (e.g. --agent "my agent") which plain word-splitting cannot express.
_ac_split_flags() {
    local s="${1:-}"
    _AC_FLAG_ARGV=()
    local n=${#s} i=0 c cur="" in_tok=0 quote=""

    while (( i < n )); do
        c="${s:$i:1}"
        if [[ -n "$quote" ]]; then
            if [[ "$c" == "$quote" ]]; then
                quote=""
            elif [[ "$c" == '\' && "$quote" == '"' ]]; then
                i=$(( i + 1 ))
                cur+="${s:$i:1}"
            else
                cur+="$c"
            fi
        else
            case "$c" in
                "'"|'"')
                    quote="$c"; in_tok=1 ;;
                '\')
                    i=$(( i + 1 )); cur+="${s:$i:1}"; in_tok=1 ;;
                ' '|$'\t')
                    if (( in_tok )); then
                        _AC_FLAG_ARGV+=("$cur"); cur=""; in_tok=0
                    fi ;;
                *)
                    cur+="$c"; in_tok=1 ;;
            esac
        fi
        i=$(( i + 1 ))
    done

    if (( in_tok )); then
        _AC_FLAG_ARGV+=("$cur")
    fi
    return 0
}

# Remove tab-launch scripts left behind by earlier runs.
#
# The generated script deletes itself when it starts, but that cannot happen if
# the terminal never launched it, or on Windows where unlinking an open file
# fails. Sweeping anything older than a day keeps the temp directory clean.
_ac_sweep_stale_tab_scripts() {
    local tmpdir="${TMPDIR:-/tmp}"
    [[ -d "$tmpdir" ]] || return 0
    find "$tmpdir" -maxdepth 1 -name 'ac-tab-*.sh' -type f -mtime +0 -delete 2>/dev/null
    return 0
}

# Run a command with a spinner indicator.
# Usage: _ac_spin "message" command [args...]
# Output from the command is suppressed; on failure the exit code is returned.
# Shared implementation for _ac_spin and _ac_spin_verbose (they differed only
# in whether captured output is shown on failure).
# Usage: _ac_spin_run <verbose:0|1> "message" command [args...]
_ac_spin_run() {
    local verbose="$1" msg="$2"; shift 2
    local -a frames=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
    local c_dim=$'\e[2m'
    local c_cyan=$'\e[1;36m'
    local c_green=$'\e[1;32m'
    local reset=$'\e[0m'
    local i=0 pid
    local tmpout="/dev/null"
    local -a own_tmp=()
    if (( verbose )); then
        tmpout=$(mktemp)
        own_tmp=("$tmpout")
    fi

    # Run command in background, capturing output only when verbose
    "$@" >"$tmpout" 2>&1 &
    pid=$!

    # Animate spinner while command runs
    printf "  %s%s%s %s" "$c_cyan" "${frames[0]}" "$reset" "$msg"
    while kill -0 "$pid" 2>/dev/null; do
        printf "\r  %s%s%s %s" "$c_cyan" "${frames[i % ${#frames[@]}]}" "$reset" "$msg"
        ((i++))
        sleep 0.08
    done

    # Get exit status
    wait "$pid"
    local rc=$?

    # Clear spinner line and show result
    printf "\r"
    if [[ $rc -eq 0 ]]; then
        printf "  %s✓%s %s\n" "$c_green" "$reset" "$msg"
    else
        printf "  %s✗%s %s %s(failed)%s\n" $'\e[1;31m' "$reset" "$msg" "$c_dim" "$reset"
        if (( verbose )); then
            # Show captured output on failure
            while IFS= read -r line; do
                printf "    %s%s%s\n" "$c_dim" "$line" "$reset"
            done < "$tmpout"
        fi
    fi
    (( ${#own_tmp[@]} > 0 )) && rm -f "${own_tmp[@]}"
    return $rc
}

# Run a command with a spinner indicator. Output is always suppressed.
# Usage: _ac_spin "message" command [args...]
_ac_spin() {
    _ac_spin_run 0 "$@"
}

# Run a command with a spinner, capturing output on failure.
# Usage: _ac_spin_verbose "message" command [args...]
# On success: shows ✓. On failure: shows ✗ and prints captured output.
_ac_spin_verbose() {
    _ac_spin_run 1 "$@"
}

# Idempotent (like _AC_MENU_TIMEOUT below): re-sourcing this file must never
# clobber a path a caller deliberately set beforehand -- an unconditional
# assignment here bit us once already (an exploratory `_AC_CONFIG_FILE=...;
# source anycli-launcher.bash` silently reset it back to the real path,
# and a subsequent _ac_write_config call landed on a live config instead of
# the intended throwaway one).
_AC_CONFIG_FILE="${_AC_CONFIG_FILE:-${HOME}/.config/anycli-launcher/config.yaml}"

# Seconds the menu waits for input before giving up.
_AC_MENU_TIMEOUT="${_AC_MENU_TIMEOUT:-300}"

# --- YAML Config Parser ---

# --- YAML scalar quoting ---
#
# Values were previously written raw, which produced invalid YAML: a title of
# "# sharp" became "title: # sharp" (a comment, i.e. null to any real parser)
# and a value containing ": " was ambiguous. Always emit a double-quoted
# scalar with backslash and quote escaped.
# Replace a leading $HOME with "~" for display/storage -- e.g.
# "/home/eric/projects" -> "~/projects". Written as an explicit prefix check
# rather than "${v/#$HOME/\~}": that pattern-substitution idiom handles the
# escaped tilde differently in bash (produces "~") than in zsh (produces the
# literal two characters "\~", even under `emulate -L bash` -- this specific
# escaping quirk isn't one of the things bash emulation changes), so it wrote
# a broken "\~/projects" into config.yaml whenever this ran under zsh.
_ac_tildify() {
    local v="${1:-}"
    if [[ -n "$HOME" && "$v" == "$HOME"* ]]; then
        v="~${v#"$HOME"}"
    fi
    printf '%s' "$v"
}

_ac_yaml_quote() {
    local v="${1:-}"
    v="${v//\\/\\\\}"
    v="${v//\"/\\\"}"
    printf '"%s"' "$v"
}

# Inverse of _ac_yaml_quote. Strips one layer of surrounding quotes and
# unescapes. Bare (unquoted) values are returned as-is so hand-written config
# files keep working.
_ac_yaml_unquote() {
    local v="${1:-}"
    # Trailing whitespace is not significant in unquoted YAML scalars
    if [[ "$v" == '"'*'"' && ${#v} -ge 2 ]]; then
        v="${v:1:${#v}-2}"
        v="${v//\\\"/\"}"
        v="${v//\\\\/\\}"
    elif [[ "$v" == "'"*"'" && ${#v} -ge 2 ]]; then
        v="${v:1:${#v}-2}"
        v="${v//\'\'/\'}"
    fi
    printf '%s' "$v"
}

# Portable mtime (epoch seconds) of a file, or "" if it doesn't exist. Used by
# hot loops to skip a redundant _ac_load_config (full YAML re-parse + legacy
# migration scan) when the config file hasn't changed since it was last read.
_ac_file_mtime() {
    local f="${1:-}"
    [[ -f "$f" ]] || { printf ''; return 0; }
    stat -c %Y "$f" 2>/dev/null || stat -f %m "$f" 2>/dev/null || printf '0'
}

_ac_load_config() {
    # Defaults
    _AC_PROJECTS_DIR="${HOME}/projects"
    _AC_CLI_FLAGS=""
    # No privileged default CLI -- anycli-launcher launches whatever tool is
    # configured (via the first-run wizard or config.yaml's cli_tool), not
    # one AI CLI singled out over the others in _AC_CLIS.
    _AC_CLI_TOOL=""
    _AC_CLI_LAUNCH=""
    # Override for the current CLI's config folder ("~" unexpanded). Empty
    # means "use the hardcoded default from _AC_CLIS for the current tool" --
    # see _ac_resolved_config_dir.
    _AC_CLI_CONFIG_DIR=""
    _AC_AUTO_UPDATE_CHECK="true"
    _AC_THEME="Default"
    _AC_SORT_ORDER="name"
    _AC_SCAN_DEPTH="1"
    _AC_SHOW_GIT_STATUS="false"
    _AC_SHOW_SESSIONS="false"
    _AC_EXTRA_DIRS=()
    # Clear per-project flags/color/title/prompt/alias
    local varname prefix
    for prefix in "${_AC_PROJ_PROP_PREFIXES[@]}"; do
        for varname in $(_ac_vars_with_prefix "$prefix"); do
            unset "$varname"
        done
    done
    _AC_PROJ_NAME=()
    for varname in $(_ac_vars_with_prefix _AC_APP_); do
        unset "$varname"
    done
    _AC_TRACKED_APP_NAMES=()

    # Auto-migrate legacy flat config to YAML
    local _AC_LEGACY_CONFIG="${HOME}/.config/anycli-launcher/config"
    if [[ ! -f "$_AC_CONFIG_FILE" && -f "$_AC_LEGACY_CONFIG" ]]; then
        _ac_migrate_legacy "$_AC_LEGACY_CONFIG"
    fi

    # Parse YAML config if it exists
    if [[ -f "$_AC_CONFIG_FILE" ]]; then
        _ac_parse_yaml "$_AC_CONFIG_FILE"
        # Repair configs written before _ac_safe_name became injective (commit
        # bfde9cc). Old keys collapsed every non-alphanumeric byte to "_", so a
        # project like "public-knowledge-network" was stored under the key
        # "public_knowledge_network". The new injective encoding is
        # "public_2dknowledge_2dnetwork", so those old keys no longer resolve and
        # per-project color/title/prompt/alias/flags appeared to vanish. This
        # remaps any orphaned legacy keys onto the current encoding in-memory.
        _ac_migrate_legacy_project_keys
    fi

    # Env var overrides config file
    [[ -n "${AC_PROJECTS_DIR:-}" ]] && _AC_PROJECTS_DIR="$AC_PROJECTS_DIR"

    # Explicit success: the guard above evaluates false whenever
    # AC_PROJECTS_DIR is unset, which used to make this function return 1 in
    # the common case and silently break "_ac_load_config && ..." callers.
    return 0
}

_ac_migrate_legacy() {
    local legacy_file="$1"
    local dir
    dir="$(dirname "$_AC_CONFIG_FILE")"
    mkdir -p "$dir"

    # Parse legacy flat config
    while IFS='=' read -r key value || [[ -n "$key" ]]; do
        [[ -z "$key" || "$key" =~ ^[[:space:]]*# ]] && continue
        key="${key#"${key%%[![:space:]]*}"}"
        key="${key%"${key##*[![:space:]]}"}"
        value="${value#"${value%%[![:space:]]*}"}"
        value="${value%"${value##*[![:space:]]}"}"

        case "$key" in
            PROJECTS_DIR)    _AC_PROJECTS_DIR="$value" ;;
            CLI_FLAGS)      _AC_CLI_FLAGS="$value" ;;
            CLI_FLAGS_*)
                local proj="${key#CLI_FLAGS_}"
                local safe
                safe="$(_ac_safe_name "$proj")"
                _ac_register_project "$proj"
                _ac_assign "_AC_CLI_FLAGS_${safe}" "$value"
                ;;
        esac
    done < "$legacy_file"

    # Write new YAML config
    _ac_write_config

    # Rename old file so migration doesn't repeat -- but only once the new
    # config actually landed. A failed or truncated write (disk full,
    # permissions) previously still renamed the legacy file away, leaving
    # neither a working config nor a way to retry the migration next run.
    if [[ -s "$_AC_CONFIG_FILE" ]] && grep -q '^projects_dir:' "$_AC_CONFIG_FILE" 2>/dev/null; then
        mv "$legacy_file" "${legacy_file}.bak" 2>/dev/null
    fi
}

_ac_parse_yaml() {
    local file="$1"
    local section="" project=""

    while IFS= read -r line || [[ -n "$line" ]]; do
        # Skip comments and blank lines
        [[ "$line" =~ ^[[:space:]]*# ]] && continue
        [[ "$line" =~ ^[[:space:]]*$ ]] && continue

        # Top-level scalar: key: value
        if [[ "$line" =~ ^([a-z_]+):[[:space:]]+(.*) ]]; then
            local key="${BASH_REMATCH[1]}"
            local value="${BASH_REMATCH[2]}"
            value="${value%"${value##*[![:space:]]}"}"
            value="$(_ac_yaml_unquote "$value")"
            case "$value" in
                "~"*) value="${HOME}${value#"~"}" ;;
            esac
            case "$key" in
                projects_dir)  _AC_PROJECTS_DIR="$value" ;;
                default_flags) _AC_CLI_FLAGS="$value" ;;
                cli_tool)      _AC_CLI_TOOL="$value" ;;
                cli_launch)    _AC_CLI_LAUNCH="$value" ;;
                cli_config_dir) _AC_CLI_CONFIG_DIR="$value" ;;
                auto_update_check) _AC_AUTO_UPDATE_CHECK="$value" ;;
                theme)         _AC_THEME="$value" ;;
                sort_order)    _AC_SORT_ORDER="$value" ;;
                scan_depth)    _AC_SCAN_DEPTH="$value" ;;
                show_git_status) _AC_SHOW_GIT_STATUS="$value" ;;
                show_sessions) _AC_SHOW_SESSIONS="$value" ;;
            esac
            section=""
            project=""
        # Section header: key:
        elif [[ "$line" =~ ^([a-z_]+):[[:space:]]*$ ]]; then
            section="${BASH_REMATCH[1]}"
            project=""
        # 2-space indent name (project), double-quoted: '  "name":'
        elif [[ "$line" =~ ^[[:space:]]{2}(\"([^\"]|\\.)*\"):[[:space:]]*$ ]]; then
            if [[ "$section" == "projects" ]]; then
                project="$(_ac_yaml_unquote "${BASH_REMATCH[1]}")"
                _ac_register_project "$project"
            elif [[ "$section" == "tracked_apps" ]]; then
                project="$(_ac_yaml_unquote "${BASH_REMATCH[1]}")"
                _ac_register_app "$project"
            fi
        # 2-space indent name (project), bare: "  name:"
        elif [[ "$line" =~ ^[[:space:]]{2}([^[:space:]][^:]*):[[:space:]]*$ ]]; then
            if [[ "$section" == "projects" ]]; then
                project="$(_ac_yaml_unquote "${BASH_REMATCH[1]}")"
                _ac_register_project "$project"
            elif [[ "$section" == "tracked_apps" ]]; then
                project="$(_ac_yaml_unquote "${BASH_REMATCH[1]}")"
                _ac_register_app "$project"
            fi
        # List item under a top-level sequence key: "  - value"
        elif [[ "$line" =~ ^[[:space:]]{2}-[[:space:]]+(.*) ]]; then
            if [[ "$section" == "extra_dirs" ]]; then
                local item="${BASH_REMATCH[1]}"
                item="${item%"${item##*[![:space:]]}"}"
                item="$(_ac_yaml_unquote "$item")"
                case "$item" in
                    "~"*) item="${HOME}${item#"~"}" ;;
                esac
                [[ -n "$item" ]] && _AC_EXTRA_DIRS+=("$item")
            fi
        # 4-space indent: project properties
        elif [[ "$line" =~ ^[[:space:]]{4}([a-z_]+):[[:space:]]+(.*) ]]; then
            if [[ "$section" == "tracked_apps" && -n "$project" ]]; then
                local akey="${BASH_REMATCH[1]}"
                local aval="${BASH_REMATCH[2]}"
                aval="${aval%"${aval##*[![:space:]]}"}"
                aval="$(_ac_yaml_unquote "$aval")"
                case "$aval" in
                    "~"*) aval="${HOME}${aval#"~"}" ;;
                esac
                local asafe
                asafe="$(_ac_safe_name "$project")"
                case "$akey" in
                    path)            _ac_assign "_AC_APP_PATH_${asafe}" "$aval" ;;
                    version_file)    _ac_assign "_AC_APP_VERFILE_${asafe}" "$aval" ;;
                    version_key)     _ac_assign "_AC_APP_VERKEY_${asafe}" "$aval" ;;
                    version_pattern) _ac_assign "_AC_APP_VERPAT_${asafe}" "$aval" ;;
                esac
            elif [[ "$section" == "projects" && -n "$project" ]]; then
                local pkey="${BASH_REMATCH[1]}"
                local val="${BASH_REMATCH[2]}"
                val="${val%"${val##*[![:space:]]}"}"
                val="$(_ac_yaml_unquote "$val")"
                local safe
                safe="$(_ac_safe_name "$project")"
                case "$pkey" in
                    flags) _ac_assign "_AC_CLI_FLAGS_${safe}" "$val" ;;
                    color) _ac_assign "_AC_PROJECT_COLOR_${safe}" "$val" ;;
                    title) _ac_assign "_AC_PROJECT_TITLE_${safe}" "$val" ;;
                    prompt) _ac_assign "_AC_PROJECT_PROMPT_${safe}" "$val" ;;
                    alias) _ac_assign "_AC_PROJECT_ALIAS_${safe}" "$val" ;;
                esac
            fi
        fi
    done < "$file"
}

# Build the argv for the configured CLI and run it in the current tab.
# Flags and the launch subcommand are split quote-aware, so a flag value may
# legitimately contain spaces.
_ac_run_cli() {
    local flags="${1:-}" prompt="${2:-}" resume="${3:-}"
    if [[ -z "$_AC_CLI_TOOL" ]]; then
        printf '  No CLI tool configured yet. Run "ac" then "c" to set one.\n' >&2
        return 1
    fi
    local -a argv=()
    if [[ -n "$_AC_CLI_LAUNCH" ]]; then
        _ac_split_flags "$_AC_CLI_LAUNCH"
        argv+=("${_AC_FLAG_ARGV[@]+"${_AC_FLAG_ARGV[@]}"}")
    fi
    # Resume/picker: insert the appropriate flag right after the launch
    # subcommand and suppress the per-project prompt (an ongoing or selected
    # session must not be re-seeded). "$resume" is a mode: "picker" selects a
    # session interactively, any other non-empty value resumes the most recent.
    if [[ -n "$resume" ]]; then
        local rflag
        if [[ "$resume" == "picker" ]]; then
            rflag="$(_ac_cli_picker_flag "$_AC_CLI_TOOL")"
        else
            rflag="$(_ac_cli_resume_flag "$_AC_CLI_TOOL")"
        fi
        if [[ -n "$rflag" ]]; then
            argv+=("$rflag")
            prompt=""
        else
            printf '  %s has no session %s support — starting a fresh session.\n' \
                "$_AC_CLI_TOOL" "$([[ "$resume" == "picker" ]] && echo picker || echo resume)"
        fi
    fi
    _ac_split_flags "$flags"
    argv+=("${_AC_FLAG_ARGV[@]+"${_AC_FLAG_ARGV[@]}"}")
    [[ -n "$prompt" ]] && argv+=("$prompt")
    "$_AC_CLI_TOOL" "${argv[@]+"${argv[@]}"}"
}

_ac_get_flags() {
    local dirname="$1"
    local safe_dirname
    safe_dirname="$(_ac_safe_name "$dirname")"
    local varname="_AC_CLI_FLAGS_${safe_dirname}"
    local val="$(_ac_indirect "$varname")"

    # Check if project has flags configured
    if _ac_indirect_set "$varname"; then
        # "none" means suppress inheritance — start empty
        if [[ "$val" == "none" ]]; then
            echo ""
        elif [[ -n "$val" && -n "$_AC_CLI_FLAGS" ]]; then
            # Inherit default + extend with project flags
            echo "$_AC_CLI_FLAGS $val"
        elif [[ -n "$val" ]]; then
            echo "$val"
        else
            # Empty string set explicitly = inherit only defaults
            echo "$_AC_CLI_FLAGS"
        fi
    else
        # No project config — use defaults only
        echo "$_AC_CLI_FLAGS"
    fi
}

# Emit the tab-launch script to stdout: a self-deleting bash script that sets
# the tab title, cd's into the project, drains stray terminal-handshake bytes,
# and execs the CLI. Pure (writes nothing to disk itself) so its content can
# be tested by capturing stdout, rather than writing a real temp file and
# reading it back through a mocked wt.exe.
#
# Args: <script_file> <title> <dir> <launch_argv...>
#   script_file  the path this script will be written to by the caller --
#                used only for its own self-delete line
#   title        tab title (emitted as an OSC 0 escape), or empty for none
#   dir          project directory to cd into
#   launch_argv  the fully-resolved argv to exec (CLI binary path, launch
#                subcommand, resume/picker flag, split flags, prompt)
_ac_emit_tab_script() {
    local script_file="$1" title="$2" dir="$3"
    shift 3
    local -a launch_argv=("$@")

    # Every interpolated value is shell-quoted with printf %q so that directory
    # names, titles, and prompts containing quotes or shell metacharacters are
    # passed as literal data and cannot inject commands.
    #
    # The script removes itself as its first action rather than its last: the
    # final command runs via exec, so anything after it would never run.
    printf '#!/usr/bin/env bash\n'
    printf 'rm -f -- %q\n' "$script_file"
    printf 'export _AC_SPAWNED_TAB=1\n'
    if [[ -n "$title" ]]; then
        printf 'printf %q\n' "$(printf '\e]0;%s\a' "$title")"
    fi
    printf 'cd %q || exit 1\n' "$dir"
    # Drain any pending terminal-reply bytes (device-attribute / cursor-position
    # / OSC color-query responses from the wt.exe tab handshake) so they don't
    # leak into the CLI's prompt buffer as stray input (e.g. "2c/3434") on
    # launch.
    #
    # A single fixed-timeout read is not enough: the handshake replies arrive
    # asynchronously and can land *after* one read window closes. Instead we
    # put the TTY into raw, no-echo mode and drain in a loop, giving the
    # terminal an initial settle window and continuing until an iteration
    # reads zero bytes. The TTY state is always restored before exec so the
    # CLI inherits a clean terminal.
    # The single quotes below are intentional: these printf calls emit
    # literal shell code into the generated tab script. Variables like
    # $_saved and $_junk must NOT expand at generation time — they belong
    # to the spawned script's runtime. (SC2016 suppressed in .shellcheckrc.)
    printf '%s\n' '_ac_drain_tty() {'
    printf '%s\n' '    [[ -t 0 ]] || return 0'
    printf '%s\n' '    local _saved'
    printf '%s\n' '    _saved="$(stty -g 2>/dev/null)" || return 0'
    printf '%s\n' '    trap '\''stty "$_saved" 2>/dev/null'\'' INT TERM'
    printf '%s\n' '    stty raw -echo min 0 time 0 2>/dev/null'
    printf '%s\n' '    # Let the wt.exe tab handshake replies arrive.'
    printf '%s\n' '    sleep 0.15'
    printf '%s\n' '    local _junk _i'
    printf '%s\n' '    for ((_i=0; _i<50; _i++)); do'
    printf '%s\n' '        _junk=""'
    printf '%s\n' '        IFS= read -r -t 0.05 -n 4096 _junk 2>/dev/null'
    printf '%s\n' '        [[ -z "$_junk" ]] && break'
    printf '%s\n' '    done'
    printf '%s\n' '    trap - INT TERM'
    printf '%s\n' '    stty "$_saved" 2>/dev/null'
    printf '%s\n' '}'
    printf '%s\n' '_ac_drain_tty'
    printf 'exec'
    printf ' %q' "${launch_argv[@]}"
    printf '\n'
}

# Build the wt.exe invocation for opening a new tab, into the global array
# _AC_WT_ARGS. A pure function of its arguments plus _ac_platform() (no
# filesystem or process side effects), so each platform's exact argv can be
# asserted directly instead of mocking wt.exe and grepping a captured log.
#
# Args: <dir> <script_file> <title> <color>
# Reads: $WT_PROFILE_ID, $WSL_DISTRO_NAME (both optional, same as before this
# was extracted) and the platform from _ac_platform().
_ac_build_wt_invocation() {
    local dir="$1" script_file="$2" title="$3" color="$4"
    _AC_WT_ARGS=("-w" "0" "new-tab")
    [[ -n "${WT_PROFILE_ID:-}" ]] && _AC_WT_ARGS+=("--profile" "$WT_PROFILE_ID")
    [[ -n "$title" ]] && _AC_WT_ARGS+=("--title" "$title")
    [[ -n "$color" ]] && _AC_WT_ARGS+=("--tabColor" "$color")

    # Platform-specific wt.exe invocation. See _ac_platform for the single
    # detection seam every platform-specific branch in the file shares.
    case "$(_ac_platform)" in
        wsl)
            local distro="${WSL_DISTRO_NAME:-Ubuntu}"
            # Do NOT pin wt.exe's --startingDirectory to a \\wsl.localhost\...
            # UNC path: WSL cannot set its working directory to a UNC path, so
            # wsl.exe fails with "Wsl/ERROR_PATH_NOT_FOUND" before the tab
            # script runs. Instead pass the Linux path to `wsl.exe --cd`,
            # which is the supported way to set a WSL invocation's working
            # directory. The generated script also `cd`s to the project as a
            # belt-and-braces fallback.
            _AC_WT_ARGS+=("wsl.exe" "-d" "$distro" "--cd" "$dir" "--" "bash" "-l" "$script_file")
            ;;
        gitbash)
            # Git Bash on native Windows — convert paths for wt.exe (Windows process)
            # Use env to set _AC_SPAWNED_TAB BEFORE bash sources .bashrc
            # Use -- to separate wt.exe options from the command
            local win_script win_bash win_env win_dir
            win_script="$(cygpath -w "$script_file" 2>/dev/null || echo "$script_file")"
            win_bash="$(cygpath -w "$(command -v bash)" 2>/dev/null || echo "bash")"
            win_env="$(cygpath -w "$(command -v env)" 2>/dev/null || echo "env")"
            # Pin the starting directory to the project (Windows path) so a stale
            # Windows Terminal default-profile startingDirectory cannot break the
            # launch with "Could not access starting directory" (error 0x8007010b).
            win_dir="$(cygpath -w "$dir" 2>/dev/null || true)"
            [[ -n "$win_dir" ]] && _AC_WT_ARGS+=("--startingDirectory" "$win_dir")
            _AC_WT_ARGS+=("--" "$win_env" "_AC_SPAWNED_TAB=1" "$win_bash" "-l" "$win_script")
            ;;
        *)
            _AC_WT_ARGS+=("bash" "-l" "$script_file")
            ;;
    esac
    return 0
}

_ac_launch_project() {
    local dir="${1%/}"
    local resume="${2:-}"
    local dirname="${dir##*/}"
    # Track usage so the menu can offer recency ordering
    _ac_record_launch "$dirname"
    local safe
    safe="$(_ac_safe_name "$dirname")"
    local title="$(_ac_indirect "_AC_PROJECT_TITLE_${safe}")"
    local color="$(_ac_indirect "_AC_PROJECT_COLOR_${safe}")"
    local prompt="$(_ac_indirect "_AC_PROJECT_PROMPT_${safe}")"
    local flags
    flags=$(_ac_get_flags "$dirname")

    # If no color/title configured, just run in current tab
    if [[ -z "$color" && -z "$title" ]]; then
        cd "$dir" || return 1
        _ac_run_cli "$flags" "$prompt" "$resume"
        return
    fi

    # Open in a new tab via wt.exe (cached lookup)
    if [[ -z "${_AC_WT_BIN+x}" ]]; then
        _AC_WT_BIN="$(command -v wt.exe 2>/dev/null)" || \
        _AC_WT_BIN="$(find /mnt/c/Users/*/AppData/Local/Microsoft/WindowsApps -name "wt.exe" 2>/dev/null | head -1)" || \
        _AC_WT_BIN="$(find /c/Users/*/AppData/Local/Microsoft/WindowsApps -name "wt.exe" 2>/dev/null | head -1)" || \
        _AC_WT_BIN=""
    fi

    if [[ -z "$_AC_WT_BIN" ]]; then
        # Fallback: no wt.exe, run in current tab
        cd "$dir" || return 1
        _ac_run_cli "$flags" "$prompt" "$resume"
        return
    fi

    # Build the script to run in the new tab.
    #
    # Resolve the CLI to a real executable path via _ac_resolve_bin, NOT
    # `command -v`. When the tool name is shadowed by a shell alias (e.g. a
    # security wrapper aliases `kiro-cli` to `kiro-guard`), `command -v
    # kiro-cli` prints the alias definition text ("alias kiro-cli='kiro-guard'"),
    # and exec-ing that literal fails with "exec: alias ...: not found" (exit
    # 127). _ac_resolve_bin only ever yields an executable path, ignoring
    # aliases and functions.
    local cli_bin
    cli_bin="$(_ac_resolve_bin "$_AC_CLI_TOOL")"
    if [[ -z "$cli_bin" ]]; then
        # No executable on PATH (name may only be a shell alias/function, which a
        # non-interactive tab cannot use). Fall back to running in the current tab.
        printf '  %s not found on PATH — running in current tab.\n' "$_AC_CLI_TOOL"
        cd "$dir" || return 1
        _ac_run_cli "$flags" "$prompt" "$resume"
        return
    fi

    _ac_sweep_stale_tab_scripts

    local script_file
    script_file="$(mktemp "${TMPDIR:-/tmp}/ac-tab-XXXXXXXX.sh" 2>/dev/null)" || return 1
    chmod 700 "$script_file" 2>/dev/null

    # argv[0] is the resolved executable path from _ac_resolve_bin. The spawned tab
    # runs a NON-interactive `bash -l`, and .bashrc's standard interactive guard
    # (`case $- in *) return;;`) returns before user aliases are defined — so the
    # security-wrapper alias (kiro-cli -> kiro-guard) is not available in the tab
    # regardless of expand_aliases. Exec the real binary path directly; this both
    # fixes the exit-127 crash and is the only thing that actually works here.
    local -a launch_argv=("$cli_bin")
    [[ -n "$_AC_CLI_LAUNCH" ]] && launch_argv+=("$_AC_CLI_LAUNCH")
    # Resume/picker: insert the appropriate flag after the subcommand and drop
    # the prompt, matching the current-tab behavior in _ac_run_cli. "$resume" is
    # a mode: "picker" selects interactively, any other non-empty value resumes
    # the most recent. Unsupported CLIs emit no flag and start fresh.
    if [[ -n "$resume" ]]; then
        local rflag
        if [[ "$resume" == "picker" ]]; then
            rflag="$(_ac_cli_picker_flag "$_AC_CLI_TOOL")"
        else
            rflag="$(_ac_cli_resume_flag "$_AC_CLI_TOOL")"
        fi
        if [[ -n "$rflag" ]]; then
            launch_argv+=("$rflag")
            prompt=""
        fi
    fi
    _ac_split_flags "$flags"
    launch_argv+=("${_AC_FLAG_ARGV[@]+"${_AC_FLAG_ARGV[@]}"}")
    [[ -n "$prompt" ]] && launch_argv+=("$prompt")

    _ac_emit_tab_script "$script_file" "$title" "$dir" "${launch_argv[@]}" > "$script_file"
    chmod +x "$script_file"

    _ac_build_wt_invocation "$dir" "$script_file" "$title" "$color"

    "$_AC_WT_BIN" "${_AC_WT_ARGS[@]}" 2>/dev/null &
    disown
    _AC_LAUNCHED_NEW_TAB=1
}

# --- YAML Config Writer ---
_ac_write_config() {
    local dir
    dir="$(dirname "$_AC_CONFIG_FILE")"
    mkdir -p "$dir"

    {
        echo "projects_dir: $(_ac_yaml_quote "$(_ac_tildify "$_AC_PROJECTS_DIR")")"
        echo "default_flags: $(_ac_yaml_quote "$_AC_CLI_FLAGS")"
        echo "cli_tool: $(_ac_yaml_quote "$_AC_CLI_TOOL")"
        echo "cli_launch: $(_ac_yaml_quote "$_AC_CLI_LAUNCH")"
        echo "cli_config_dir: $(_ac_yaml_quote "$(_ac_tildify "$_AC_CLI_CONFIG_DIR")")"
        echo "auto_update_check: $(_ac_yaml_quote "$_AC_AUTO_UPDATE_CHECK")"
        echo "theme: $(_ac_yaml_quote "$_AC_THEME")"
        echo "sort_order: $(_ac_yaml_quote "$_AC_SORT_ORDER")"
        echo "scan_depth: $(_ac_yaml_quote "$_AC_SCAN_DEPTH")"
        echo "show_git_status: $(_ac_yaml_quote "$_AC_SHOW_GIT_STATUS")"
        echo "show_sessions: $(_ac_yaml_quote "$_AC_SHOW_SESSIONS")"
        if (( ${#_AC_EXTRA_DIRS[@]} > 0 )); then
            echo "extra_dirs:"
            local xd
            for xd in "${_AC_EXTRA_DIRS[@]}"; do
                echo "  - $(_ac_yaml_quote "$(_ac_tildify "$xd")")"
            done
        fi
        echo ""
        echo "projects:"
        # Collect all projects that have any config (using assoc array as set)
        local -A _ac_seen=()
        local -a _ac_all_projects=()
        local varname p prefix
        for prefix in "${_AC_PROJ_PROP_PREFIXES[@]}"; do
            for varname in $(_ac_vars_with_prefix "$prefix"); do
                p="${varname#"$prefix"}"
                [[ -z "${_ac_seen[$p]+x}" ]] && _ac_all_projects+=("$p")
                _ac_seen[$p]=1
            done
        done
        for proj in "${_ac_all_projects[@]:-}"; do
            [[ -z "$proj" ]] && continue
            # Emit the project name as the user wrote it. Previously the
            # sanitized variable suffix was written back, which silently
            # renamed "my-app" to "my_app" and dropped colliding entries.
            local orig
            orig="$(_ac_project_name "$proj")"
            echo "  $(_ac_yaml_quote "$orig"):"
            local flags="$(_ac_indirect "_AC_CLI_FLAGS_${proj}")"
            local color="$(_ac_indirect "_AC_PROJECT_COLOR_${proj}")"
            local title="$(_ac_indirect "_AC_PROJECT_TITLE_${proj}")"
            local prompt="$(_ac_indirect "_AC_PROJECT_PROMPT_${proj}")"
            local alias="$(_ac_indirect "_AC_PROJECT_ALIAS_${proj}")"
            [[ -n "$flags" ]]  && echo "    flags: $(_ac_yaml_quote "$flags")"
            [[ -n "$color" ]]  && echo "    color: $(_ac_yaml_quote "$color")"
            [[ -n "$title" ]]  && echo "    title: $(_ac_yaml_quote "$title")"
            [[ -n "$prompt" ]] && echo "    prompt: $(_ac_yaml_quote "$prompt")"
            [[ -n "$alias" ]]  && echo "    alias: $(_ac_yaml_quote "$alias")"
        done

        if (( ${#_AC_TRACKED_APP_NAMES[@]} > 0 )); then
            echo ""
            echo "tracked_apps:"
            local app a_path a_file a_key a_pat
            for app in "${_AC_TRACKED_APP_NAMES[@]}"; do
                [[ -z "$app" ]] && continue
                echo "  $(_ac_yaml_quote "$app"):"
                a_path="$(_ac_app_field "$app" path)"
                a_file="$(_ac_app_field "$app" version_file)"
                a_key="$(_ac_app_field "$app" version_key)"
                a_pat="$(_ac_app_field "$app" version_pattern)"
                [[ -n "$a_path" ]] && echo "    path: $(_ac_yaml_quote "$(_ac_tildify "$a_path")")"
                [[ -n "$a_file" ]] && echo "    version_file: $(_ac_yaml_quote "$a_file")"
                [[ -n "$a_key" ]]  && echo "    version_key: $(_ac_yaml_quote "$a_key")"
                [[ -n "$a_pat" ]]  && echo "    version_pattern: $(_ac_yaml_quote "$a_pat")"
            done
        fi
        :
    } > "$_AC_CONFIG_FILE"
}

# --- CLI Detection and Flag Parsing ---
# Every known AI CLI, one row per tool, single source of truth for both "what
# tools does the wizard offer" and "what can this tool do". Previously these
# were two separate tables (_AC_KNOWN_CLIS keyed by ":", _AC_CLI_CAPS keyed by
# "|") that had to be kept in sync by hand -- a tool added to one and not the
# other silently lost either its default launch subcommand or its resume
# capabilities.
#
# Format: tool|default-subcmd|resume-flag|list-sessions-argv|picker-flag|config-dir
#   - default-subcmd: launch subcommand offered by the setup wizard (empty = none)
#   - resume-flag: flag to resume the most recent conversation (empty = none)
#   - list-sessions-argv: space-separated argv to list sessions as JSON (empty = none)
#   - picker-flag: flag to interactively pick a session to resume (empty = none)
#   - config-dir: the tool's per-user config/settings folder, "~" left
#     unexpanded (empty = unknown; the menu's "config" shortcut and the
#     config-folder setting stay hidden until someone adds one, or the user
#     overrides it themselves via `c` -> Config folder)
# Empty capability fields mean the tool lacks that capability; the launcher
# never emits an unsupported flag and hides the corresponding trigger.
#
# claude is TO-VERIFY: --continue resumes the most recent conversation per its
# public docs, but this has not been confirmed against a local install. Its
# list-sessions command and picker flag are unknown, so session-aware menu
# entries and the picker are disabled for claude until verified.
_AC_CLIS=(
    "kiro-cli|chat|--resume|chat --list-sessions --format json|--resume-picker|~/.kiro"
    "claude||--continue|||~/.claude"
    "aider||||"
    "goose|session|--resume||"
    "codex||||"
    "amp||||"
)

# Split a tool's row into fields. Sets _AC_CLI_FIELDS (indexed 0..5):
# 0=tool 1=subcmd 2=resume-flag 3=list-argv 4=picker-flag 5=config-dir.
# Returns 1 if the tool isn't in the table.
_ac_cli_fields() {
    local tool="${1:-}" entry
    _AC_CLI_FIELDS=()
    for entry in "${_AC_CLIS[@]}"; do
        if [[ "${entry%%|*}" == "$tool" ]]; then
            local IFS='|'
            # zsh's `read` splits into an array with -A, not bash's -a.
            if [[ -n "${ZSH_VERSION:-}" ]]; then
                read -rA _AC_CLI_FIELDS <<< "$entry"
            else
                read -ra _AC_CLI_FIELDS <<< "$entry"
            fi
            return 0
        fi
    done
    return 1
}

# Print the resume flag for a tool, or nothing if resume is unsupported.
_ac_cli_resume_flag() {
    _ac_cli_fields "${1:-}" || return 0
    printf '%s' "${_AC_CLI_FIELDS[2]:-}"
    return 0
}

# Print the list-sessions argv (space-separated) for a tool, or nothing if the
# tool has no session-listing capability.
_ac_cli_list_argv() {
    _ac_cli_fields "${1:-}" || return 0
    printf '%s' "${_AC_CLI_FIELDS[3]:-}"
    return 0
}

# Print the picker flag for a tool, or nothing if the tool has no session picker.
_ac_cli_picker_flag() {
    _ac_cli_fields "${1:-}" || return 0
    printf '%s' "${_AC_CLI_FIELDS[4]:-}"
    return 0
}

# Print the hardcoded config-folder default for a tool ("~" left
# unexpanded), or nothing if no folder is known for it.
_ac_cli_config_dir() {
    _ac_cli_fields "${1:-}" || return 0
    printf '%s' "${_AC_CLI_FIELDS[5]:-}"
    return 0
}

# Resolve the config folder to use for the currently configured CLI: an
# explicit _AC_CLI_CONFIG_DIR override (set via the config menu) wins, else
# fall back to the tool's hardcoded default from _AC_CLIS. Expands a leading
# "~" either way. Prints nothing when neither is known -- callers hide the
# "config" shortcut in that case.
_ac_resolved_config_dir() {
    if [[ -n "$_AC_CLI_CONFIG_DIR" ]]; then
        _ac_normalize_typed_path "$_AC_CLI_CONFIG_DIR"
    else
        local default_dir
        default_dir="$(_ac_cli_config_dir "$_AC_CLI_TOOL")"
        [[ -n "$default_dir" ]] && _ac_normalize_typed_path "$default_dir"
    fi
}

_ac_detect_clis() {
    # Returns list of available CLI tools on PATH
    local -a found=()
    local entry
    for entry in "${_AC_CLIS[@]}"; do
        local tool="${entry%%|*}"
        if command -v "$tool" &>/dev/null; then
            found+=("$tool")
        fi
    done
    echo "${found[*]}"
}

_ac_get_default_launch() {
    # Get the default launch subcommand for a tool
    _ac_cli_fields "${1:-}" || return 0
    printf '%s' "${_AC_CLI_FIELDS[1]:-}"
}

_ac_parse_cli_flags() {
    # Parse flags from --help output, with caching
    # Output format: --flag or --flag=<PARAM> (one per line)
    local tool="$1"
    local subcmd="$2"
    local cache="${TMPDIR:-/tmp}/ac-flags-${tool}.cache"

    # Use cache if less than 1 hour old
    if [[ -f "$cache" ]]; then
        local age=$(( $(date +%s) - $(stat -c %Y "$cache" 2>/dev/null || stat -f %m "$cache" 2>/dev/null || echo 0) ))
        if (( age < 3600 )); then
            cat "$cache"
            return
        fi
    fi

    # Run help and parse flags. Bounded by a hard timeout -- some CLIs' --help
    # blocks on a first-run prompt, login flow, or slow network version check
    # instead of returning immediately, which would otherwise hang this whole
    # menu waiting on it forever.
    local help_output=""
    if [[ -n "$subcmd" ]]; then
        help_output=$(_ac_timeout 5 "$tool" "$subcmd" --help 2>/dev/null || _ac_timeout 5 "$tool" --help 2>/dev/null || echo "")
    else
        help_output=$(_ac_timeout 5 "$tool" --help 2>/dev/null || echo "")
    fi

    if [[ -z "$help_output" ]]; then
        echo ""
        return
    fi

    # Extract flags: detect --flag <PARAM> patterns vs bare --flag (boolean).
    # Short options are also collected: a line like "-a, --trust-all-tools"
    # contributes the long form, while a short-only option such as "-v" would
    # previously have been dropped entirely.
    local results=""
    # Angle brackets below are matched via [<]/[>] classes rather than the
    # more common \<...\> escapes: zsh's parser aborts on an unquoted \< or \>
    # inside [[ =~ ]] (it reads them as its own glob syntax, not GNU word-
    # boundary escapes), so the pattern is built in a variable using a form
    # both shells' regex engines treat as literal angle brackets.
    local re_long_angle='(--[a-z][a-z0-9_-]*)[[:space:]]+[<]([^>]+)[>]'
    local re_short_angle='(^|[[:space:]])(-[a-zA-Z])[[:space:]]+[<]([^>]+)[>]'
    while IFS= read -r hline; do
        if [[ "$hline" =~ $re_long_angle ]]; then
            results+="${BASH_REMATCH[1]}=<${BASH_REMATCH[2]}>"$'\n'
        elif [[ "$hline" =~ (--[a-z][a-z0-9_-]*)[[:space:]]+([A-Z][A-Z_]+) ]]; then
            results+="${BASH_REMATCH[1]}=<${BASH_REMATCH[2]}>"$'\n'
        elif [[ "$hline" =~ (--[a-z][a-z0-9_-]*) ]]; then
            results+="${BASH_REMATCH[1]}"$'\n'
        elif [[ "$hline" =~ $re_short_angle ]]; then
            results+="${BASH_REMATCH[2]}=<${BASH_REMATCH[3]}>"$'\n'
        elif [[ "$hline" =~ (^|[[:space:]])(-[a-zA-Z])([[:space:]]|,|$) ]]; then
            results+="${BASH_REMATCH[2]}"$'\n'
        fi
    done <<< "$help_output"

    # Deduplicate and sort, remove --help/--version
    local flags
    flags=$(sort -u <<< "$results" | grep -v '^--help' | grep -v '^--version' | grep -v '^$')

    # Cache results. Written atomically (temp file + mv) so a concurrent `ac`
    # invocation reading the cache never sees a half-written file.
    local tmp_cache
    tmp_cache="$(mktemp "${cache}.XXXXXX" 2>/dev/null)" && {
        echo "$flags" > "$tmp_cache"
        mv "$tmp_cache" "$cache" 2>/dev/null || rm -f "$tmp_cache"
    }
    echo "$flags"
}

_ac_get_flag_opts() {
    # Return flag options for the config editor (dynamic based on configured CLI)
    local flags
    flags=$(_ac_parse_cli_flags "$_AC_CLI_TOOL" "$_AC_CLI_LAUNCH")

    if [[ -z "$flags" ]]; then
        # Fallback to basic options
        echo "--trust-all-tools"
        echo "custom"
        echo "none (use default)"
        return
    fi

    # Build useful combinations from discovered flags
    local -a opts=()
    while IFS= read -r flag; do
        [[ -n "$flag" ]] && opts+=("$flag")
    done <<< "$flags"

    # Output individual flags
    printf '%s\n' "${opts[@]}"
    echo "custom"
    echo "none (use default)"
}

_ac_config_edit_cli_tool() {
    _ac_clear
    local -a available=()
    local detected
    detected=$(_ac_detect_clis)

    if [[ -z "$detected" ]]; then
        echo "  No known AI CLI tools found on PATH."
        printf "  Enter CLI tool name manually: "
        local val
        read -r val
        if [[ -n "$val" ]]; then
            _AC_CLI_TOOL="$val"
            printf "  Subcommand (blank for none): "
            local sub
            read -r sub
            _AC_CLI_LAUNCH="$sub"
            # Invalidate flag cache
            rm -f "${TMPDIR:-/tmp}/ac-flags-${val}.cache"
            _ac_write_config
            printf "  ✓ Saved: %s %s\n" "$_AC_CLI_TOOL" "$_AC_CLI_LAUNCH"
        fi
        return
    fi

    if [[ -n "${ZSH_VERSION:-}" ]]; then
        read -rA available <<< "$detected"
    else
        read -ra available <<< "$detected"
    fi

    printf "\n  Available AI CLI tools:\n"
    local i=1
    for tool in "${available[@]}"; do
        local subcmd
        subcmd=$(_ac_get_default_launch "$tool")
        local marker=""
        [[ "$tool" == "$_AC_CLI_TOOL" ]] && marker=" (current)"
        printf "    %d. %s" "$i" "$tool"
        [[ -n "$subcmd" ]] && printf " %s" "$subcmd"
        printf "%s\n" "$marker"
        ((i++))
    done
    printf "    %d. other (enter manually)\n" "$i"

    printf "\n  › "
    local _ac_inp
    read -r _ac_inp || return

    if [[ ! "$_ac_inp" =~ ^[0-9]+$ ]] || (( _ac_inp < 1 || _ac_inp > i )); then
        echo "  Invalid selection"
        return
    fi

    if (( _ac_inp == i )); then
        printf "  CLI tool name: "
        local val
        read -r val
        [[ -z "$val" ]] && return
        _AC_CLI_TOOL="$val"
        printf "  Subcommand (blank for none): "
        local sub
        read -r sub
        _AC_CLI_LAUNCH="$sub"
    else
        local selected="${available[$((_ac_inp - 1))]}"
        _AC_CLI_TOOL="$selected"
        _AC_CLI_LAUNCH=$(_ac_get_default_launch "$selected")
    fi

    # Invalidate flag cache
    rm -f "${TMPDIR:-/tmp}/ac-flags-${_AC_CLI_TOOL}.cache"
    _ac_write_config
    printf "  ✓ Saved: %s %s\n" "$_AC_CLI_TOOL" "$_AC_CLI_LAUNCH"
}

_ac_config_toggle_update_check() {
    if [[ "$_AC_AUTO_UPDATE_CHECK" == "true" ]]; then
        _AC_AUTO_UPDATE_CHECK="false"
        printf "\n  ✓ Auto-update check: disabled\n"
    else
        _AC_AUTO_UPDATE_CHECK="true"
        printf "\n  ✓ Auto-update check: enabled\n"
    fi
    _ac_write_config
    sleep 0.5
}

_ac_config_toggle_sessions() {
    if [[ "${_AC_SHOW_SESSIONS:-false}" == "true" ]]; then
        _AC_SHOW_SESSIONS="false"
        printf "\n  ✓ Session display: disabled\n"
    else
        _AC_SHOW_SESSIONS="true"
        printf "\n  ✓ Session display: enabled\n"
    fi
    _ac_write_config
    sleep 0.5
}

# Toggle sort order between the two supported values: "name" (alphabetical,
# the default) and "recent" (most-recently-launched first). The recency sort
# itself is applied in _ac_collect_projects; this only flips the stored value.
_ac_config_toggle_sort_order() {
    if [[ "${_AC_SORT_ORDER:-name}" == "recent" ]]; then
        _AC_SORT_ORDER="name"
        printf "\n  ✓ Sort order: name (alphabetical)\n"
    else
        _AC_SORT_ORDER="recent"
        printf "\n  ✓ Sort order: recent (most-recently-launched first)\n"
    fi
    _ac_write_config
    sleep 0.5
}

# Toggle the per-project git status summary shown on each menu row. Off by
# default because it shells out to git per project on every render (the render
# path guards each call and fails quiet for non-git directories).
_ac_config_toggle_git_status() {
    if [[ "${_AC_SHOW_GIT_STATUS:-false}" == "true" ]]; then
        _AC_SHOW_GIT_STATUS="false"
        printf "\n  ✓ Git status: disabled\n"
    else
        _AC_SHOW_GIT_STATUS="true"
        printf "\n  ✓ Git status: enabled\n"
    fi
    _ac_write_config
    sleep 0.5
}

# --- Theme System ---
# Idempotent, like _AC_CONFIG_FILE above -- re-sourcing must not clobber a
# path a caller (a test, an isolated shell) deliberately set beforehand.
_AC_THEME_FILE="${_AC_THEME_FILE:-${HOME}/.config/anycli-launcher/theme.bash}"

# Theme definitions: name|bg|fg|cursor|black|red|green|yellow|blue|purple|cyan|white|br_black|br_red|br_green|br_yellow|br_blue|br_purple|br_cyan|br_white|ps1_style
# PS1 styles: minimal, arrow, powerline, lambda
_AC_THEMES=(
    "Default|||||||||||||||||||||"
    "One Half Dark|#282C34|#DCDFE4|#A3B3CC|#282C34|#E06C75|#98C379|#E5C07B|#61AFEF|#C678DD|#56B6C2|#DCDFE4|#5A6374|#E06C75|#98C379|#E5C07B|#61AFEF|#C678DD|#56B6C2|#DCDFE4|arrow"
    "Catppuccin Mocha|#1E1E2E|#CDD6F4|#F5E0DC|#45475A|#F38BA8|#A6E3A1|#F9E2AF|#89B4FA|#CBA6F7|#94E2D5|#BAC2DE|#585B70|#F38BA8|#A6E3A1|#F9E2AF|#89B4FA|#CBA6F7|#94E2D5|#A6ADC8|arrow"
    "Dracula|#282A36|#F8F8F2|#F8F8F2|#21222C|#FF5555|#50FA7B|#F1FA8C|#BD93F9|#FF79C6|#8BE9FD|#F8F8F2|#6272A4|#FF6E6E|#69FF94|#FFFFA5|#D6ACFF|#FF92DF|#A4FFFF|#FFFFFF|arrow"
    "Tokyo Night|#1A1B26|#C0CAF5|#C0CAF5|#15161E|#F7768E|#9ECE6A|#E0AF68|#7AA2F7|#BB9AF7|#7DCFFF|#A9B1D6|#414868|#F7768E|#9ECE6A|#E0AF68|#7AA2F7|#BB9AF7|#7DCFFF|#C0CAF5|minimal"
    "Gruvbox Dark|#282828|#EBDBB2|#EBDBB2|#282828|#CC241D|#98971A|#D79921|#458588|#B16286|#689D6A|#A89984|#928374|#FB4934|#B8BB26|#FABD2F|#83A598|#D3869B|#8EC07C|#EBDBB2|lambda"
    "Nord|#2E3440|#D8DEE9|#D8DEE9|#3B4252|#BF616A|#A3BE8C|#EBCB8B|#81A1C1|#B48EAD|#88C0D0|#E5E9F0|#4C566A|#BF616A|#A3BE8C|#EBCB8B|#81A1C1|#B48EAD|#8BE5D5|#ECEFF4|minimal"
    "Rosé Pine|#191724|#E0DEF4|#E0DEF4|#26233A|#EB6F92|#31748F|#F6C177|#9CCFD8|#C4A7E7|#EBBCBA|#E0DEF4|#6E6A86|#EB6F92|#31748F|#F6C177|#9CCFD8|#C4A7E7|#EBBCBA|#E0DEF4|arrow"
)

_ac_theme_field() {
    # Extract field by index from a theme entry
    local entry="$1" index="$2"
    local IFS='|'
    local -a fields=($entry)
    echo "${fields[$index]:-}"
}

_ac_find_python() {
    # Find a working Python interpreter.
    #
    # Deliberately does NOT cache itself: every call site invokes this via
    # command substitution (`python_bin=$(_ac_find_python)`), which forks a
    # subshell -- any variable this function assigned would be set only in
    # that subshell and vanish the instant the substitution completes,
    # making an internal cache a no-op that looks like it works but never
    # actually avoids re-detection. Callers that want caching (see the
    # _AC_WT_BIN pattern in _ac_launch_project) must cache the *result* of
    # the substitution themselves, in their own scope.
    #
    # Must verify execution because Windows Store stubs pass 'command -v' but fail at runtime
    if command -v python3 &>/dev/null && python3 --version &>/dev/null; then
        echo "python3"
        return
    fi
    if command -v python &>/dev/null && python --version &>/dev/null; then
        echo "python"
        return
    fi
    # Fallback: check common Windows Python install paths (enterprise/manual installs)
    local p
    for p in \
        "/c/Python3"*/python.exe \
        "/c/Program Files/Python3"*/python.exe \
        "/c/Program Files (x86)/Python3"*/python.exe \
        "${LOCALAPPDATA:-/nonexistent}/Programs/Python/Python3"*/python.exe \
        "${APPDATA:-/nonexistent}/../Local/Programs/Python/Python3"*/python.exe \
        "/c/Users/${USER:-nobody}/AppData/Local/Programs/Python/Python3"*/python.exe; do
        if [[ -x "$p" ]] && "$p" --version &>/dev/null; then
            echo "$p"
            return
        fi
    done
}

_ac_detect_wt_settings() {
    # Find Windows Terminal settings.json from WSL or Git Bash
    local settings=""

    # Explicit override, used by tests and by users with a non-standard install
    if [[ -n "${_AC_WT_SETTINGS_OVERRIDE:-}" ]]; then
        printf '%s' "$_AC_WT_SETTINGS_OVERRIDE"
        return 0
    fi

    # WSL path
    local wsl_path
    for wsl_path in /mnt/c/Users/*/AppData/Local/Packages/Microsoft.WindowsTerminal*/LocalState/settings.json; do
        [[ -f "$wsl_path" ]] && settings="$wsl_path" && break
    done

    # Git Bash / MSYS path
    if [[ -z "$settings" ]]; then
        local appdata="${LOCALAPPDATA:-${APPDATA:-}}"
        if [[ -n "$appdata" ]]; then
            local win_path="$appdata/Packages/Microsoft.WindowsTerminal_8wekyb3d8bbwe/LocalState/settings.json"
            [[ -f "$win_path" ]] && settings="$win_path"
        fi
    fi

    echo "$settings"
}

_ac_is_windows_terminal() {
    # Detect if we're running inside Windows Terminal (WSL or Git Bash)
    [[ -n "${WT_SESSION:-}" || -n "${WT_PROFILE_ID:-}" ]] && return 0
    # WSL: check for Windows Terminal settings file
    local settings
    settings=$(_ac_detect_wt_settings)
    [[ -n "$settings" ]] && return 0
    return 1
}

# Shared Python helpers for every settings.json mutation.
#
# Three problems this fixes:
#   1. Windows Terminal ships settings.json with // comments. json.load rejects
#      those, so theme application failed outright for most users and the error
#      surfaced only as a captured stderr blob.
#   2. The file was rewritten with open(path, "w") followed by json.dump, so an
#      exception or interrupt mid-write truncated the user's terminal config
#      with no way back. Writes now go to a temp file and are moved into place
#      with os.replace, which is atomic on POSIX and Windows.
#   3. There was no backup. One is taken before the first modification.
_AC_WT_PY_PRELUDE='
import json, os, shutil, sys, tempfile

def _strip_jsonc(text):
    """Remove // and /* */ comments that appear outside of string literals."""
    out = []
    i, n = 0, len(text)
    in_str = esc = False
    while i < n:
        c = text[i]
        if in_str:
            out.append(c)
            if esc:
                esc = False
            elif c == "\\":
                esc = True
            elif c == "\"":
                in_str = False
            i += 1
            continue
        if c == "\"":
            in_str = True
            out.append(c)
            i += 1
            continue
        if c == "/" and i + 1 < n and text[i + 1] == "/":
            while i < n and text[i] != "\n":
                i += 1
            continue
        if c == "/" and i + 1 < n and text[i + 1] == "*":
            i += 2
            while i + 1 < n and not (text[i] == "*" and text[i + 1] == "/"):
                i += 1
            i += 2
            continue
        out.append(c)
        i += 1
    return "".join(out)

def load_settings(path):
    with open(path, "r", encoding="utf-8-sig") as f:
        raw = f.read()
    try:
        return json.loads(raw)
    except ValueError:
        return json.loads(_strip_jsonc(raw))

def save_settings(path, data):
    backup = path + ".ac-backup"
    if not os.path.exists(backup):
        try:
            shutil.copy2(path, backup)
        except OSError:
            pass
    directory = os.path.dirname(path) or "."
    fd, tmp = tempfile.mkstemp(dir=directory, prefix=".ac-wt-", suffix=".json")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=4)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise

def profile_list(settings):
    return settings.setdefault("profiles", {}).setdefault("list", [])

def profile_defaults(settings):
    return settings.setdefault("profiles", {}).setdefault("defaults", {})

def profile_key(profile, index):
    return profile.get("guid") or profile.get("name") or ("#%d" % index)
'

# Where the pre-theme colorScheme of each profile is recorded so that resetting
# restores what the user had rather than deleting the field outright.
_AC_WT_SCHEME_BACKUP="${_AC_WT_SCHEME_BACKUP:-${HOME}/.config/anycli-launcher/wt-schemes.json}"

# Detect the two prerequisites every Windows Terminal settings.json mutation
# needs: the settings file itself, and a working Python interpreter to edit
# it with. On success sets _AC_WT_SETTINGS / _AC_WT_PYTHON and returns 0. On
# failure returns 1 and sets exactly one of _AC_WT_SETTINGS_MISSING /
# _AC_WT_PYTHON_MISSING so the caller can report which one -- callers differ
# in what they do after a failure (some just return, the font/opacity editor
# shows a "press any key" prompt first), so this only detects; it prints
# nothing itself. Pair with _ac_wt_prereqs_fail_msg for the standard message.
_ac_wt_prereqs() {
    _AC_WT_SETTINGS=""
    _AC_WT_PYTHON=""
    _AC_WT_SETTINGS_MISSING=""
    _AC_WT_PYTHON_MISSING=""
    _AC_WT_SETTINGS="$(_ac_detect_wt_settings)"
    if [[ -z "$_AC_WT_SETTINGS" ]]; then
        _AC_WT_SETTINGS_MISSING=1
        return 1
    fi
    _AC_WT_PYTHON="$(_ac_find_python)"
    if [[ -z "$_AC_WT_PYTHON" ]]; then
        _AC_WT_PYTHON_MISSING=1
        return 1
    fi
    return 0
}

# Print the bare (uncolored, unprefixed) description of whichever
# _ac_wt_prereqs check just failed. Deliberately bare, not "✗ Failed: ...":
# _ac_apply_theme_windows and _ac_reset_theme_windows are always invoked as
# `result=$(... 2>&1)` by _ac_config_theme, which itself prints
# "✗ Failed: %s" around whatever they produced -- a caller-supplied "✗
# Failed:" here would double up into "✗ Failed:   ✗ Failed: ...", which is
# in fact what _ac_apply_theme_windows's Python-missing message used to
# print before this was unified. Callers that display the message directly
# instead of returning it for another layer to wrap (_ac_config_theme_font)
# add their own "✗ Failed: %s" around the captured text, matching the same
# visual result.
# Must be called right after a failing _ac_wt_prereqs, before either
# _AC_WT_SETTINGS_MISSING or _AC_WT_PYTHON_MISSING is overwritten.
_ac_wt_prereqs_fail_msg() {
    if [[ -n "$_AC_WT_SETTINGS_MISSING" ]]; then
        printf "Could not find Windows Terminal settings.json\n"
    else
        printf "Python was not found. Install Python 3 or disable the Windows Store alias\n"
        printf "    (Settings > Apps > Advanced app settings > App execution aliases)."
    fi
}

_ac_apply_theme_windows() {
    # Apply theme to Windows Terminal settings.json
    local theme_entry="$1"
    local name bg fg cursor
    name=$(_ac_theme_field "$theme_entry" 0)
    bg=$(_ac_theme_field "$theme_entry" 1)
    fg=$(_ac_theme_field "$theme_entry" 2)
    cursor=$(_ac_theme_field "$theme_entry" 3)

    # Build the color scheme JSON using python (available in WSL and Git Bash)
    if ! _ac_wt_prereqs; then
        _ac_wt_prereqs_fail_msg
        return 1
    fi
    local settings="$_AC_WT_SETTINGS" python_bin="$_AC_WT_PYTHON"
    mkdir -p "$(dirname "$_AC_WT_SCHEME_BACKUP")"
    "$python_bin" -c "$_AC_WT_PY_PRELUDE"'
settings_path = sys.argv[1]
fields = sys.argv[2].split("|")
scheme_backup_path = sys.argv[3]

name, bg, fg, cursor = fields[0], fields[1], fields[2], fields[3]
colors = fields[4:20]

settings = load_settings(settings_path)

scheme = {
    "name": name,
    "background": bg,
    "foreground": fg,
    "cursorColor": cursor,
    "selectionBackground": cursor,
    "black": colors[0], "red": colors[1], "green": colors[2],
    "yellow": colors[3], "blue": colors[4], "purple": colors[5],
    "cyan": colors[6], "white": colors[7],
    "brightBlack": colors[8], "brightRed": colors[9],
    "brightGreen": colors[10], "brightYellow": colors[11],
    "brightBlue": colors[12], "brightPurple": colors[13],
    "brightCyan": colors[14], "brightWhite": colors[15],
}

schemes = settings.setdefault("schemes", [])
settings["schemes"] = [x for x in schemes if x.get("name") != name] + [scheme]

# Record what each profile used before we touch it, so that resetting can put
# it back. Previously reset simply deleted colorScheme everywhere, silently
# discarding per-profile customisation.
try:
    with open(scheme_backup_path, "r", encoding="utf-8") as f:
        saved = json.load(f)
except (OSError, ValueError):
    saved = {}

if "__defaults__" not in saved:
    saved["__defaults__"] = profile_defaults(settings).get("colorScheme")

for index, profile in enumerate(profile_list(settings)):
    key = profile_key(profile, index)
    if key not in saved:
        saved[key] = profile.get("colorScheme")
    if "colorScheme" in profile:
        profile["colorScheme"] = name

profile_defaults(settings)["colorScheme"] = name

with open(scheme_backup_path, "w", encoding="utf-8") as f:
    json.dump(saved, f, indent=2)

save_settings(settings_path, settings)
print("OK:" + name)
' "$settings" "$theme_entry" "$_AC_WT_SCHEME_BACKUP"
}

_ac_reset_theme_windows() {
    # Remove custom colorScheme from Windows Terminal, restoring defaults
    if ! _ac_wt_prereqs; then
        _ac_wt_prereqs_fail_msg
        return 1
    fi
    local settings="$_AC_WT_SETTINGS" python_bin="$_AC_WT_PYTHON"
    "$python_bin" -c "$_AC_WT_PY_PRELUDE"'
settings_path = sys.argv[1]
scheme_backup_path = sys.argv[2]

settings = load_settings(settings_path)

try:
    with open(scheme_backup_path, "r", encoding="utf-8") as f:
        saved = json.load(f)
except (OSError, ValueError):
    saved = {}

# Restore the recorded pre-theme scheme where we have one; only fall back to
# removing the key when nothing was recorded.
defaults = profile_defaults(settings)
if "__defaults__" in saved:
    previous = saved["__defaults__"]
    if previous:
        defaults["colorScheme"] = previous
    else:
        defaults.pop("colorScheme", None)
else:
    defaults.pop("colorScheme", None)

for index, profile in enumerate(profile_list(settings)):
    key = profile_key(profile, index)
    if key in saved:
        previous = saved[key]
        if previous:
            profile["colorScheme"] = previous
        else:
            profile.pop("colorScheme", None)
    else:
        profile.pop("colorScheme", None)

save_settings(settings_path, settings)

try:
    os.unlink(scheme_backup_path)
except OSError:
    pass

print("OK:Default")
' "$settings" "$_AC_WT_SCHEME_BACKUP"
}

_ac_apply_theme_linux() {
    # Apply theme as PS1 prompt + dircolors for Linux terminals
    local theme_entry="$1"
    local name fg green blue purple cyan
    name=$(_ac_theme_field "$theme_entry" 0)
    fg=$(_ac_theme_field "$theme_entry" 2)
    green=$(_ac_theme_field "$theme_entry" 6)
    blue=$(_ac_theme_field "$theme_entry" 8)
    purple=$(_ac_theme_field "$theme_entry" 9)
    cyan=$(_ac_theme_field "$theme_entry" 10)
    local ps1_style
    ps1_style=$(_ac_theme_field "$theme_entry" 20)

    # Convert hex to 256-color escape (approximate)
    # We'll use direct RGB escapes (truecolor) since most modern terminals support it
    local dir
    dir="$(dirname "$_AC_THEME_FILE")"
    mkdir -p "$dir"

    # Generate PS1 based on style
    local ps1_def=""
    case "$ps1_style" in
        minimal)
            ps1_def='PS1="\[\e[0;36m\]\w\[\e[0;35m\]\$(__git_ps1 \" (%s)\" 2>/dev/null)\[\e[0m\] \$ "'
            ;;
        arrow)
            ps1_def='PS1="\[\e[0;32m\]\u\[\e[0m\] \[\e[0;34m\]\w\[\e[0;35m\]\$(__git_ps1 \" ⎇ %s\" 2>/dev/null)\[\e[0m\] › "'
            ;;
        lambda)
            ps1_def='PS1="\[\e[0;33m\]λ\[\e[0m\] \[\e[0;34m\]\w\[\e[0;35m\]\$(__git_ps1 \" (%s)\" 2>/dev/null)\[\e[0m\] "'
            ;;
        *)
            ps1_def='PS1="\[\e[0;32m\]\u\[\e[0m\] \[\e[0;34m\]\w\[\e[0;35m\]\$(__git_ps1 \" ⎇ %s\" 2>/dev/null)\[\e[0m\] › "'
            ;;
    esac

    cat > "$_AC_THEME_FILE" <<EOF
# anycli-launcher Theme: $name
# Applied by anycli-launcher — do not edit manually

# Git prompt support
if [[ -f /usr/lib/git-core/git-sh-prompt ]]; then
    source /usr/lib/git-core/git-sh-prompt
elif [[ -f /usr/share/git-core/contrib/completion/git-prompt.sh ]]; then
    source /usr/share/git-core/contrib/completion/git-prompt.sh
fi
export GIT_PS1_SHOWDIRTYSTATE=1
export GIT_PS1_SHOWUNTRACKEDFILES=1

# Prompt
$ps1_def

# Directory colors
export LS_COLORS="di=1;34:ln=1;36:so=1;35:pi=33:ex=1;32:bd=1;33;40:cd=1;33;40:su=37;41:sg=30;43:tw=30;42:ow=34;42"
EOF

    printf "OK:%s" "$name"
}

_ac_config_theme() {
    local reset=$'\e[0m'
    local c_title=$'\e[1;36m'
    local c_dim=$'\e[2m'
    local c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m'
    local c_prompt=$'\e[1;32m'
    local c_val=$'\e[0;35m'

    while true; do
        _ac_clear
        printf "\n  %s──  Theme  ───────────────────────────%s\n\n" "$c_title" "$reset"

        # Detect platform
        local platform="linux"
        if _ac_is_windows_terminal; then
            platform="windows"
            printf "  %sPlatform: Windows Terminal%s\n" "$c_dim" "$reset"
            printf "  %sApplies: color scheme, font, opacity to all profiles%s\n\n" "$c_dim" "$reset"
        else
            printf "  %sPlatform: Linux terminal%s\n" "$c_dim" "$reset"
            printf "  %sApplies: PS1 prompt + dircolors (source from .bashrc)%s\n\n" "$c_dim" "$reset"
        fi

        # Current theme
        local current_theme="${_AC_THEME:-Default}"
        printf "  Current: %s%s%s\n\n" "$c_val" "$current_theme" "$reset"

        # List themes with color previews
        local ti=1
        for theme_entry in "${_AC_THEMES[@]}"; do
            local t_name t_bg t_fg t_red t_green t_yellow t_blue t_purple t_cyan
            t_name=$(_ac_theme_field "$theme_entry" 0)

            # Show theme name with color swatches (using the theme's actual colors)
            local marker=""
            [[ "$t_name" == "$current_theme" ]] && marker=" ✓"

            printf "   %s%d%s  %s%-16s%s " "$c_num" "$ti" "$reset" "$c_name" "$t_name" "$reset"
            if [[ "$t_name" == "Default" ]]; then
                printf "%s(Windows Terminal default)%s%s\n" "$c_dim" "$reset" "$marker"
            else
                # Color preview using ANSI approximation
                printf "\e[31m●\e[32m●\e[33m●\e[34m●\e[35m●\e[36m●%s%s\n" "$reset" "$marker"
            fi
            ((ti++))
        done

        printf "\n"
        if [[ "$platform" == "windows" ]]; then
            printf "   %sf%s  %sfont & opacity%s\n" "$c_num" "$reset" "$c_name" "$reset"
        fi
        printf "   %sb%s  %sback%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "\n  %s›%s " "$c_prompt" "$reset"

        local choice
        read -r choice

        [[ "$choice" == "b" || "$choice" == "B" ]] && return

        # Font & opacity (Windows only)
        if [[ "$choice" == "f" || "$choice" == "F" ]] && [[ "$platform" == "windows" ]]; then
            _ac_config_theme_font
            continue
        fi

        # Validate number selection
        if [[ ! "$choice" =~ ^[0-9]+$ ]] || (( choice < 1 || choice > ${#_AC_THEMES[@]} )); then
            continue
        fi

        local selected="${_AC_THEMES[$((choice - 1))]}"
        local selected_name
        selected_name=$(_ac_theme_field "$selected" 0)

        printf "\n  Applying %s...\n" "$selected_name"

        local result=""
        if [[ "$selected_name" == "Default" ]]; then
            if [[ "$platform" == "windows" ]]; then
                result=$(_ac_reset_theme_windows 2>&1)
            else
                # Linux: remove the theme file
                rm -f "$_AC_THEME_FILE"
                result="OK:Default"
            fi
        elif [[ "$platform" == "windows" ]]; then
            result=$(_ac_apply_theme_windows "$selected" 2>&1)
        else
            result=$(_ac_apply_theme_linux "$selected" 2>&1)
        fi

        if [[ "$result" == OK:* ]]; then
            _AC_THEME="$selected_name"
            _ac_write_config
            printf "  %s✓ Theme applied: %s%s\n" "$c_prompt" "$selected_name" "$reset"
            if [[ "$platform" == "linux" ]]; then
                printf "  %sRun: source %s%s\n" "$c_dim" "$_AC_THEME_FILE" "$reset"
                printf "  %s(Add to .bashrc: source %s)%s\n" "$c_dim" "$_AC_THEME_FILE" "$reset"
            else
                printf "  %sReopen terminal tabs to see changes.%s\n" "$c_dim" "$reset"
            fi
        else
            printf "  \e[31m✗ Failed: %s\e[0m\n" "$result"
        fi

        printf "\n  %sPress any key to continue...%s " "$c_dim" "$reset"
        _ac_pause
    done
}

_ac_config_theme_font() {
    local reset=$'\e[0m'
    local c_title=$'\e[1;36m'
    local c_dim=$'\e[2m'
    local c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m'
    local c_prompt=$'\e[1;32m'
    local c_val=$'\e[0;35m'

    _ac_clear
    printf "\n  %s──  Font & Opacity  ─────────────────%s\n\n" "$c_title" "$reset"

    # Recommended fonts
    local -a fonts=(
        "JetBrainsMono Nerd Font"
        "CaskaydiaCove Nerd Font"
        "FiraCode Nerd Font"
        "Hack Nerd Font"
        "MesloLGS Nerd Font"
    )

    printf "  %sRecommended fonts (Nerd Font = icons + powerline glyphs):%s\n\n" "$c_dim" "$reset"
    local fi=1
    for font in "${fonts[@]}"; do
        printf "   %s%d%s  %s%s%s\n" "$c_num" "$fi" "$reset" "$c_name" "$font" "$reset"
        ((fi++))
    done
    printf "   %s%d%s  %scustom (type a name)%s\n" "$c_num" "$fi" "$reset" "$c_name" "$reset"
    printf "\n   %ss%s  %sskip font%s\n" "$c_num" "$reset" "$c_name" "$reset"
    printf "\n  %s›%s " "$c_prompt" "$reset"

    local font_choice
    read -r font_choice

    local selected_font=""
    if [[ "$font_choice" == "s" || "$font_choice" == "S" ]]; then
        : # skip
    elif [[ "$font_choice" =~ ^[0-9]+$ ]] && (( font_choice >= 1 && font_choice <= ${#fonts[@]} )); then
        selected_font="${fonts[$((font_choice - 1))]}"
    elif [[ "$font_choice" =~ ^[0-9]+$ ]] && (( font_choice == fi )); then
        printf "  Font name: "
        read -r selected_font
    fi

    # Opacity
    printf "\n  %sOpacity (50-100, blank to skip):%s " "$c_dim" "$reset"
    local opacity_val
    read -r opacity_val

    # Apply via python
    if [[ -n "$selected_font" || -n "$opacity_val" ]]; then
        if ! _ac_wt_prereqs; then
            printf "  \e[31m✗ Failed: %s\e[0m\n" "$(_ac_wt_prereqs_fail_msg)"
            printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
            _ac_pause
            return
        fi
        local settings="$_AC_WT_SETTINGS" python_bin="$_AC_WT_PYTHON"

        "$python_bin" -c "$_AC_WT_PY_PRELUDE"'
settings_path = sys.argv[1]
font_name = sys.argv[2] if len(sys.argv) > 2 else ""
opacity = sys.argv[3] if len(sys.argv) > 3 else ""

settings = load_settings(settings_path)
defaults = profile_defaults(settings)

if font_name:
    font = defaults.get("font")
    if not isinstance(font, dict):
        font = {}
    font["face"] = font_name
    defaults["font"] = font

if opacity:
    try:
        value = int(opacity)
    except ValueError:
        value = None
    if value is not None and 50 <= value <= 100:
        defaults["opacity"] = value
        if value < 100:
            defaults["useAcrylic"] = True
        else:
            defaults.pop("useAcrylic", None)

save_settings(settings_path, settings)
print("OK")
' "$settings" "$selected_font" "$opacity_val"

        printf "  %s✓ Font & opacity applied%s\n" "$c_prompt" "$reset"
        if [[ -n "$selected_font" ]]; then
            printf "  %sFont: %s%s\n" "$c_dim" "$selected_font" "$reset"
        fi
        if [[ -n "$opacity_val" ]]; then
            printf "  %sOpacity: %s%%%s\n" "$c_dim" "$opacity_val" "$reset"
        fi
        printf "  %sReopen terminal tabs to see changes.%s\n" "$c_dim" "$reset"
    else
        printf "  %s— No changes%s\n" "$c_dim" "$reset"
    fi

    printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
    _ac_pause
}

# Shared project picker for the config editors.
# Sets _AC_PICKED_PROJECT and returns 0, or returns 1 when cancelled.
_ac_config_pick_project() {
    local label="${1:-Select project}"
    local c_num=$'\e[1;33m' c_name=$'\e[0;37m' c_prompt=$'\e[1;32m'
    local c_val=$'\e[0;35m' c_dim=$'\e[2m' reset=$'\e[0m'

    _AC_PICKED_PROJECT=""
    _ac_collect_projects

    local -a names=()
    local dir
    for dir in "${_AC_PROJECT_DIRS[@]+"${_AC_PROJECT_DIRS[@]}"}"; do
        names+=("$(basename "$dir")")
    done

    if (( ${#names[@]} == 0 )); then
        printf "\n    %s(no projects found in %s)%s\n" "$c_dim" "$_AC_PROJECTS_DIR" "$reset"
        printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
        _ac_pause
        return 1
    fi

    printf "\n  %s:\n" "$label"
    local i=1 n existing
    for n in "${names[@]}"; do
        existing="$(_ac_lookup_alias "$n")"
        if [[ -n "$existing" ]]; then
            printf "   %s%2d%s  %s%s%s  %s(%s)%s\n" "$c_num" "$i" "$reset" \
                "$c_name" "$n" "$reset" "$c_val" "$existing" "$reset"
        else
            printf "   %s%2d%s  %s%s%s\n" "$c_num" "$i" "$reset" "$c_name" "$n" "$reset"
        fi
        i=$(( i + 1 ))
    done
    printf "\n   %s b%s  %sback%s\n" "$c_num" "$reset" "$c_name" "$reset"
    printf "\n  %s›%s " "$c_prompt" "$reset"

    local sel
    read -r sel || return 1
    [[ "$sel" == "b" || "$sel" == "B" ]] && return 1
    [[ "$sel" =~ ^[0-9]+$ ]] || return 1
    (( sel >= 1 && sel <= ${#names[@]} )) || return 1

    _AC_PICKED_PROJECT="${names[$(( sel - 1 ))]}"
    _ac_register_project "$_AC_PICKED_PROJECT"
    return 0
}

# Edit a single appearance field via a prompt.
#
# Args: <varname> <prompt-text> [validate-regex] [error-label]
# Reads a value; a blank clears (unsets) the variable, a non-blank is assigned
# after optional regex validation. Writes the config on any accepted change.
# Returns 1 (without writing) when validation fails so the caller can re-loop.
_ac_edit_style_field() {
    local varname="$1" prompt_text="$2" regex="${3:-}" err_label="${4:-value}"
    local c_err=$'\e[31m' reset=$'\e[0m'
    printf '  %s' "$prompt_text"
    local val
    read -r val
    if [[ -z "$val" ]]; then
        unset "$varname"
    elif [[ -n "$regex" && ! "$val" =~ $regex ]]; then
        printf "  %s✗ Invalid %s: %s%s\n" "$c_err" "$err_label" "$val" "$reset"
        sleep 1
        return 1
    else
        _ac_assign "$varname" "$val"
    fi
    _ac_write_config
    return 0
}

# Edit the per-project appearance keys.
#
# color, title and prompt are documented in the README but were previously
# editable only by hand-editing the YAML file.
_ac_config_edit_project_style() {
    local c_dim=$'\e[2m' c_title=$'\e[1;36m' c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m' c_prompt=$'\e[1;32m' c_val=$'\e[0;35m'
    local reset=$'\e[0m'

    _ac_clear
    printf "\n  %sPer-Project Appearance%s\n" "$c_title" "$reset"
    printf "  %s(color or title opens the project in a new terminal tab)%s\n" "$c_dim" "$reset"

    # Current settings
    local varname
    local found=0
    for varname in $(_ac_vars_with_prefix _AC_PROJECT_COLOR_ | sort); do
        printf "    %s → %scolor %s%s\n" "$(_ac_project_name "${varname#_AC_PROJECT_COLOR_}")" \
            "$c_val" "$(_ac_indirect "$varname")" "$reset"
        found=$(( found + 1 ))
    done
    for varname in $(_ac_vars_with_prefix _AC_PROJECT_TITLE_ | sort); do
        printf "    %s → %stitle %s%s\n" "$(_ac_project_name "${varname#_AC_PROJECT_TITLE_}")" \
            "$c_val" "$(_ac_indirect "$varname")" "$reset"
        found=$(( found + 1 ))
    done
    for varname in $(_ac_vars_with_prefix _AC_PROJECT_PROMPT_ | sort); do
        printf "    %s → %sprompt %s%s\n" "$(_ac_project_name "${varname#_AC_PROJECT_PROMPT_}")" \
            "$c_val" "$(_ac_indirect "$varname")" "$reset"
        found=$(( found + 1 ))
    done
    (( found == 0 )) && printf "    %s(none configured)%s\n" "$c_dim" "$reset"

    _ac_config_pick_project "Select project" || return 0
    local proj="$_AC_PICKED_PROJECT"
    local safe
    safe="$(_ac_safe_name "$proj")"

    while true; do
        _ac_clear
        local cur_color cur_title cur_prompt
        cur_color="$(_ac_indirect "_AC_PROJECT_COLOR_${safe}")"
        cur_title="$(_ac_indirect "_AC_PROJECT_TITLE_${safe}")"
        cur_prompt="$(_ac_indirect "_AC_PROJECT_PROMPT_${safe}")"

        printf "\n  %sAppearance for: %s%s\n\n" "$c_title" "$proj" "$reset"
        printf "   %s 1%s  %scolor%s   %s%s%s\n"  "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${cur_color:-(none)}" "$reset"
        printf "   %s 2%s  %stitle%s   %s%s%s\n"  "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${cur_title:-(none)}" "$reset"
        printf "   %s 3%s  %sprompt%s  %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${cur_prompt:-(none)}" "$reset"
        printf "\n   %s c%s  %sclear all three%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "   %s d%s  %sdone%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "\n  %s›%s " "$c_prompt" "$reset"

        local choice
        read -r choice || return 0

        case "$choice" in
            1)
                _ac_edit_style_field "_AC_PROJECT_COLOR_${safe}" \
                    "Hex color (e.g. #00cc66, blank to clear): " \
                    '^#[0-9a-fA-F]{6}$' "6-digit hex color" || continue
                ;;
            2)
                _ac_edit_style_field "_AC_PROJECT_TITLE_${safe}" \
                    "Tab title (blank to clear): "
                ;;
            3)
                _ac_edit_style_field "_AC_PROJECT_PROMPT_${safe}" \
                    "Initial prompt (blank to clear): "
                ;;
            c|C)
                unset "_AC_PROJECT_COLOR_${safe}" "_AC_PROJECT_TITLE_${safe}" "_AC_PROJECT_PROMPT_${safe}"
                _ac_write_config
                ;;
            d|D) return 0 ;;
            *) continue ;;
        esac
    done
}

# --- Config Submenu ---
_ac_config_menu() {
    local _ac_cfg_mtime="" _ac_cfg_loaded=0 _ac_cfg_mtime_now
    while true; do
        # Skip the reload (full YAML re-parse + legacy migration scan) when
        # the config file hasn't changed since last read -- most submenu
        # actions already updated these variables directly before writing, so
        # re-reading from disk right after is redundant; this only re-reads
        # when something (a submenu write, or an external edit) actually
        # touched the file.
        _ac_cfg_mtime_now="$(_ac_file_mtime "$_AC_CONFIG_FILE")"
        if (( ! _ac_cfg_loaded )) || [[ "$_ac_cfg_mtime_now" != "$_ac_cfg_mtime" ]]; then
            _ac_load_config
            _ac_cfg_mtime="$_ac_cfg_mtime_now"
            _ac_cfg_loaded=1
        fi
        _ac_clear

        local reset=$'\e[0m'
        local c_title=$'\e[1;36m'
        local c_num=$'\e[1;33m'
        local c_name=$'\e[0;37m'
        local c_prompt=$'\e[1;32m'
        local c_val=$'\e[0;35m'
        local c_dim=$'\e[2m'

        printf '\n'
        printf "  %s──  anycli-launcher Config  ────────%s\n\n" "$c_title" "$reset"
        printf "  %s 1%s  %sProjects directory%s   %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "$_AC_PROJECTS_DIR" "$reset"
        printf "  %s 2%s  %sDefault flags%s        %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${_AC_CLI_FLAGS:-(none)}" "$reset"
        printf "  %s 3%s  %sPer-project flags%s\n" "$c_num" "$reset" "$c_name" "$reset"

        # Count per-project entries
        local count=0
        for v in $(_ac_vars_with_prefix _AC_CLI_FLAGS_); do ((count++)); done
        if (( count > 0 )); then
            printf "      %s(%d configured)%s\n" "$c_val" "$count" "$reset"
        fi

        printf "  %s 4%s  %sCLI tool%s             %s%s %s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "$_AC_CLI_TOOL" "$_AC_CLI_LAUNCH" "$reset"
        local _cfg_dir_resolved
        _cfg_dir_resolved="$(_ac_resolved_config_dir)"
        if [[ -n "$_AC_CLI_CONFIG_DIR" ]]; then
            printf "  %s f%s  %sConfig folder%s        %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "$_cfg_dir_resolved" "$reset"
        elif [[ -n "$_cfg_dir_resolved" ]]; then
            printf "  %s f%s  %sConfig folder%s        %s%s%s %s(default)%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "$_cfg_dir_resolved" "$reset" "$c_dim" "$reset"
        else
            printf "  %s f%s  %sConfig folder%s        %s(none known for %s)%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "$_AC_CLI_TOOL" "$reset"
        fi
        printf "  %s 5%s  %sAuto-update check%s    %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "$_AC_AUTO_UPDATE_CHECK" "$reset"
        printf "  %s 6%s  %sTheme%s                %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${_AC_THEME:-Default}" "$reset"
        printf "  %s 7%s  %sPer-project aliases%s\n" "$c_num" "$reset" "$c_name" "$reset"

        # Count alias entries
        local alias_count=0
        for v in $(_ac_vars_with_prefix _AC_PROJECT_ALIAS_); do ((alias_count++)); done
        if (( alias_count > 0 )); then
            printf "      %s(%d configured)%s\n" "$c_val" "$alias_count" "$reset"
        fi

        printf "  %s 8%s  %sPer-project appearance%s\n" "$c_num" "$reset" "$c_name" "$reset"
        local style_count=0
        for v in $(_ac_vars_with_prefix _AC_PROJECT_COLOR_) \
                 $(_ac_vars_with_prefix _AC_PROJECT_TITLE_) \
                 $(_ac_vars_with_prefix _AC_PROJECT_PROMPT_); do
            style_count=$(( style_count + 1 ))
        done
        if (( style_count > 0 )); then
            printf "      %s(%d configured)%s\n" "$c_val" "$style_count" "$reset"
        fi

        # Show sessions toggle — only meaningful when the CLI can list sessions.
        local _sess_supported=""
        [[ -n "$(_ac_cli_list_argv "$_AC_CLI_TOOL")" ]] && _sess_supported="1"
        if [[ -n "$_sess_supported" ]]; then
            printf "  %s 9%s  %sShow last session%s    %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${_AC_SHOW_SESSIONS:-false}" "$reset"
        fi

        printf "  %s s%s  %sSort order%s           %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${_AC_SORT_ORDER:-name}" "$reset"
        printf "  %s g%s  %sShow git status%s      %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${_AC_SHOW_GIT_STATUS:-false}" "$reset"

        printf "\n"
        printf "  %s b%s  %sback%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "\n  %s›%s " "$c_prompt" "$reset"

        local _ac_inp
        read -r _ac_inp || return

        case "$_ac_inp" in
            1) _ac_config_edit_projects_dir ;;
            2) _ac_config_edit_default_flags ;;
            3) _ac_config_edit_project_flags ;;
            4) _ac_config_edit_cli_tool ;;
            f|F) _ac_config_edit_config_dir ;;
            5) _ac_config_toggle_update_check ;;
            6) _ac_config_theme ;;
            7) _ac_config_edit_project_aliases ;;
            8) _ac_config_edit_project_style ;;
            9) [[ -n "$_sess_supported" ]] && _ac_config_toggle_sessions || echo "  Invalid selection" ;;
            s|S) _ac_config_toggle_sort_order ;;
            g|G) _ac_config_toggle_git_status ;;
            b|B) return ;;
            *) echo "  Invalid selection" ;;
        esac
    done
}

# Normalize a user-typed projects-directory path: expand a leading "~" to
# $HOME, and (Git Bash) convert a pasted native Windows path like
# C:\Users\me\code to its Unix form via cygpath -- bash cannot cd into a
# backslash path. Prints the normalized path; the caller decides what to do
# if it still doesn't exist. Shared by every prompt that accepts a
# projects-directory path (first-run wizard, `ac setup`, the config menu).
_ac_normalize_typed_path() {
    local p="${1:-}"
    case "$p" in "~"*) p="${HOME}${p#"~"}" ;; esac
    if [[ "$p" == *\\* || "$p" =~ ^[A-Za-z]:[/\\] ]] && command -v cygpath >/dev/null 2>&1; then
        p="$(cygpath -u "$p" 2>/dev/null || printf '%s' "$p")"
    fi
    printf '%s' "$p"
}

_ac_config_edit_config_dir() {
    local default_dir
    default_dir="$(_ac_cli_config_dir "$_AC_CLI_TOOL")"
    printf "\n  Current: %s\n" "$(_ac_resolved_config_dir)"
    if [[ -n "$_AC_CLI_CONFIG_DIR" && -n "$default_dir" ]]; then
        printf "  (%s's default is %s)\n" "$_AC_CLI_TOOL" "$default_dir"
    fi
    printf "  New value (blank to keep%s): " "$([[ -n "$_AC_CLI_CONFIG_DIR" ]] && printf ', "d" to reset to default')"
    local val
    read -r val
    if [[ "$val" == "d" || "$val" == "D" ]]; then
        _AC_CLI_CONFIG_DIR=""
        _ac_write_config
        printf "  ✓ Reset to default\n"
    elif [[ -n "$val" ]]; then
        val="$(_ac_normalize_typed_path "$val")"
        if [[ ! -d "$val" ]]; then
            printf "  Warning: directory does not exist: %s\n" "$val"
            printf "  Save anyway? (y/n): "
            local confirm
            read -r confirm
            if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
                printf "  ✗ Not saved\n"
                return
            fi
        fi
        _AC_CLI_CONFIG_DIR="$val"
        _ac_write_config
        printf "  ✓ Saved\n"
    else
        printf "  — No changes\n"
    fi
}

_ac_config_edit_projects_dir() {
    printf "\n  Current: %s\n" "$_AC_PROJECTS_DIR"
    printf "  New value (blank to keep): "
    local val
    read -r val
    if [[ -n "$val" ]]; then
        val="$(_ac_normalize_typed_path "$val")"
        if [[ ! -d "$val" ]]; then
            printf "  Warning: directory does not exist: %s\n" "$val"
            printf "  Save anyway? (y/n): "
            local confirm
            read -r confirm
            if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
                printf "  ✗ Not saved\n"
                return
            fi
        fi
        _AC_PROJECTS_DIR="$val"
        _ac_write_config
        printf "  ✓ Saved\n"
    else
        printf "  — No changes\n"
    fi
}

# Resolve a chosen entry from _ac_get_flag_opts's output into the flag text
# to add, prompting for a value when the entry needs one. Sets _AC_NEW_FLAG
# (empty when nothing should be added -- "none (use default)" was chosen, or
# "custom"/a parameterized flag was chosen but nothing was typed). Shared by
# _ac_config_edit_default_flags and _ac_config_edit_project_flags, whose
# flag-picker loops were otherwise identical.
_ac_resolve_chosen_flag() {
    local chosen="$1"
    _AC_NEW_FLAG=""
    if [[ "$chosen" == "custom" ]]; then
        printf "  Enter flag: "
        read -r _AC_NEW_FLAG
    elif [[ "$chosen" == "none (use default)" ]]; then
        return 0
    elif [[ "$chosen" == *"=<"* ]]; then
        local flag_name="${chosen%%=<*}"
        local param_hint="${chosen#*=<}"
        param_hint="${param_hint%>}"
        printf "  Enter value for %s (%s): " "$flag_name" "$param_hint"
        local param_val
        read -r param_val
        [[ -n "$param_val" ]] && _AC_NEW_FLAG="$flag_name $param_val"
    else
        _AC_NEW_FLAG="$chosen"
    fi
    return 0
}

_ac_config_edit_default_flags() {
    local c_dim=$'\e[2m'
    local c_title=$'\e[1;36m'
    local c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m'
    local c_prompt=$'\e[1;32m'
    local c_val=$'\e[0;35m'
    local reset=$'\e[0m'

    local -a flag_opts=()
    while IFS= read -r fline; do
        [[ -n "$fline" ]] && flag_opts+=("$fline")
    done < <(_ac_get_flag_opts)

    while true; do
        _ac_clear
        printf "\n  %sDefault Flags%s\n" "$c_title" "$reset"
        printf "  %s(these apply to ALL projects)%s\n" "$c_dim" "$reset"
        printf "  Current: %s%s%s\n\n" "$c_val" "${_AC_CLI_FLAGS:-(none)}" "$reset"

        printf "  Add a flag:\n"
        local ci=1
        for ch in "${flag_opts[@]}"; do
            printf "   %s%2d%s  %s%s%s\n" "$c_num" "$ci" "$reset" "$c_name" "$ch" "$reset"
            ((ci++))
        done

        printf "\n   %s c%s  %sclear all%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "   %s d%s  %sdone%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "\n  %s›%s " "$c_prompt" "$reset"

        local cnum
        read -r cnum || return

        [[ "$cnum" == "d" || "$cnum" == "D" ]] && return
        if [[ "$cnum" == "c" || "$cnum" == "C" ]]; then
            _AC_CLI_FLAGS=""
            _ac_write_config
            continue
        fi

        if [[ ! "$cnum" =~ ^[0-9]+$ ]] || (( cnum < 1 || cnum > ${#flag_opts[@]} )); then
            continue
        fi

        local chosen="${flag_opts[$((cnum - 1))]}"
        _ac_resolve_chosen_flag "$chosen"
        local new_flag="$_AC_NEW_FLAG"

        if [[ -n "$new_flag" ]]; then
            _AC_CLI_FLAGS="${_AC_CLI_FLAGS:+$_AC_CLI_FLAGS }$new_flag"
            _ac_write_config
        fi
    done
}

_ac_config_edit_project_flags() {
    local c_dim=$'\e[2m'
    local c_title=$'\e[1;36m'
    local c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m'
    local c_prompt=$'\e[1;32m'
    local c_val=$'\e[0;35m'
    local reset=$'\e[0m'

    _ac_clear
    printf "\n  %sPer-Project Flags%s\n" "$c_title" "$reset"
    printf "  %s(additive — projects inherit default_flags automatically)%s\n\n" "$c_dim" "$reset"

    # Show current config
    local varname
    local found=0
    for varname in $(_ac_vars_with_prefix _AC_CLI_FLAGS_ | sort); do
        local p="${varname#_AC_CLI_FLAGS_}"
        local v="$(_ac_indirect "$varname")"
        # Show the project name as the user wrote it, not the sanitized suffix
        printf "    %s → %s%s%s\n" "$(_ac_project_name "$p")" "$c_val" "${v:-(inherit only)}" "$reset"
        ((found++))
    done
    (( found == 0 )) && printf "    %s(none configured)%s\n" "$c_dim" "$reset"

    # List available projects
    _ac_config_pick_project "Select project" || return
    local proj="$_AC_PICKED_PROJECT"
    local safe
    safe="$(_ac_safe_name "$proj")"
    local varname="_AC_CLI_FLAGS_${safe}"
    local current=""
    _ac_indirect_set "$varname" && current="$(_ac_indirect "$varname")"

    # Build flag options
    local -a flag_opts=()
    while IFS= read -r fline; do
        [[ -n "$fline" ]] && flag_opts+=("$fline")
    done < <(_ac_get_flag_opts)

    # Accumulating loop
    while true; do
        _ac_clear
        # Re-read current value
        current=""
        _ac_indirect_set "$varname" && current="$(_ac_indirect "$varname")"

        printf "\n  %sFlags for: %s%s\n" "$c_title" "$proj" "$reset"
        printf "  Current: %s%s%s\n\n" "$c_val" "${current:-(inherits defaults only)}" "$reset"
        printf "  %sResolved: %s%s\n\n" "$c_dim" "$(_ac_get_flags "$proj")" "$reset"

        printf "  Add a flag:\n"
        local ci=1
        for ch in "${flag_opts[@]}"; do
            printf "   %s%2d%s  %s%s%s\n" "$c_num" "$ci" "$reset" "$c_name" "$ch" "$reset"
            ((ci++))
        done

        printf "\n   %s n%s  %snone (suppress inheritance)%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "   %s c%s  %sclear (reset to default)%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "   %s d%s  %sdone%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "\n  %s›%s " "$c_prompt" "$reset"

        local cnum
        read -r cnum || return

        [[ "$cnum" == "d" || "$cnum" == "D" ]] && return
        if [[ "$cnum" == "n" || "$cnum" == "N" ]]; then
            _ac_assign "$varname" "none"
            _ac_write_config
            return
        fi
        if [[ "$cnum" == "c" || "$cnum" == "C" ]]; then
            unset "$varname"
            _ac_write_config
            continue
        fi

        if [[ ! "$cnum" =~ ^[0-9]+$ ]] || (( cnum < 1 || cnum > ${#flag_opts[@]} )); then
            continue
        fi

        local chosen="${flag_opts[$((cnum - 1))]}"
        _ac_resolve_chosen_flag "$chosen"
        local new_flag="$_AC_NEW_FLAG"

        if [[ -n "$new_flag" ]]; then
            current=""
            _ac_indirect_set "$varname" && current="$(_ac_indirect "$varname")"
            if [[ "$current" == "none" ]]; then
                _ac_assign "$varname" "$new_flag"
            else
                _ac_assign "$varname" "${current:+$current }$new_flag"
            fi
            _ac_write_config
        fi
    done
}


_ac_config_edit_project_aliases() {
    local c_dim=$'\e[2m'
    local c_title=$'\e[1;36m'
    local c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m'
    local c_prompt=$'\e[1;32m'
    local c_val=$'\e[0;35m'
    local reset=$'\e[0m'

    _ac_clear
    printf "\n  %sPer-Project Aliases%s\n" "$c_title" "$reset"
    printf "  %s(display name shown in the menu instead of directory name)%s\n\n" "$c_dim" "$reset"

    # Show current aliases
    local varname
    local found=0
    for varname in $(_ac_vars_with_prefix _AC_PROJECT_ALIAS_ | sort); do
        local p="${varname#_AC_PROJECT_ALIAS_}"
        local v="$(_ac_indirect "$varname")"
        printf "    %s → %s%s%s\n" "$(_ac_project_name "$p")" "$c_val" "$v" "$reset"
        ((found++))
    done
    (( found == 0 )) && printf "    %s(none configured)%s\n" "$c_dim" "$reset"

    # List available projects
    _ac_config_pick_project "Select project" || return
    local proj="$_AC_PICKED_PROJECT"
    local safe
    safe="$(_ac_safe_name "$proj")"
    local varname="_AC_PROJECT_ALIAS_${safe}"
    local current="$(_ac_indirect "$varname")"

    printf "\n  %sAlias for: %s%s\n" "$c_title" "$proj" "$reset"
    printf "  Current: %s%s%s\n" "$c_val" "${current:-(none)}" "$reset"
    printf "\n  New alias (blank to clear): "

    local val
    read -r val

    if [[ -n "$val" ]]; then
        _ac_assign "$varname" "$val"
    else
        unset "$varname"
    fi
    _ac_write_config
}

# Validate a proposed new project directory name.
#
# Note this deliberately does NOT use _ac_safe_name: that function is a
# config-key encoder (e.g. "my-app" -> "my_2dapp") used only when reading or
# writing the per-project variables above. The directory created on disk must
# stay human-readable like every other project directory, so this applies a
# separate, lighter character-allowlist check instead.
#
# Prints one human-readable error message on stdout and returns 1 when the
# name is unusable; prints nothing and returns 0 when it is fine.
_ac_validate_new_project_name() {
    local name="${1:-}"
    if [[ -z "$name" ]]; then
        printf '%s' "Name cannot be empty."
        return 1
    fi
    if [[ "$name" == *"/"* || "$name" == *"\\"* ]]; then
        printf '%s' "Name cannot contain a path separator (/ or \\) — projects are created directly inside <root>."
        return 1
    fi
    if [[ ! "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
        printf '%s' "Use letters, digits, dot, dash or underscore; must start with a letter or digit."
        return 1
    fi
    if [[ -e "$_AC_PROJECTS_DIR/$name" ]]; then
        printf "A project named '%s' already exists." "$name"
        return 1
    fi
    return 0
}

# Inline "set alias/color/title/prompt" submenu for a brand-new project.
# Same shape as _ac_config_edit_project_style's inner loop, but scoped to one
# already-known project (no _ac_config_pick_project step) and with an extra
# alias field.
_ac_new_project_setup() {
    local proj="$1" safe
    safe="$(_ac_safe_name "$proj")"

    local c_dim=$'\e[2m' c_title=$'\e[1;36m' c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m' c_prompt=$'\e[1;32m' c_val=$'\e[0;35m'
    local reset=$'\e[0m'

    while true; do
        _ac_clear
        local cur_alias cur_color cur_title cur_prompt
        cur_alias="$(_ac_indirect "_AC_PROJECT_ALIAS_${safe}")"
        cur_color="$(_ac_indirect "_AC_PROJECT_COLOR_${safe}")"
        cur_title="$(_ac_indirect "_AC_PROJECT_TITLE_${safe}")"
        cur_prompt="$(_ac_indirect "_AC_PROJECT_PROMPT_${safe}")"

        printf "\n  %sSet up: %s%s\n" "$c_title" "$proj" "$reset"
        printf "  %s(all optional — press d when you're done)%s\n\n" "$c_dim" "$reset"
        printf "   %s1%s  %salias%s   %s%s%s\n"  "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${cur_alias:-(none)}" "$reset"
        printf "   %s2%s  %scolor%s   %s%s%s\n"  "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${cur_color:-(none)}" "$reset"
        printf "   %s3%s  %stitle%s   %s%s%s\n"  "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${cur_title:-(none)}" "$reset"
        printf "   %s4%s  %sprompt%s  %s%s%s\n" "$c_num" "$reset" "$c_name" "$reset" "$c_val" "${cur_prompt:-(none)}" "$reset"
        printf "\n   %s d%s  %sdone%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "\n  %s›%s " "$c_prompt" "$reset"

        local choice
        read -r choice || return 0

        case "$choice" in
            1)
                _ac_edit_style_field "_AC_PROJECT_ALIAS_${safe}" \
                    "Display name (blank to clear): "
                ;;
            2)
                _ac_edit_style_field "_AC_PROJECT_COLOR_${safe}" \
                    "Hex color (e.g. #00cc66, blank to clear): " \
                    '^#[0-9a-fA-F]{6}$' "6-digit hex color" || continue
                ;;
            3)
                _ac_edit_style_field "_AC_PROJECT_TITLE_${safe}" \
                    "Tab title (blank to clear): "
                ;;
            4)
                _ac_edit_style_field "_AC_PROJECT_PROMPT_${safe}" \
                    "Initial prompt (blank to clear): "
                ;;
            d|D) return 0 ;;
            *) continue ;;
        esac
    done
}

# Top-level "create a new project" flow: prompt for a name -> validate (retry
# loop) -> mkdir -> git init -> register -> optionally offer the inline
# alias/color/title/prompt setup screen -> return.
_ac_new_project() {
    local c_title=$'\e[1;36m' c_dim=$'\e[2m' c_name=$'\e[0;37m'
    local c_prompt=$'\e[1;32m' c_err=$'\e[31m' c_ok=$'\e[32m'
    local reset=$'\e[0m'

    local name="" err_msg=""
    while true; do
        _ac_clear
        printf "\n  %sNew Project%s\n" "$c_title" "$reset"
        printf "  %sCreated in: %s%s\n\n" "$c_dim" "$(_ac_tildify "$_AC_PROJECTS_DIR")" "$reset"
        if [[ -n "$err_msg" ]]; then
            printf "  %s✗ %s%s\n\n" "$c_err" "$err_msg" "$reset"
        fi
        printf "  %sProject name (blank to cancel): %s" "$c_name" "$reset"

        read -r name || return 0
        [[ -z "$name" ]] && return 0

        if err_msg="$(_ac_validate_new_project_name "$name")"; then
            break
        fi
    done

    local dir="${_AC_PROJECTS_DIR%/}/$name"
    if ! mkdir -- "$dir" 2>/dev/null; then
        printf "\n  %s✗ Could not create %s%s\n" "$c_err" "$dir" "$reset"
        printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
        _ac_pause
        return 0
    fi

    local git_bin git_note
    git_bin="$(_ac_resolve_bin git)"
    if [[ -z "$git_bin" ]]; then
        git_note="git not found on PATH — folder created without a repo"
    elif "$git_bin" -C "$dir" init -q >/dev/null 2>&1; then
        git_note="git repository initialized"
    else
        git_note="git init failed — folder created without a repo"
    fi

    _ac_register_project "$name"

    printf "\n  %s✓ Created %s%s\n" "$c_ok" "$(_ac_tildify "$dir")" "$reset"
    printf "  %s%s%s\n" "$c_dim" "$git_note" "$reset"
    printf "\n  %sSet alias / color / title / prompt now? (Y/n): %s" "$c_prompt" "$reset"

    local ans
    read -r ans || ans="n"
    if [[ -z "$ans" || "$ans" == "y" || "$ans" == "Y" ]]; then
        _ac_new_project_setup "$name"
    fi

    return 0
}
# --- Changelog Viewer ---
_ac_show_changelog() {
    _ac_clear
    local changelog="$_AC_SCRIPT_DIR/CHANGELOG.md"
    local reset=$'\e[0m'
    local c_title=$'\e[1;36m'
    local c_dim=$'\e[2m'
    local c_prompt=$'\e[1;32m'

    printf "\n  %s╭─────────────────────────────────────╮%s\n" "$c_title" "$reset"
    local _ac_hdr="Changelog"
    local _ac_ver="v$_AC_VERSION"
    local _ac_pad=$(( 37 - 2 - ${#_ac_hdr} - 2 - ${#_ac_ver} ))
    printf "  %s│%s  %s%*s%s%s%s  %s│%s\n" "$c_title" "$reset" "$_ac_hdr" "$_ac_pad" "" "$c_dim" "$_ac_ver" "$reset" "$c_title" "$reset"
    printf "  %s╰─────────────────────────────────────╯%s\n\n" "$c_title" "$reset"

    if [[ -f "$changelog" ]]; then
        # Show changelog with basic formatting
        while IFS= read -r line; do
            case "$line" in
                "## "*)  printf "  %s%s%s\n" "$c_title" "${line#\# }" "$reset" ;;
                "- "*)   printf "  %s\n" "$line" ;;
                "")      printf "\n" ;;
                *)       printf "  %s%s%s\n" "$c_dim" "$line" "$reset" ;;
            esac
        done < "$changelog"
    else
        printf "  %sNo changelog found.%s\n" "$c_dim" "$reset"
    fi

    printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
    _ac_pause
}

# Help screen for the menu's less-obvious keys (session resume/picker triggers).
# Reached via "?" from the main menu; only lists triggers the configured CLI
# actually supports, so the menu footer stays uncluttered for everyone else.
_ac_show_key_help() {
    _ac_clear
    local reset=$'\e[0m'
    local c_title=$'\e[1;36m'
    local c_num=$'\e[1;33m'
    local c_dim=$'\e[2m'

    printf "\n  %sMenu keys%s\n\n" "$c_title" "$reset"
    printf "   %s<N>%s   launch project number N\n" "$c_num" "$reset"

    if [[ -n "$(_ac_cli_resume_flag "$_AC_CLI_TOOL")" ]]; then
        printf "   %sr<N>%s  resume project N's most recent session\n" "$c_num" "$reset"
    fi
    if [[ -n "$(_ac_cli_picker_flag "$_AC_CLI_TOOL")" ]]; then
        printf "   %sp<N>%s  pick a session to resume for project N\n" "$c_num" "$reset"
    fi

    printf "\n   %stype%s  filter the list by name    %s/%s  clear the filter\n" "$c_num" "$reset" "$c_num" "$reset"
    printf "   %s c%s   config    %s n%s  new project    %s v%s  changelog    %s q%s  quit\n" "$c_num" "$reset" "$c_num" "$reset" "$c_num" "$reset" "$c_num" "$reset"

    if [[ -n "$(_ac_cli_resume_flag "$_AC_CLI_TOOL")" ]]; then
        printf "\n  %sResume continues the CLI's last conversation in that project;%s\n" "$c_dim" "$reset"
        printf "  %sthe per-project startup prompt is skipped so it isn't re-seeded.%s\n" "$c_dim" "$reset"
    fi

    printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
    _ac_pause
}

# --- Platform Helpers ---

# Single source of truth for "what platform is this". Echoes exactly one of:
#   wsl      Windows Subsystem for Linux (Windows Terminal reached via wsl.exe)
#   gitbash  Git Bash / MSYS2 / MSYS / UCRT on native Windows
#   macos    macOS
#   linux    everything else (native Linux, or an unrecognized platform)
#
# Real detection (the /proc/version, $MSYSTEM, and `uname -s` probes) is
# cached in _AC_PLATFORM after the first call within a process, since it
# can't change mid-process. Every platform-specific branch in the file
# (new-tab launch mechanism, Windows Terminal settings.json discovery, theme
# application) should consult this instead of re-deriving platform directly
# -- that duplication previously meant each call site could disagree, and
# tests had to mock three different signals (plus shadow `uname` as a
# function) to force one code path.
#
# _AC_PLATFORM_OVERRIDE is the single test seam: set it to force a platform
# without touching /proc/version, $MSYSTEM, or `uname`. It is checked BEFORE
# the cache on every call (and is not itself cached into _AC_PLATFORM), so it
# always wins even after real detection has already run and cached a result.
_ac_platform() {
    if [[ -n "${_AC_PLATFORM_OVERRIDE:-}" ]]; then
        printf '%s' "$_AC_PLATFORM_OVERRIDE"
        return 0
    fi
    if [[ -n "${_AC_PLATFORM:-}" ]]; then
        printf '%s' "$_AC_PLATFORM"
        return 0
    fi
    if [[ -f /proc/version ]] && grep -qi microsoft /proc/version 2>/dev/null; then
        _AC_PLATFORM="wsl"
    elif [[ "${MSYSTEM:-}" =~ ^(MINGW|MSYS|UCRT) || "$(uname -s 2>/dev/null)" == MINGW* ]]; then
        _AC_PLATFORM="gitbash"
    elif [[ "$(uname -s 2>/dev/null)" == "Darwin" ]]; then
        _AC_PLATFORM="macos"
    else
        _AC_PLATFORM="linux"
    fi
    printf '%s' "$_AC_PLATFORM"
}

_ac_kiro_cli_data_dir() {
    # Resolve the kiro-cli data directory for the current platform.
    # Returns the path to stdout. Returns 1 if no valid path found.
    #   Linux/WSL:          ~/.local/share/kiro-cli
    #   Windows (Git Bash): $LOCALAPPDATA/kiro-cli or ~/AppData/Local/kiro-cli
    #   macOS:              ~/Library/Application Support/kiro-cli
    if [[ -d "${HOME}/.local/share/kiro-cli" ]]; then
        echo "${HOME}/.local/share/kiro-cli"
    elif [[ -n "${LOCALAPPDATA:-}" && -d "${LOCALAPPDATA}/kiro-cli" ]]; then
        echo "${LOCALAPPDATA}/kiro-cli"
    elif [[ -d "${HOME}/AppData/Local/kiro-cli" ]]; then
        echo "${HOME}/AppData/Local/kiro-cli"
    elif [[ -d "${HOME}/Library/Application Support/kiro-cli" ]]; then
        echo "${HOME}/Library/Application Support/kiro-cli"
    else
        return 1
    fi
}

_ac_timeout() {
    # Portable timeout wrapper. Uses `timeout` (Linux/Git Bash) or `gtimeout`
    # (macOS with coreutils). Falls back to direct execution without timeout
    # if neither is available (macOS without coreutils).
    local seconds="$1"
    shift
    if command -v timeout &>/dev/null; then
        timeout "$seconds" "$@"
    elif command -v gtimeout &>/dev/null; then
        gtimeout "$seconds" "$@"
    else
        # No timeout available — run directly (curl has --max-time as fallback)
        "$@"
    fi
}

# --- Update Detection ---
# Idempotent, like _AC_CONFIG_FILE above.
_AC_UPDATE_CACHE="${_AC_UPDATE_CACHE:-${HOME}/.config/anycli-launcher/.update-check}"
_AC_UPDATE_CHECK_INTERVAL="${_AC_UPDATE_CHECK_INTERVAL:-14400}"  # 4 hours in seconds
_AC_CLI_RELEASE_INDEX="https://desktop-release.q.us-east-1.amazonaws.com/index.json"

# Tracked apps are user-defined in config.yaml under "tracked_apps". The list
# used to be hardcoded to the author's own repositories, which meant every
# other user paid for three background git fetches against paths that do not
# exist on their machine.
#
# Each app is described declaratively — no shell command is stored, because
# config is data and running it through eval would make the config file an
# arbitrary code execution vector.
#
#   tracked_apps:
#     myapp:
#       path: ~/projects/myapp
#       version_file: pyproject.toml
#       version_key: "^version"          # optional line filter
#       version_pattern: "[0-9]+\.[0-9]+\.[0-9]+"   # optional
_AC_TRACKED_APP_NAMES=()
_AC_DEFAULT_VERSION_PATTERN='[0-9]+\.[0-9]+\.[0-9]+'

# Read one configured field for a tracked app.
_ac_app_field() {
    local name="${1:-}" field="${2:-}" safe
    safe="$(_ac_safe_name "$name")"
    case "$field" in
        path)            _ac_indirect "_AC_APP_PATH_${safe}" ;;
        version_file)    _ac_indirect "_AC_APP_VERFILE_${safe}" ;;
        version_key)     _ac_indirect "_AC_APP_VERKEY_${safe}" ;;
        version_pattern) _ac_indirect "_AC_APP_VERPAT_${safe}" ;;
    esac
    return 0
}

# Extract the current version of a tracked app by reading a file.
#
# Previously this ran a stored shell snippet through eval. It now greps a
# configured file, so nothing from the config file is ever executed.
_ac_get_app_version() {
    local app_path="${1:-}" version_file="${2:-}" version_key="${3:-}" pattern="${4:-}"
    [[ -n "$app_path" && -d "$app_path" ]] || return 0
    [[ -n "$version_file" ]] || return 0
    local f="$app_path/$version_file"
    [[ -f "$f" ]] || return 0
    [[ -n "$pattern" ]] || pattern="$_AC_DEFAULT_VERSION_PATTERN"

    if [[ -n "$version_key" ]]; then
        grep -m1 -E "$version_key" "$f" 2>/dev/null | grep -oE "$pattern" 2>/dev/null | head -1
    else
        grep -oE "$pattern" "$f" 2>/dev/null | head -1
    fi
    return 0
}

# Convenience wrapper: resolve every field for a named app and read its version.
_ac_app_version() {
    local name="${1:-}"
    _ac_get_app_version \
        "$(_ac_app_field "$name" path)" \
        "$(_ac_app_field "$name" version_file)" \
        "$(_ac_app_field "$name" version_key)" \
        "$(_ac_app_field "$name" version_pattern)"
    return 0
}

_ac_semver_compare() {
    # Compare two semver strings. Returns:
    #   0 if v1 == v2
    #   1 if v1 > v2
    #   2 if v1 < v2
    # Works on both Linux and Windows (Git Bash)
    local v1="$1" v2="$2"
    [[ "$v1" == "$v2" ]] && return 0

    local IFS='.'
    local -a parts1=($v1) parts2=($v2)

    local i
    for i in 0 1 2; do
        local n1="${parts1[$i]:-0}" n2="${parts2[$i]:-0}"
        if (( n1 > n2 )); then
            return 1
        elif (( n1 < n2 )); then
            return 2
        fi
    done
    return 0
}

_ac_check_cli_update_via_index() {
    # Check for kiro-cli updates by fetching the remote release index. Only
    # called when _AC_CLI_TOOL is "kiro-cli" (see _ac_update_probe) -- this
    # index and version format are specific to kiro-cli, not a generic
    # mechanism other CLIs share.
    # More reliable than running `kiro-cli update` which requires a TTY.
    # Sets cli_update (0/1) and cli_version in the calling scope.
    # Works on both Linux and Windows (Git Bash) — requires curl.
    local current_ver
    current_ver=$("$_AC_CLI_TOOL" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+') || true
    if [[ -z "$current_ver" ]]; then
        return 1
    fi

    local index_json latest_ver
    index_json=$(_ac_timeout 10 curl -sf "$_AC_CLI_RELEASE_INDEX" 2>/dev/null) || true
    if [[ -z "$index_json" ]]; then
        return 1
    fi

    # Extract the first version from the index (latest release).
    # The JSON format is: "versions": [{"version": "X.Y.Z", ...}, ...]
    # Use portable grep (no -oP) for Windows Git Bash compatibility.
    latest_ver=$(grep -oE '"version"[[:space:]]*:[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"' <<< "$index_json" | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+') || true
    if [[ -z "$latest_ver" ]]; then
        return 1
    fi

    # Compare: if latest is newer than current, update is available
    _ac_semver_compare "$latest_ver" "$current_ver"
    local cmp_result=$?
    if [[ $cmp_result -eq 1 ]]; then
        # latest > current — update available
        cli_update=1
        cli_version="$latest_ver"
    fi
}

_ac_check_update_cache() {
    # Read cached update state. Sets:
    #   _AC_CLI_UPDATE_AVAILABLE (0/1)
    #   _AC_CLI_LATEST_VERSION (version string or "")
    #   _AC_LAUNCHER_UPDATE_AVAILABLE (0/1)
    #   _AC_LAUNCHER_COMMITS_BEHIND (number or "")
    #   _AC_APPS_UPDATE_AVAILABLE (0/1)
    #   _AC_APPS_UPDATE_LIST (space-separated app names with updates)
    _AC_CLI_UPDATE_AVAILABLE=0
    _AC_CLI_LATEST_VERSION=""
    _AC_LAUNCHER_UPDATE_AVAILABLE=0
    _AC_LAUNCHER_COMMITS_BEHIND=""
    _AC_APPS_UPDATE_AVAILABLE=0
    _AC_APPS_UPDATE_LIST=""

    [[ -f "$_AC_UPDATE_CACHE" ]] || return 1

    local key val
    while IFS='=' read -r key val || [[ -n "$key" ]]; do
        case "$key" in
            CLI_UPDATE_AVAILABLE)      _AC_CLI_UPDATE_AVAILABLE="$val" ;;
            CLI_LATEST_VERSION)        _AC_CLI_LATEST_VERSION="$val" ;;
            LAUNCHER_UPDATE_AVAILABLE) _AC_LAUNCHER_UPDATE_AVAILABLE="$val" ;;
            LAUNCHER_COMMITS_BEHIND)   _AC_LAUNCHER_COMMITS_BEHIND="$val" ;;
            APPS_UPDATE_AVAILABLE)     _AC_APPS_UPDATE_AVAILABLE="$val" ;;
            APPS_UPDATE_LIST)          _AC_APPS_UPDATE_LIST="$val" ;;
        esac
    done < "$_AC_UPDATE_CACHE"
}

# Perform the actual probes and persist the result to the cache file.
# Runs in the background by default so the menu never waits on the network.
_ac_update_probe() {
    local cli_update=0 cli_version="" launcher_update=0 launcher_behind=""
    local apps_update=0 apps_list=""

    # 1. The configured CLI's own updates, via the remote release index.
    # This mechanism only exists for kiro-cli -- there's no equivalent public
    # index for claude/aider/goose/codex/amp/etc. So this checks whether
    # kiro-cli is what the user actually configured (not merely whether it
    # happens to also be installed alongside whatever they really use).
    if [[ "$_AC_CLI_TOOL" == "kiro-cli" ]] && command -v kiro-cli &>/dev/null; then
        _ac_check_cli_update_via_index || true
    fi

    # 2. anycli-launcher, via git
    if [[ -d "$_AC_SCRIPT_DIR/.git" ]]; then
        _ac_timeout 10 git -C "$_AC_SCRIPT_DIR" fetch origin main --quiet 2>/dev/null || true
        local behind
        behind=$(git -C "$_AC_SCRIPT_DIR" rev-list --count HEAD..origin/main 2>/dev/null) || true
        if [[ "$behind" =~ ^[0-9]+$ ]] && (( behind > 0 )); then
            launcher_update=1
            launcher_behind="$behind"
        fi
    fi

    # 3. User-configured tracked apps
    local app_name app_path
    for app_name in "${_AC_TRACKED_APP_NAMES[@]+"${_AC_TRACKED_APP_NAMES[@]}"}"; do
        app_path="$(_ac_app_field "$app_name" path)"
        [[ -n "$app_path" && -d "$app_path/.git" ]] || continue
        local default_branch app_behind
        default_branch=$(git -C "$app_path" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@') || true
        [[ -z "$default_branch" ]] && default_branch="main"
        _ac_timeout 10 git -C "$app_path" fetch origin "$default_branch" --quiet 2>/dev/null || true
        app_behind=$(git -C "$app_path" rev-list --count HEAD..origin/"$default_branch" 2>/dev/null) || true
        if [[ "$app_behind" =~ ^[0-9]+$ ]] && (( app_behind > 0 )); then
            apps_update=1
            apps_list="${apps_list:+$apps_list }$app_name"
        fi
    done

    # Write the cache atomically so a reader never sees a half-written file
    local cache_dir tmp_cache
    cache_dir="$(dirname "$_AC_UPDATE_CACHE")"
    mkdir -p "$cache_dir"
    tmp_cache="$(mktemp "${cache_dir}/.update-check.XXXXXX" 2>/dev/null)" || return 1
    cat > "$tmp_cache" <<EOF
LAST_CHECK=$(date +%s)
CLI_UPDATE_AVAILABLE=$cli_update
CLI_LATEST_VERSION=$cli_version
LAUNCHER_UPDATE_AVAILABLE=$launcher_update
LAUNCHER_COMMITS_BEHIND=${launcher_behind:-0}
APPS_UPDATE_AVAILABLE=$apps_update
APPS_UPDATE_LIST=$apps_list
EOF
    mv "$tmp_cache" "$_AC_UPDATE_CACHE" 2>/dev/null || rm -f "$tmp_cache"
    return 0
}

_ac_update_check() {
    # Time-gated update check. Skipped if auto_update_check config is off.
    #
    # The menu is rendered from the cache only; probes run in a background
    # subshell. Previously the kiro-cli index fetch and the launcher git fetch
    # ran in the foreground, so every four hours the first ac invocation stalled
    # on the network before drawing anything.
    [[ "${_AC_AUTO_UPDATE_CHECK:-true}" == "true" ]] || return 0

    # Show whatever the last completed probe found
    _ac_check_update_cache || true

    local last_check=0 key val now
    if [[ -f "$_AC_UPDATE_CACHE" ]]; then
        while IFS='=' read -r key val || [[ -n "$key" ]]; do
            [[ "$key" == "LAST_CHECK" ]] && last_check="$val"
        done < "$_AC_UPDATE_CACHE"
    fi
    # A corrupt cache must not break arithmetic
    [[ "$last_check" =~ ^[0-9]+$ ]] || last_check=0

    now=$(date +%s)
    if (( now - last_check < _AC_UPDATE_CHECK_INTERVAL )); then
        return 0
    fi

    if [[ "${_AC_UPDATE_CHECK_SYNC:-0}" == "1" ]]; then
        _ac_update_probe
        _ac_check_update_cache || true
    else
        ( _ac_update_probe ) >/dev/null 2>&1 &
        disown 2>/dev/null || true
    fi
    return 0
}

_ac_update_available() {
    # Returns 0 (true) if any update is available, 1 (false) otherwise
    [[ "${_AC_CLI_UPDATE_AVAILABLE:-0}" == "1" || "${_AC_LAUNCHER_UPDATE_AVAILABLE:-0}" == "1" || "${_AC_APPS_UPDATE_AVAILABLE:-0}" == "1" ]]
}

_ac_show_cli_changelog() {
    # Display changelog for a specific kiro-cli version from feed.json.
    # Called after update to show what changed. Works on Linux, Windows (Git Bash), and macOS.
    local target_version="${1:-}"
    local feed_file=""
    local reset=$'\e[0m'
    local c_dim=$'\e[2m'
    local c_title=$'\e[1;36m'
    local c_added=$'\e[32m'
    local c_changed=$'\e[33m'
    local c_fixed=$'\e[34m'

    # Resolve feed.json path using platform-aware data dir helper
    local data_dir
    data_dir=$(_ac_kiro_cli_data_dir) || return 0
    feed_file="${data_dir}/feed.json"

    [[ -f "$feed_file" ]] || return 0
    [[ -n "$target_version" ]] || return 0

    # Parse feed.json using grep/sed (portable — no jq/python dependency)
    # Extract the block for the target version and its changes
    local in_target=0 found_changes=0
    local line change_type change_desc

    printf "  %s──  What's new in %s  ──%s\n" "$c_title" "$target_version" "$reset"

    # Use a simple state-machine parser for the JSON.
    # The change type is buffered and consumed by the next description so each
    # entry is labelled and coloured; previously change_type and the colour
    # variables were assigned but never used.
    change_type=""
    while IFS= read -r line; do
        # Detect target version entry
        if [[ "$line" == *"\"version\""*"\"$target_version\""* ]]; then
            in_target=1
            continue
        fi
        # Detect next version entry (end of target block)
        if [[ $in_target -eq 1 && "$line" == *"\"version\""*"\""[0-9]* ]]; then
            break
        fi
        if [[ $in_target -eq 1 && "$line" == *"\"type\""* ]]; then
            # Capture just the quoted value, so a line holding several keys
            # ("type": "added", "description": "...") still parses.
            change_type=$(sed -n 's/.*"type"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<< "$line")
        fi
        # Parse changes within target block
        if [[ $in_target -eq 1 && "$line" == *"\"description\""* ]]; then
            change_desc=$(sed -n 's/.*"description"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<< "$line")
            found_changes=1
            local label_color="$c_dim" label=""
            case "$change_type" in
                added|Added|feature|Feature) label_color="$c_added"; label="added" ;;
                changed|Changed|improved)    label_color="$c_changed"; label="changed" ;;
                fixed|Fixed|fix|bugfix)      label_color="$c_fixed"; label="fixed" ;;
            esac
            if [[ -n "$label" ]]; then
                printf "    %s•%s %s%s%s %s\n" "$label_color" "$reset" "$label_color" "$label" "$reset" "$change_desc"
            else
                printf "    %s•%s %s\n" "$c_dim" "$reset" "$change_desc"
            fi
            change_type=""
        fi
    done < "$feed_file"

    if [[ $found_changes -eq 0 ]]; then
        printf "    %sNo changelog available for this version.%s\n" "$c_dim" "$reset"
    fi
    printf "\n"
}

# Update the launcher checkout and reload it into the running shell.
#
# Previously this ran a bare "git pull origin main" and re-read VERSION, so the
# header advertised the new version while every function in memory was still
# the old code, and the pull failed confusingly on a dirty tree or a different
# branch.
_ac_update_launcher() {
    local reset=$'\e[0m' c_dim=$'\e[2m' c_warn=$'\e[1;33m' c_err=$'\e[31m' c_ok=$'\e[1;32m'
    local dir="$_AC_SCRIPT_DIR"

    if [[ ! -d "$dir/.git" ]]; then
        printf "  %s✗ %s is not a git checkout — update manually%s\n" "$c_err" "$dir" "$reset"
        return 1
    fi

    if [[ -n "$(git -C "$dir" status --porcelain 2>/dev/null)" ]]; then
        printf "  %s✗ Local changes in %s%s\n" "$c_err" "$dir" "$reset"
        printf "    %sCommit or stash them first, then press u again.%s\n" "$c_dim" "$reset"
        return 1
    fi

    local branch
    branch=$(git -C "$dir" rev-parse --abbrev-ref HEAD 2>/dev/null) || branch=""
    if [[ "$branch" != "main" ]]; then
        printf "  %s! On branch '%s', not main — skipping self-update%s\n" "$c_warn" "$branch" "$reset"
        return 1
    fi

    _ac_spin_verbose "Updating anycli-launcher..." \
        git -C "$dir" pull --ff-only origin main || return 1

    _AC_VERSION="$(cat "$dir/VERSION" 2>/dev/null || echo "dev")"
    unset _AC_CLI_VERSION_CACHED

    # Reload the new definitions so the running shell actually gets the update.
    # Parse first: sourcing a broken file would break the caller's shell.
    local src="$dir/anycli-launcher.bash"
    if [[ -f "$src" ]] && bash -n "$src" 2>/dev/null; then
        # shellcheck source=/dev/null
        source "$src"
        printf "  %s✓ Reloaded v%s into this shell%s\n" "$c_ok" "$_AC_VERSION" "$reset"
    else
        printf "  %s! Updated to v%s but the new file did not parse%s\n" "$c_warn" "$_AC_VERSION" "$reset"
        printf "    %sOpen a new terminal, or run: source %s%s\n" "$c_dim" "$src" "$reset"
    fi
    return 0
}

_ac_do_update() {
    _ac_clear
    local reset=$'\e[0m'
    local c_title=$'\e[1;36m'
    local c_dim=$'\e[2m'
    local c_prompt=$'\e[1;32m'
    local c_name=$'\e[0;37m'
    local c_warn=$'\e[1;33m'
    local c_num=$'\e[1;33m'

    printf "\n  %s──  Update  ──────────────────────────%s\n\n" "$c_title" "$reset"

    local has_core_update=0

    # --- Core updates ($_AC_CLI_TOOL + launcher) — auto-applied on confirm ---
    # _AC_CLI_UPDATE_AVAILABLE is only ever set when _AC_CLI_TOOL is
    # "kiro-cli" (see _ac_update_probe) -- that's the only tool this has a
    # known update mechanism for.
    if [[ "${_AC_CLI_UPDATE_AVAILABLE:-0}" == "1" ]]; then
        has_core_update=1
        printf "  %s⬆  %s%s" "$c_warn" "$_AC_CLI_TOOL" "$reset"
        if [[ -n "$_AC_CLI_LATEST_VERSION" ]]; then
            printf "  %s→ %s%s" "$c_name" "$_AC_CLI_LATEST_VERSION" "$reset"
        fi
        local current_ver
        current_ver=$("$_AC_CLI_TOOL" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || echo "unknown")
        printf "  %s(current: %s)%s\n" "$c_dim" "$current_ver" "$reset"
    fi

    if [[ "${_AC_LAUNCHER_UPDATE_AVAILABLE:-0}" == "1" ]]; then
        has_core_update=1
        printf "  %s⬆  anycli-launcher%s  %s(%s commit%s behind)%s\n" \
            "$c_warn" "$reset" "$c_dim" \
            "$_AC_LAUNCHER_COMMITS_BEHIND" \
            "$( (( _AC_LAUNCHER_COMMITS_BEHIND > 1 )) && echo "s" || echo "")" \
            "$reset"
    fi

    if [[ "$has_core_update" == "1" ]]; then
        printf "\n  %sUpdate %s/launcher? (y/n)%s " "$c_prompt" "$_AC_CLI_TOOL" "$reset"
        local confirm
        read -r confirm
        if [[ "$confirm" == "y" || "$confirm" == "Y" ]]; then
            printf "\n"
            if [[ "${_AC_CLI_UPDATE_AVAILABLE:-0}" == "1" ]]; then
                _ac_spin_verbose "Updating $_AC_CLI_TOOL..." "$_AC_CLI_TOOL" update
                # The header caches the CLI version for the shell's lifetime,
                # so it must be invalidated or it keeps showing the old one.
                unset _AC_CLI_VERSION_CACHED
                printf "\n"
                # Show changelog for the newly installed version
                _ac_show_cli_changelog "$_AC_CLI_LATEST_VERSION"
            fi
            if [[ "${_AC_LAUNCHER_UPDATE_AVAILABLE:-0}" == "1" ]]; then
                _ac_update_launcher || true
                printf "\n"
            fi
            _AC_CLI_UPDATE_AVAILABLE=0
            _AC_LAUNCHER_UPDATE_AVAILABLE=0
        fi
    fi

    # --- Tracked apps — manual selection with changelogs ---
    if [[ "${_AC_APPS_UPDATE_AVAILABLE:-0}" == "1" && -n "$_AC_APPS_UPDATE_LIST" ]]; then
        printf "  %s──  App Updates  ─────────────────────%s\n\n" "$c_title" "$reset"

        # Build numbered list of apps with updates
        local -a _update_apps=()
        local app_name app_path
        for app_name in "${_AC_TRACKED_APP_NAMES[@]+"${_AC_TRACKED_APP_NAMES[@]}"}"; do
            if [[ " $_AC_APPS_UPDATE_LIST " == *" $app_name "* ]]; then
                _update_apps+=("$app_name")
            fi
        done

        local ai=1
        for app_name in "${_update_apps[@]+"${_update_apps[@]}"}"; do
            app_path="$(_ac_app_field "$app_name" path)"

            local cur_ver new_ver="" ver_file ver_pat
            cur_ver=$(_ac_app_version "$app_name")
            ver_file="$(_ac_app_field "$app_name" version_file)"
            ver_pat="$(_ac_app_field "$app_name" version_pattern)"
            [[ -n "$ver_pat" ]] || ver_pat="$_AC_DEFAULT_VERSION_PATTERN"

            # Get incoming version from origin using the configured version file
            local default_branch
            default_branch=$(git -C "$app_path" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@') || true
            [[ -z "$default_branch" ]] && default_branch="main"
            if [[ -n "$ver_file" ]]; then
                new_ver=$(git -C "$app_path" show "origin/${default_branch}:${ver_file}" 2>/dev/null | grep -oE "$ver_pat" | head -1) || true
            fi

            printf "   %s%d%s  %s%s%s" "$c_num" "$ai" "$reset" "$c_name" "$app_name" "$reset"
            if [[ -n "$cur_ver" ]]; then
                printf "  %sv%s%s" "$c_dim" "$cur_ver" "$reset"
            fi
            if [[ -n "$new_ver" && "$new_ver" != "$cur_ver" ]]; then
                printf " %s→ v%s%s" "$c_warn" "$new_ver" "$reset"
            fi
            printf "\n"

            # Show recent commits (changelog)
            local log_output
            log_output=$(git -C "$app_path" log --oneline HEAD..origin/"$default_branch" 2>/dev/null | head -5) || true
            if [[ -n "$log_output" ]]; then
                while IFS= read -r log_line; do
                    printf "      %s%s%s\n" "$c_dim" "$log_line" "$reset"
                done <<< "$log_output"
                local total_commits
                total_commits=$(git -C "$app_path" rev-list --count HEAD..origin/"$default_branch" 2>/dev/null) || true
                if [[ -n "$total_commits" && "$total_commits" -gt 5 ]]; then
                    printf "      %s... and %d more%s\n" "$c_dim" "$((total_commits - 5))" "$reset"
                fi
            fi
            printf "\n"
            ((ai++))
        done

        printf "   %sa%s  %supdate all%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "   %sb%s  %sback%s\n" "$c_num" "$reset" "$c_name" "$reset"
        printf "\n  %s›%s " "$c_prompt" "$reset"

        local choice
        read -r choice

        if [[ "$choice" == "b" || "$choice" == "B" ]]; then
            return
        fi

        # Determine which apps to update
        local -a _apps_to_update=()
        if [[ "$choice" == "a" || "$choice" == "A" ]]; then
            _apps_to_update=("${_update_apps[@]}")
        elif [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#_update_apps[@]} )); then
            _apps_to_update=("${_update_apps[$((choice - 1))]}")
        else
            printf "  %sInvalid selection%s\n" "$c_dim" "$reset"
            printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
            _ac_pause
            return
        fi

        # Apply updates for selected apps
        printf "\n"
        for app_name in "${_apps_to_update[@]+"${_apps_to_update[@]}"}"; do
            app_path="$(_ac_app_field "$app_name" path)"

            printf "  Updating %s...\n" "$app_name"
            local default_branch
            default_branch=$(git -C "$app_path" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@') || true
            [[ -z "$default_branch" ]] && default_branch="main"
            _ac_spin_verbose "Pulling $app_name..." git -C "$app_path" pull origin "$default_branch"
            local new_ver
            new_ver=$(_ac_app_version "$app_name")
            if [[ -n "$new_ver" ]]; then
                printf "  %s(v%s)%s\n" "$c_dim" "$new_ver" "$reset"
            fi
            printf "\n"

            # Remove from update list
            _AC_APPS_UPDATE_LIST="${_AC_APPS_UPDATE_LIST//$app_name/}"
            _AC_APPS_UPDATE_LIST="${_AC_APPS_UPDATE_LIST## }"
            _AC_APPS_UPDATE_LIST="${_AC_APPS_UPDATE_LIST%% }"
        done

        # Update apps state
        if [[ -z "${_AC_APPS_UPDATE_LIST// /}" ]]; then
            _AC_APPS_UPDATE_AVAILABLE=0
            _AC_APPS_UPDATE_LIST=""
        fi
    elif [[ "$has_core_update" == "0" ]]; then
        printf "  %sEverything is up to date.%s\n" "$c_dim" "$reset"
    fi

    # Clear the cache so next check is fresh
    rm -f "$_AC_UPDATE_CACHE"

    printf "\n  %sPress any key to return...%s " "$c_dim" "$reset"
    _ac_pause
}

# --- Launch history ---
#
# Records how often and how recently each project was launched, so the menu can
# float what you actually use to the top and so tooling can see usage.
_AC_RECENT_FILE="${_AC_RECENT_FILE:-${HOME}/.config/anycli-launcher/.recent}"
_AC_RECENT_MAX_ENTRIES="${_AC_RECENT_MAX_ENTRIES:-200}"

# Read one field of a project's history. field is "count" or "ts".
_ac_recent_field() {
    local name="${1:-}" field="${2:-ts}" n c t
    [[ -f "$_AC_RECENT_FILE" ]] || { printf '0'; return 0; }
    while IFS=$'\t' read -r n c t || [[ -n "$n" ]]; do
        if [[ "$n" == "$name" ]]; then
            case "$field" in
                count) [[ "$c" =~ ^[0-9]+$ ]] && printf '%s' "$c" || printf '0' ;;
                *)     [[ "$t" =~ ^[0-9]+$ ]] && printf '%s' "$t" || printf '0' ;;
            esac
            return 0
        fi
    done < "$_AC_RECENT_FILE"
    printf '0'
    return 0
}

# Note that a project was just launched.
_ac_record_launch() {
    local name="${1:-}"
    [[ -n "$name" ]] || return 0

    local dir
    dir="$(dirname "$_AC_RECENT_FILE")"
    mkdir -p "$dir" 2>/dev/null || return 0

    local tmp
    tmp="$(mktemp "${dir}/.recent.XXXXXX" 2>/dev/null)" || return 0

    local count=0 n c t now
    now=$(date +%s)
    if [[ -f "$_AC_RECENT_FILE" ]]; then
        {
            while IFS=$'\t' read -r n c t || [[ -n "$n" ]]; do
                [[ -z "$n" ]] && continue
                if [[ "$n" == "$name" ]]; then
                    [[ "$c" =~ ^[0-9]+$ ]] && count="$c"
                else
                    printf '%s\t%s\t%s\n' "$n" "$c" "$t"
                fi
            done < "$_AC_RECENT_FILE"
        } > "$tmp"
    fi
    printf '%s\t%s\t%s\n' "$name" "$(( count + 1 ))" "$now" >> "$tmp"

    # Keep the file bounded: newest entries win
    if (( $(wc -l < "$tmp") > _AC_RECENT_MAX_ENTRIES )); then
        local pruned
        pruned="$(mktemp "${dir}/.recent.XXXXXX" 2>/dev/null)" || pruned=""
        if [[ -n "$pruned" ]]; then
            sort -t$'\t' -k3,3nr "$tmp" | head -n "$_AC_RECENT_MAX_ENTRIES" > "$pruned"
            mv "$pruned" "$tmp" 2>/dev/null || rm -f "$pruned"
        fi
    fi

    mv "$tmp" "$_AC_RECENT_FILE" 2>/dev/null || rm -f "$tmp"
    return 0
}

# Reorder _AC_PROJECT_DIRS most-recently-launched first. Projects that have
# never been launched keep their alphabetical order at the end.
_ac_sort_projects_by_recent() {
    (( ${#_AC_PROJECT_DIRS[@]} > 1 )) || return 0
    local dir base ts
    local -a sorted=()
    while IFS= read -r line; do
        [[ -n "$line" ]] && sorted+=("${line#*$'\t'}")
    done < <(
        for dir in "${_AC_PROJECT_DIRS[@]}"; do
            base="${dir##*/}"
            ts="$(_ac_recent_field "$base" ts)"
            printf '%020d\t%s\n' "$ts" "$dir"
        done | sort -t$'\t' -k1,1nr -k2,2
    )
    (( ${#sorted[@]} > 0 )) && _AC_PROJECT_DIRS=("${sorted[@]}")
    return 0
}

# --- Git status ---
#
# Opt-in via show_git_status: true. Off by default because it costs two git
# invocations per project on every render.
_ac_git_summary() {
    local dir="${1:-}"
    [[ -n "$dir" && -d "$dir/.git" ]] || return 0

    local branch
    branch=$(git -C "$dir" rev-parse --abbrev-ref HEAD 2>/dev/null) || return 0
    [[ -n "$branch" ]] || return 0

    local out="$branch"
    # A single porcelain line is enough to know the tree is dirty
    if [[ -n "$(git -C "$dir" status --porcelain 2>/dev/null | head -1)" ]]; then
        out="${out}*"
    fi

    local counts behind ahead
    counts=$(git -C "$dir" rev-list --left-right --count '@{u}...HEAD' 2>/dev/null) || counts=""
    if [[ -n "$counts" ]]; then
        behind="${counts%%[[:space:]]*}"
        ahead="${counts##*[[:space:]]}"
        if [[ "$ahead" =~ ^[0-9]+$ ]] && (( ahead > 0 )); then
            out="${out} ↑${ahead}"
        fi
        if [[ "$behind" =~ ^[0-9]+$ ]] && (( behind > 0 )); then
            out="${out} ↓${behind}"
        fi
    fi
    printf '%s' "$out"
    return 0
}

# True when "$1" < "$2" under whatever collation is in effect in the calling
# environment. Exists so callers can force byte-wise (C-locale) string
# comparison for data that must sort the same regardless of the user's
# locale (e.g. ISO8601 timestamps) by prefixing the call with LC_ALL=C --
# `LC_ALL=C [[ "$a" < "$b" ]]` is not valid bash ([[ is a keyword, not a
# simple command, so a leading var=value assignment doesn't apply to it),
# but `LC_ALL=C _ac_str_lt "$a" "$b"` is.
_ac_str_lt() {
    [[ "$1" < "$2" ]]
}

# Convert an ISO8601 timestamp (e.g. 2026-09-04T13:19:09.305Z) to epoch seconds.
# Portable across GNU date (Linux, WSL, Git Bash coreutils) and BSD/macOS date.
# Prints the epoch or nothing on failure.
_ac_iso_to_epoch() {
    local iso="${1:-}"
    [[ -n "$iso" ]] || return 0
    local epoch
    # GNU date (Linux / WSL / Git Bash): accepts ISO8601 directly.
    epoch="$(date -d "$iso" +%s 2>/dev/null)" && { printf '%s' "$epoch"; return 0; }
    # BSD/macOS date: strip fractional seconds and trailing Z, parse explicitly.
    local trimmed="${iso%%.*}"
    trimmed="${trimmed%Z}"
    epoch="$(date -j -u -f "%Y-%m-%dT%H:%M:%S" "$trimmed" +%s 2>/dev/null)" && { printf '%s' "$epoch"; return 0; }
    return 0
}

# --- Session summary ---
#
# Opt-in via show_sessions: true. Off by default because it spawns the AI CLI
# once per project to list its saved sessions. Prints "title<TAB>epoch" for the
# most recent non-empty session in <dir>, or nothing when there are no sessions
# or the lookup fails. Best-effort: a parse miss degrades to empty rather than
# corrupting the menu. Cross-platform: works on Linux, WSL2, macOS, and Git Bash
# (guards the absence of `timeout` and handles both GNU and BSD `date`).
_ac_session_summary() {
    local dir="${1:-}"
    [[ -n "$dir" && -d "$dir" ]] || return 0

    local list_argv
    list_argv="$(_ac_cli_list_argv "$_AC_CLI_TOOL")"
    [[ -n "$list_argv" ]] || return 0

    # Fetch JSON with a hard timeout when available; fall back to a plain call on
    # platforms/shells without `timeout`. Fail quiet on any error.
    # list_argv is a space-separated command (e.g. "chat --list-sessions --format
    # json"); split it into an array rather than relying on word-splitting.
    local -a largv
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        read -rA largv <<< "$list_argv"
    else
        read -ra largv <<< "$list_argv"
    fi
    local json
    if command -v timeout >/dev/null 2>&1; then
        json="$(timeout 3 bash -c 'cd "$1" 2>/dev/null && shift && "$@"' _ "$dir" "$_AC_CLI_TOOL" "${largv[@]}" 2>/dev/null)" || return 0
    else
        json="$(bash -c 'cd "$1" 2>/dev/null && shift && "$@"' _ "$dir" "$_AC_CLI_TOOL" "${largv[@]}" 2>/dev/null)" || return 0
    fi
    [[ -n "$json" ]] || return 0

    local title="" iso=""
    if command -v jq >/dev/null 2>&1; then
        # jq path: newest non-empty session by updatedAt.
        local parsed
        parsed="$(printf '%s' "$json" | jq -r '
            [ .[].sessions[]? | select(.messageCount > 0) ]
            | sort_by(.updatedAt) | last
            | if . == null then "" else "\(.title)\t\(.updatedAt)" end
        ' 2>/dev/null)" || parsed=""
        [[ -z "$parsed" ]] && return 0
        title="${parsed%%$'\t'*}"
        iso="${parsed##*$'\t'}"
    else
        # Best-effort fallback: no jq. Titles are free-form and may contain
        # commas/quotes, so this is inherently fragile — on any ambiguity we
        # return empty and let the caller show a title-less marker if it wants.
        # ISO8601 sorts chronologically as text, so the max updatedAt is newest
        # -- but only under byte-wise (C-locale) collation. bash's [[ > ]]
        # uses the current locale's collation, which in some locales can
        # reorder or ignore punctuation/case, silently picking the wrong
        # "newest" session. Force C collation for this comparison specifically.
        local newest="" ts
        while IFS= read -r ts; do
            LC_ALL=C _ac_str_lt "$newest" "$ts" && newest="$ts"
        done < <(printf '%s' "$json" | grep -oE '"updatedAt":"[^"]*"' | sed 's/"updatedAt":"//; s/"$//')
        [[ -z "$newest" ]] && return 0
        iso="$newest"
        # Title best-effort: the object chunk containing this timestamp.
        local chunk
        chunk="$(printf '%s' "$json" | tr '{' '\n' | grep -F "$newest" | head -1)"
        # Skip zero-message sessions.
        if [[ "$chunk" =~ \"messageCount\":0([^0-9]|$) ]]; then
            return 0
        fi
        if [[ "$chunk" =~ \"title\":\"(([^\"\\]|\\.)*)\" ]]; then
            title="${BASH_REMATCH[1]}"
        fi
    fi

    [[ -z "$iso" ]] && return 0

    local epoch
    epoch="$(_ac_iso_to_epoch "$iso")"
    [[ -z "$epoch" ]] && return 0

    printf '%s\t%s' "$title" "$epoch"
    return 0
}

# In-invocation cache of session summaries, keyed by safe directory name. The
# value is "title<TAB>epoch", or the sentinel "\0none" when a lookup returned
# nothing (so a session-less project is not re-queried on every re-render).
# Declared here; populated lazily by _ac_session_cached only when show_sessions
# is on. Never populated when the toggle is off — zero CLI spawns in that case.
declare -gA _AC_SESSION_CACHE 2>/dev/null || declare -A _AC_SESSION_CACHE

# Return the cached session summary for a directory, computing it once on first
# request. Prints "title<TAB>epoch" or nothing.
_ac_session_cached() {
    local dir="${1:-}" safe
    [[ -n "$dir" ]] || return 0
    safe="$(_ac_safe_name "${dir##*/}")"
    if [[ -z "${_AC_SESSION_CACHE[$safe]+set}" ]]; then
        local summary
        summary="$(_ac_session_summary "$dir")"
        if [[ -n "$summary" ]]; then
            _AC_SESSION_CACHE[$safe]="$summary"
        else
            _AC_SESSION_CACHE[$safe]=$'\0none'
        fi
    fi
    local cached="${_AC_SESSION_CACHE[$safe]}"
    [[ "$cached" == $'\0none' ]] && return 0
    printf '%s' "$cached"
    return 0
}

# Compact relative age for an epoch timestamp, e.g. "2h", "3d", "just now".
_ac_relative_age() {
    local epoch="${1:-}"
    [[ "$epoch" =~ ^[0-9]+$ ]] || return 0
    local now delta
    now="$(date +%s 2>/dev/null)" || return 0
    delta=$(( now - epoch ))
    (( delta < 0 )) && delta=0
    if   (( delta < 60 ));     then printf 'just now'
    elif (( delta < 3600 ));   then printf '%dm' "$(( delta / 60 ))"
    elif (( delta < 86400 ));  then printf '%dh' "$(( delta / 3600 ))"
    elif (( delta < 604800 )); then printf '%dd' "$(( delta / 86400 ))"
    else                            printf '%dw' "$(( delta / 604800 ))"
    fi
    return 0
}

# Format the session suffix shown in a menu row: a marker, the (truncated)
# last-session title, and its relative age. Prints nothing when there is no
# session. Title is truncated to 32 chars so it does not push the row too wide.
_ac_session_suffix() {
    local dir="${1:-}"
    local summary title epoch age
    summary="$(_ac_session_cached "$dir")"
    [[ -n "$summary" ]] || return 0
    title="${summary%%$'\t'*}"
    epoch="${summary##*$'\t'}"
    if (( ${#title} > 32 )); then
        title="${title:0:31}…"
    fi
    age="$(_ac_relative_age "$epoch")"
    if [[ -n "$title" && -n "$age" ]]; then
        printf '⏵ "%s" · %s' "$title" "$age"
    elif [[ -n "$age" ]]; then
        printf '⏵ session · %s' "$age"
    else
        printf '⏵ session'
    fi
    return 0
}

# --- Project discovery and name resolution ---

# Every directory that is scanned for projects: the configured root plus any
# extra_dirs.
_ac_project_roots() {
    _AC_PROJECT_ROOTS=("$_AC_PROJECTS_DIR")
    local extra
    for extra in "${_AC_EXTRA_DIRS[@]+"${_AC_EXTRA_DIRS[@]}"}"; do
        [[ -n "$extra" ]] && _AC_PROJECT_ROOTS+=("$extra")
    done
    return 0
}

# True when the path is one of the scanned roots.
_ac_is_project_root() {
    local candidate="${1:-}" root
    for root in "${_AC_PROJECT_ROOTS[@]+"${_AC_PROJECT_ROOTS[@]}"}"; do
        [[ "${root%/}" == "${candidate%/}" ]] && return 0
    done
    return 1
}

# Populate the global _AC_PROJECT_DIRS with every project directory to offer.
_ac_collect_projects() {
    _AC_PROJECT_DIRS=()
    _ac_project_roots

    local depth="${_AC_SCAN_DEPTH:-1}"
    local root dir child
    local -a kids

    for root in "${_AC_PROJECT_ROOTS[@]}"; do
        [[ -n "$root" && -d "$root" ]] || continue
        for dir in "$root"/*/; do
            [[ -d "$dir" ]] || continue
            dir="${dir%/}"

            # With scan_depth 2, a directory that is not itself a repository is
            # treated as a grouping folder and its children are listed instead.
            # This supports layouts like ~/projects/work/* alongside
            # ~/projects/standalone-repo.
            if [[ "$depth" == "2" && ! -d "$dir/.git" ]]; then
                kids=()
                for child in "$dir"/*/; do
                    [[ -d "$child" ]] || continue
                    kids+=("${child%/}")
                done
                if (( ${#kids[@]} > 0 )); then
                    _AC_PROJECT_DIRS+=("${kids[@]}")
                    continue
                fi
            fi

            _AC_PROJECT_DIRS+=("$dir")
        done
    done

    # Alphabetical by default (glob order); opt in to recency with
    # sort_order: recent
    if [[ "${_AC_SORT_ORDER:-name}" == "recent" ]]; then
        _ac_sort_projects_by_recent
    fi
    return 0
}

# Resolve the configured alias for a project directory name.
#
# Fast path: exact safe-name key (the modern, correct storage form).
# Fallback: scan every _AC_PROJECT_ALIAS_* variable, decode its suffix back to
# a project name, and match in two collision-safe stages:
#   1. exact, case-insensitive (a key that differs only by letter case)
#   2. the precise legacy-bug shape: the requested name with hyphens replaced
#      by underscores equals the stored key. Older versions sanitized keys by
#      turning "-" into "_" (one-directional), so a hyphenated directory like
#      "public-knowledge-network" matches a legacy key "public_knowledge_network".
# Because the match is one-directional (hyphen dir → underscore key), a genuine
# "my_app" directory will NOT falsely pick up an alias set for a distinct
# "my-app" directory. Prints the alias and returns 0 when found, 1 otherwise.
_ac_lookup_alias() {
    local name="${1:-}" safe val varname p decoded
    [[ -z "$name" ]] && return 1

    # Fast path: exact encoded key.
    safe="$(_ac_safe_name "$name")"
    val="$(_ac_indirect "_AC_PROJECT_ALIAS_${safe}")"
    if [[ -n "$val" ]]; then
        printf '%s' "$val"
        return 0
    fi

    # Fallback only runs when the fast path missed. Capture the alias var list
    # once; if no aliases are configured, skip the fallback setup entirely.
    local -a alias_vars=()
    local _ac_av
    while IFS= read -r _ac_av; do
        [[ -n "$_ac_av" ]] && alias_vars+=("$_ac_av")
    done < <(_ac_vars_with_prefix _AC_PROJECT_ALIAS_)
    (( ${#alias_vars[@]} == 0 )) && return 1

    local name_lower="$(_ac_lower "$name")"
    # The legacy key form: this name with every hyphen turned into an underscore.
    local legacy="${name//-/_}"
    for varname in "${alias_vars[@]}"; do
        p="${varname#_AC_PROJECT_ALIAS_}"
        decoded="$(_ac_project_name "$p")"
        # Stage 1: exact, case-insensitive.
        # Stage 2: legacy hyphen->underscore shape (only when the name actually
        # contains a hyphen, so identical names don't self-trigger).
        if [[ "$(_ac_lower "$decoded")" == "$name_lower" ]] \
           || { [[ "$name" == *-* ]] && [[ "$decoded" == "$legacy" ]]; }; then
            val="$(_ac_indirect "$varname")"
            if [[ -n "$val" ]]; then
                printf '%s' "$val"
                return 0
            fi
        fi
    done
    return 1
}

# The label shown for a project directory.
#
# A configured alias always wins. Otherwise the directory name is used, except
# for a project nested under a grouping folder, where the group is included so
# that work/api and personal/api stay distinguishable.
_ac_project_label() {
    local dir="${1%/}" base parent alias_val
    base="${dir##*/}"
    alias_val="$(_ac_lookup_alias "$base")"
    if [[ -n "$alias_val" ]]; then
        printf '%s' "$alias_val"
        return 0
    fi
    parent="${dir%/*}"
    if _ac_is_project_root "$parent"; then
        printf '%s' "$base"
    else
        printf '%s/%s' "${parent##*/}" "$base"
    fi
    return 0
}

# The name shown in the menu: the configured alias if there is one, else the
# directory name.
_ac_display_name() {
    local dirname="${1:-}" alias_val
    alias_val="$(_ac_lookup_alias "$dirname")"
    printf '%s' "${alias_val:-$dirname}"
}

# Resolve a user-supplied name to exactly one project directory.
#
# Matching is tried in decreasing strictness: exact, case-insensitive exact,
# case-insensitive prefix, then case-insensitive substring. Both the directory
# name and the configured alias are considered.
#
# On success sets _AC_RESOLVED_DIR and returns 0.
# Returns 1 when nothing matched, 2 when the query is ambiguous (candidates are
# left in _AC_RESOLVED_MATCHES).
_ac_resolve_project() {
    local query="${1:-}"
    _AC_RESOLVED_DIR=""
    _AC_RESOLVED_MATCHES=()
    [[ -n "$query" ]] || return 1

    _ac_collect_projects
    local -a exact=() ci_exact=() prefix=() substr=()
    local dir base disp q_lower b_lower d_lower
    q_lower="$(_ac_lower "$query")"

    local label l_lower
    for dir in "${_AC_PROJECT_DIRS[@]+"${_AC_PROJECT_DIRS[@]}"}"; do
        base="${dir##*/}"
        disp="$(_ac_display_name "$base")"
        label="$(_ac_project_label "$dir")"
        b_lower="$(_ac_lower "$base")"
        d_lower="$(_ac_lower "$disp")"
        l_lower="$(_ac_lower "$label")"

        if [[ "$base" == "$query" || "$disp" == "$query" || "$label" == "$query" ]]; then
            exact+=("$dir")
        elif [[ "$b_lower" == "$q_lower" || "$d_lower" == "$q_lower" || "$l_lower" == "$q_lower" ]]; then
            ci_exact+=("$dir")
        elif [[ "$b_lower" == "$q_lower"* || "$d_lower" == "$q_lower"* || "$l_lower" == "$q_lower"* ]]; then
            prefix+=("$dir")
        elif [[ "$b_lower" == *"$q_lower"* || "$d_lower" == *"$q_lower"* || "$l_lower" == *"$q_lower"* ]]; then
            substr+=("$dir")
        fi
    done

    local -a matches=()
    if   (( ${#exact[@]}    > 0 )); then matches=("${exact[@]}")
    elif (( ${#ci_exact[@]} > 0 )); then matches=("${ci_exact[@]}")
    elif (( ${#prefix[@]}   > 0 )); then matches=("${prefix[@]}")
    elif (( ${#substr[@]}   > 0 )); then matches=("${substr[@]}")
    fi

    case ${#matches[@]} in
        1) _AC_RESOLVED_DIR="${matches[0]}"; return 0 ;;
        0) return 1 ;;
        *) _AC_RESOLVED_MATCHES=("${matches[@]}"); return 2 ;;
    esac
}

# True when a project directory or alias matches the name exactly. Used so that
# a project called "doctor" or "config" still wins over the subcommand.
_ac_project_exists_exact() {
    local query="${1:-}" dir base
    _ac_collect_projects
    for dir in "${_AC_PROJECT_DIRS[@]+"${_AC_PROJECT_DIRS[@]}"}"; do
        base="${dir##*/}"
        if [[ "$base" == "$query" || "$(_ac_display_name "$base")" == "$query" ]]; then
            return 0
        fi
    done
    return 1
}

_ac_usage() {
    printf 'anycli-launcher %s — launch your AI CLI in a project directory\n\n' "$_AC_VERSION"
    printf 'Usage:\n'
    printf '  ac                    open the interactive menu\n'
    printf '  ac <name>             launch directly in the matching project\n'
    printf '  ac resume <name>      resume the project'"'"'s most recent session\n'
    printf '  ac <name> --resume    same as above (also -r)\n'
    printf '  ac pick <name>        pick a session to resume (interactive)\n'
    printf '  ac <name> --resume-picker  same as above\n'
    printf '  ac list               list projects (one per line)\n'
    printf '  ac config             open the configuration menu\n'
    printf '  ac setup              re-run guided setup (keeps existing values)\n'
    printf '  ac doctor             run a setup/dependency health check\n'
    printf '  ac help               show this help\n'
    printf '  ac version            print the version\n\n'
    printf 'Options:\n'
    printf '  -r, --resume          resume the most recent session for <name>\n'
    printf '  --resume-picker       pick a session to resume for <name>\n'
    printf '  -h, --help            same as "ac help"\n'
    printf '  -V, --version         same as "ac version"\n'
    printf '  -l, --list            same as "ac list"\n\n'
    printf '<name> matches a directory name or a configured alias, trying exact,\n'
    printf 'case-insensitive, prefix, then substring. A project whose name is also a\n'
    printf 'subcommand takes precedence over the subcommand.\n\n'
    printf 'Configuration: %s\n' "$_AC_CONFIG_FILE"
    return 0
}

# First-run guided setup (issue #14).
#
# Runs the very first time `ac` is invoked with no config file yet, turning the
# bare "projects directory not found" error into a short guided setup: pick a
# projects root (from auto-detected candidates), pick an AI CLI (from tools
# found on PATH), and optionally set default flags. It writes the config and
# returns 0 so the caller can render the menu in the same invocation.
#
# The caller is responsible for the trigger gate (no config file + interactive
# TTY). Returns 0 when a config was written, 1 when the user aborted.
_ac_first_run_wizard() {
    # Establish all _AC_* defaults first. With no config file present this is a
    # cheap no-op that just seeds defaults, and it guarantees _ac_write_config
    # has every variable it references (important under `set -u`).
    _ac_load_config

    local reset=$'\e[0m' c_title=$'\e[1;36m' c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m' c_dim=$'\e[2m' c_prompt=$'\e[1;32m' c_ok=$'\e[32m'

    printf '\n'
    printf "  %s──  Welcome to anycli-launcher  ────────%s\n\n" "$c_title" "$reset"
    printf "  %sNo config yet — let's set it up. Three quick questions.%s\n" "$c_dim" "$reset"

    # --- Question 1: projects root -----------------------------------------
    # Offer auto-detected candidates: common root names that exist AND contain
    # at least one subdirectory (an empty dir is a poor default). "~/projects"
    # is the launcher's standard convention, so it is always recommended and
    # listed first when present.
    local -a candidates=()
    local cand cand_kids kid
    for cand in "$HOME/projects" "$HOME/code" "$HOME/dev" "$HOME/repos" "$HOME/src" "$HOME/workspace"; do
        [[ -d "$cand" ]] || continue
        cand_kids=0
        for kid in "$cand"/*/; do
            [[ -d "$kid" ]] && { cand_kids=1; break; }
        done
        (( cand_kids == 1 )) && candidates+=("$cand")
    done
    # "$HOME/projects" is first in the scan order, so if it exists it is already
    # candidate #1 — the recommended default.

    printf "\n  %s1. Where do your projects live?%s\n" "$c_name" "$reset"
    printf "  %s(\"~/projects\" is the recommended convention.)%s\n\n" "$c_dim" "$reset"
    local i=1 chosen_root=""
    if (( ${#candidates[@]} > 0 )); then
        for cand in "${candidates[@]}"; do
            if [[ "$cand" == "$HOME/projects" ]]; then
                printf "     %s%d%s  %s  %s(recommended)%s\n" "$c_num" "$i" "$reset" "$(_ac_tildify "$cand")" "$c_ok" "$reset"
            else
                printf "     %s%d%s  %s\n" "$c_num" "$i" "$reset" "$(_ac_tildify "$cand")"
            fi
            i=$((i+1))
        done
        printf "     %s%d%s  other (type a path)\n\n" "$c_num" "$i" "$reset"
        printf "  %s›%s " "$c_prompt" "$reset"
        local pick
        read -r pick || return 1
        if [[ "$pick" =~ ^[0-9]+$ ]] && (( pick >= 1 && pick <= ${#candidates[@]} )); then
            chosen_root="${candidates[$((pick-1))]}"
        fi
    fi
    # Fall through to a typed path when there were no candidates, or the user
    # chose "other", or entered something that was not a listed number. When no
    # candidates were detected, recommend "~/projects" as the default so a blank
    # Enter sets up the standard layout.
    if [[ -z "$chosen_root" ]]; then
        local _default_root=""
        if (( ${#candidates[@]} == 0 )); then
            _default_root="$HOME/projects"
            printf "\n  %sEnter your projects directory, or press Enter for the%s\n" "$c_name" "$reset"
            printf "  %srecommended %s~/projects%s:%s\n" "$c_name" "$c_ok" "$c_name" "$reset"
        else
            printf "\n  %sEnter your projects directory (e.g. ~/projects):%s\n" "$c_name" "$reset"
        fi
        printf "  %s›%s " "$c_prompt" "$reset"
        local typed
        read -r typed || return 1
        # Blank input takes the recommended default when one is offered.
        if [[ -z "$typed" && -n "$_default_root" ]]; then
            typed="$_default_root"
        fi
        typed="$(_ac_normalize_typed_path "$typed")"
        [[ -z "$typed" ]] && { printf "\n  %sNo directory given — setup cancelled.%s\n" "$c_dim" "$reset"; return 1; }
        chosen_root="$typed"
    fi
    if [[ ! -d "$chosen_root" ]]; then
        printf "\n  %sHeads up: %s does not exist yet.%s\n" "$c_dim" "$chosen_root" "$reset"
        printf "  Create it and continue? (Y/n): "
        local mk
        read -r mk || return 1
        if [[ -z "$mk" || "$mk" == "y" || "$mk" == "Y" ]]; then
            mkdir -p "$chosen_root" 2>/dev/null || {
                printf "  %s✗ Could not create %s — setup cancelled.%s\n" "$c_dim" "$chosen_root" "$reset"
                return 1
            }
        else
            printf "  %sSetup cancelled.%s\n" "$c_dim" "$reset"
            return 1
        fi
    fi

    # --- Question 2: AI CLI tool -------------------------------------------
    local detected
    detected="$(_ac_detect_clis)"
    local -a clis=()
    read -r -a clis <<< "$detected"

    printf "\n  %s2. Which AI CLI should I launch?%s\n\n" "$c_name" "$reset"
    local chosen_cli=""
    if (( ${#clis[@]} > 0 )); then
        i=1
        for cand in "${clis[@]}"; do
            printf "     %s%d%s  %s\n" "$c_num" "$i" "$reset" "$cand"
            i=$((i+1))
        done
        printf "\n  %s›%s " "$c_prompt" "$reset"
        local cpick
        read -r cpick || return 1
        if [[ "$cpick" =~ ^[0-9]+$ ]] && (( cpick >= 1 && cpick <= ${#clis[@]} )); then
            chosen_cli="${clis[$((cpick-1))]}"
        else
            # Default to the first detected tool on any non-numeric/invalid input.
            chosen_cli="${clis[0]}"
        fi
    else
        printf "     %sNo known AI CLI found on PATH.%s\n" "$c_dim" "$reset"
        printf "     %sEnter the command to run (e.g. claude, aider):%s\n" "$c_dim" "$reset"
        printf "  %s›%s " "$c_prompt" "$reset"
        read -r chosen_cli || return 1
        if [[ -z "$chosen_cli" ]]; then
            printf "  %sSetup cancelled — no CLI tool given.%s\n" "$c_dim" "$reset"
            return 1
        fi
    fi

    # --- Question 3: default flags (optional) ------------------------------
    printf "\n  %s3. Default flags for every launch? (optional, Enter to skip)%s\n" "$c_name" "$reset"
    printf "     %se.g. --trust-all-tools%s\n\n" "$c_dim" "$reset"
    printf "  %s›%s " "$c_prompt" "$reset"
    local flags
    read -r flags || return 1

    # --- Persist -----------------------------------------------------------
    _AC_PROJECTS_DIR="$chosen_root"
    _AC_CLI_TOOL="$chosen_cli"
    _AC_CLI_LAUNCH="$(_ac_get_default_launch "$chosen_cli")"
    _AC_CLI_FLAGS="$flags"
    _ac_write_config

    printf "\n  %s✓ Setup complete.%s Config saved to %s\n" "$c_ok" "$reset" "$_AC_CONFIG_FILE"
    printf "  %sYou can change any of this later with 'ac' → 'c'.%s\n\n" "$c_dim" "$reset"
    printf "  %sPress Enter to open your menu...%s" "$c_dim" "$reset"
    read -r _ || true
    return 0
}

# 'ac setup' — re-runnable guided setup (non-destructive).
#
# Unlike the first-run wizard (which only fires when no config exists), this can
# be invoked any time via `ac setup`. It revisits the three basics — projects
# root, AI CLI, default flags — with the CURRENT value pre-filled as the
# default, so pressing Enter keeps what you already have. It never touches
# per-project configuration (flags/color/title/prompt/alias) or preferences
# (theme, sort order, git/session toggles): _ac_load_config loads them and
# _ac_write_config re-emits them verbatim. Returns 0 on success, 1 on abort.
_ac_setup() {
    # Load current config so pre-filled defaults reflect reality and every
    # existing value (including the projects: map) survives the rewrite.
    _ac_load_config

    local reset=$'\e[0m' c_title=$'\e[1;36m' c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m' c_dim=$'\e[2m' c_prompt=$'\e[1;32m' c_ok=$'\e[32m'

    printf '\n'
    printf "  %s──  anycli-launcher Setup  ────────%s\n\n" "$c_title" "$reset"
    printf "  %sRevisit the basics. Press Enter to keep the current value.%s\n" "$c_dim" "$reset"
    printf "  %sPer-project settings and preferences are left untouched.%s\n" "$c_dim" "$reset"

    # --- Projects root -----------------------------------------------------
    printf "\n  %s1. Projects directory%s\n" "$c_name" "$reset"
    printf "     %scurrent: %s%s%s\n" "$c_dim" "$c_ok" "$(_ac_tildify "$_AC_PROJECTS_DIR")" "$reset"
    printf "  %s› %s(Enter to keep)%s " "$c_prompt" "$c_dim" "$reset"
    local new_root
    read -r new_root || return 1
    if [[ -n "$new_root" ]]; then
        new_root="$(_ac_normalize_typed_path "$new_root")"
        if [[ ! -d "$new_root" ]]; then
            printf "  %s%s does not exist. Create it? (Y/n): %s" "$c_dim" "$new_root" "$reset"
            local mk
            read -r mk || return 1
            if [[ -z "$mk" || "$mk" == "y" || "$mk" == "Y" ]]; then
                mkdir -p "$new_root" 2>/dev/null || {
                    printf "  %s✗ Could not create %s — keeping current value.%s\n" "$c_dim" "$new_root" "$reset"
                    new_root=""
                }
            else
                printf "  %sKeeping current value.%s\n" "$c_dim" "$reset"
                new_root=""
            fi
        fi
        [[ -n "$new_root" ]] && _AC_PROJECTS_DIR="$new_root"
    fi

    # --- AI CLI tool -------------------------------------------------------
    printf "\n  %s2. AI CLI tool%s\n" "$c_name" "$reset"
    printf "     %scurrent: %s%s %s%s\n" "$c_dim" "$c_ok" "$_AC_CLI_TOOL" "$_AC_CLI_LAUNCH" "$reset"
    local detected
    detected="$(_ac_detect_clis)"
    local -a clis=()
    read -r -a clis <<< "$detected"
    if (( ${#clis[@]} > 0 )); then
        printf "     %sdetected on PATH:%s\n" "$c_dim" "$reset"
        local i=1 cand
        for cand in "${clis[@]}"; do
            printf "       %s%d%s  %s\n" "$c_num" "$i" "$reset" "$cand"
            i=$((i+1))
        done
        printf "  %s› %s(number to switch, Enter to keep)%s " "$c_prompt" "$c_dim" "$reset"
        local cpick
        read -r cpick || return 1
        if [[ "$cpick" =~ ^[0-9]+$ ]] && (( cpick >= 1 && cpick <= ${#clis[@]} )); then
            _AC_CLI_TOOL="${clis[$((cpick-1))]}"
            _AC_CLI_LAUNCH="$(_ac_get_default_launch "$_AC_CLI_TOOL")"
        fi
    else
        printf "     %s(no known CLI detected on PATH — keeping current)%s\n" "$c_dim" "$reset"
    fi

    # --- Default flags -----------------------------------------------------
    printf "\n  %s3. Default flags%s\n" "$c_name" "$reset"
    printf "     %scurrent: %s%s%s\n" "$c_dim" "$c_ok" "${_AC_CLI_FLAGS:-(none)}" "$reset"
    printf "     %sEnter to keep, or type '-' to clear.%s\n" "$c_dim" "$reset"
    printf "  %s› %s " "$c_prompt" "$reset"
    local new_flags
    read -r new_flags || return 1
    if [[ "$new_flags" == "-" ]]; then
        _AC_CLI_FLAGS=""
    elif [[ -n "$new_flags" ]]; then
        _AC_CLI_FLAGS="$new_flags"
    fi

    _ac_write_config

    printf "\n  %s✓ Setup saved.%s %s\n" "$c_ok" "$reset" "$_AC_CONFIG_FILE"
    printf "  %sPer-project settings and preferences were preserved.%s\n\n" "$c_dim" "$reset"
    return 0
}

# 'ac doctor' — a first-class health report for the launcher's own
# configuration plus the underlying platform/dependency checks.
#
# It runs launcher-specific checks inline (these need the loaded config, which
# a standalone script can only approximate), then delegates the environment and
# dependency checks to tests/check-platform.sh when that script is reachable
# next to the launcher (no logic duplication). Exit status is non-zero when a
# REQUIRED launcher check fails or the delegated platform check fails.
_ac_doctor() {
    _ac_load_config

    local ok warn err dim rst
    if [[ -t 1 ]]; then
        ok=$'\e[32m'; warn=$'\e[33m'; err=$'\e[31m'; dim=$'\e[2m'; rst=$'\e[0m'
    else
        ok=""; warn=""; err=""; dim=""; rst=""
    fi

    local fails=0 warns=0
    # Local helper closures. Defined with plain names (bash has no truly local
    # functions); they read/modify fails/warns in the caller's scope.
    _ac_doctor_pass() { printf '  %s✓%s %s\n' "$ok" "$rst" "$1"; }
    _ac_doctor_warn() { printf '  %s!%s %s%s%s\n' "$warn" "$rst" "$1" "${2:+ — }" "${2:-}"; warns=$((warns+1)); }
    _ac_doctor_fail() { printf '  %s✗%s %s%s%s\n' "$err" "$rst" "$1" "${2:+ — }" "${2:-}"; fails=$((fails+1)); }

    printf '%sanycli-launcher doctor%s  (v%s)\n' "$dim" "$rst" "$_AC_VERSION"

    # --- Launcher configuration ---------------------------------------------
    printf '\n%s── Launcher configuration ──%s\n' "$dim" "$rst"

    if [[ -f "$_AC_CONFIG_FILE" ]]; then
        _ac_doctor_pass "config file present ($_AC_CONFIG_FILE)"
    else
        _ac_doctor_warn "no config file yet ($_AC_CONFIG_FILE)" "defaults in use; run 'ac' then 'c' to configure"
    fi

    if [[ -d "$_AC_PROJECTS_DIR" ]]; then
        local _n_kids=0 _kid
        for _kid in "$_AC_PROJECTS_DIR"/*/; do
            [[ -d "$_kid" ]] && _n_kids=$((_n_kids+1))
        done
        if (( _n_kids > 0 )); then
            _ac_doctor_pass "projects directory exists with $_n_kids entr$([[ $_n_kids -eq 1 ]] && echo y || echo ies) ($_AC_PROJECTS_DIR)"
        else
            _ac_doctor_warn "projects directory is empty ($_AC_PROJECTS_DIR)" "the menu will have nothing to list"
        fi
    else
        _ac_doctor_fail "projects directory not found ($_AC_PROJECTS_DIR)" "set projects_dir in config or \$AC_PROJECTS_DIR"
    fi

    printf '  %ssort order: %s · git status: %s · sessions: %s%s\n' \
        "$dim" "${_AC_SORT_ORDER:-name}" "${_AC_SHOW_GIT_STATUS:-false}" "${_AC_SHOW_SESSIONS:-false}" "$rst"

    # --- Configured AI CLI --------------------------------------------------
    printf '\n%s── AI CLI ──%s\n' "$dim" "$rst"
    printf '  %sconfigured CLI: %s %s%s\n' "$dim" "$_AC_CLI_TOOL" "$_AC_CLI_LAUNCH" "$rst"
    if command -v "$_AC_CLI_TOOL" >/dev/null 2>&1; then
        # Report the resolved executable path (_ac_resolve_bin), which is what
        # the new-tab launch execs. If a shell alias shadows the tool name,
        # note it: the new tab runs the real binary, not the alias, so this
        # explains any divergence between current-tab (alias applies) and
        # new-tab behavior.
        local _cli_path
        _cli_path="$(_ac_resolve_bin "$_AC_CLI_TOOL")"
        if [[ -n "$_cli_path" ]]; then
            _ac_doctor_pass "$_AC_CLI_TOOL on PATH → $_cli_path"
        else
            _ac_doctor_pass "$_AC_CLI_TOOL is on PATH"
        fi
        if alias "$_AC_CLI_TOOL" >/dev/null 2>&1; then
            local _cli_alias
            _cli_alias="$(alias "$_AC_CLI_TOOL" 2>/dev/null)"
            _ac_doctor_warn "$_AC_CLI_TOOL is shadowed by a shell alias (${_cli_alias})" \
                "new-tab launches run the resolved binary, not the alias"
        fi
        local _r_flag _p_flag _l_argv
        _r_flag="$(_ac_cli_resume_flag "$_AC_CLI_TOOL")"
        _p_flag="$(_ac_cli_picker_flag "$_AC_CLI_TOOL")"
        _l_argv="$(_ac_cli_list_argv "$_AC_CLI_TOOL")"
        if [[ -n "$_r_flag" ]]; then _ac_doctor_pass "resume supported ($_r_flag) — r<N>, ac resume"
        else _ac_doctor_warn "no resume flag for $_AC_CLI_TOOL" "resume launches start fresh"; fi
        if [[ -n "$_p_flag" ]]; then _ac_doctor_pass "session picker supported ($_p_flag) — p<N>, ac pick"
        else _ac_doctor_warn "no session picker for $_AC_CLI_TOOL" "the picker starts fresh"; fi
        if [[ -n "$_l_argv" ]]; then _ac_doctor_pass "session listing supported — Show last session works"
        else _ac_doctor_warn "no session listing for $_AC_CLI_TOOL" "the session-aware menu shows nothing"; fi
    else
        _ac_doctor_fail "$_AC_CLI_TOOL not on PATH" "the launcher can cd but cannot launch the CLI"
    fi

    # --- Delegated platform/dependency checks -------------------------------
    printf '\n%s── Platform & dependencies ──%s\n' "$dim" "$rst"
    local _platform_check="$_AC_SCRIPT_DIR/tests/check-platform.sh"
    local _platform_rc=0
    if [[ -f "$_platform_check" ]]; then
        # Run the shared platform checker so its output appears in the report;
        # its exit status folds into the doctor verdict.
        bash "$_platform_check"
        _platform_rc=$?
    else
        _ac_doctor_warn "platform checker not found ($_platform_check)" "skipping dependency checks"
    fi

    unset -f _ac_doctor_pass _ac_doctor_warn _ac_doctor_fail 2>/dev/null

    # --- Verdict ------------------------------------------------------------
    printf '\n'
    if (( fails == 0 && _platform_rc == 0 )); then
        printf '%s✓ Healthy%s' "$ok" "$rst"
        (( warns > 0 )) && printf ' %s(%d warning%s)%s' "$warn" "$warns" \
            "$([[ $warns -eq 1 ]] && echo '' || echo 's')" "$rst"
        printf '\n'
        return 0
    else
        printf '%s✗ Problems found%s' "$err" "$rst"
        (( fails > 0 )) && printf ' %s(%d launcher check%s failed)%s' "$err" "$fails" \
            "$([[ $fails -eq 1 ]] && echo '' || echo 's')" "$rst"
        (( _platform_rc != 0 )) && printf ' %s(platform check failed)%s' "$err" "$rst"
        printf '\n'
        return 1
    fi
}

# Plain listing, one project per line, suitable for scripts and completion.
_ac_list_projects() {
    _ac_collect_projects
    local dir base disp
    for dir in "${_AC_PROJECT_DIRS[@]+"${_AC_PROJECT_DIRS[@]}"}"; do
        base="${dir##*/}"
        disp="$(_ac_project_label "$dir")"
        if [[ "$disp" == "$base" ]]; then
            printf '%s\n' "$base"
        else
            printf '%s\t%s\n' "$base" "$disp"
        fi
    done
    return 0
}

# Launch directly by name, without showing the menu.
_ac_launch_by_name() {
    local query="${1:-}"
    local resume="${2:-}"

    if [[ ! -d "$_AC_PROJECTS_DIR" ]]; then
        printf 'ac: projects directory not found: %s\n' "$_AC_PROJECTS_DIR" >&2
        return 1
    fi

    _ac_resolve_project "$query"
    local rc=$?

    if (( rc == 1 )); then
        printf "ac: no project matching '%s'\n" "$query" >&2
        printf "    run 'ac list' to see what is available\n" >&2
        return 1
    fi
    if (( rc == 2 )); then
        printf "ac: '%s' matches more than one project:\n" "$query" >&2
        local d
        for d in "${_AC_RESOLVED_MATCHES[@]}"; do
            printf '      %s\n' "$(basename "$d")" >&2
        done
        return 1
    fi

    _ac_launch_project "$_AC_RESOLVED_DIR" "$resume"
    return $?
}

# Render the full interactive menu (header, project list, footer) to stdout.
# Extracted out of ac()'s ~150-line inline render block so the two concerns
# -- "what does the menu look like" and "how do we dispatch a keypress" --
# aren't interleaved in one function.
#
# This is NOT a standalone, independently-callable renderer: like several
# other helpers in this file (e.g. _ac_doctor_pass inside _ac_doctor), it
# relies on bash's dynamic scoping of `local` variables to read the caller's
# in-progress state directly rather than taking it as parameters --
# specifically `dirs` (the filtered project list), `_ac_filter`, and
# `_ac_label_cache` (all declared `local` in ac()'s while loop before this is
# called). It must only ever be called from inside that loop.
#
# Writes to the caller's `index` (declared `local` in ac() before the call,
# left undeclared here so bash's dynamic scoping resolves it to that
# variable rather than shadowing it) -- one past the last valid selection
# number, needed by ac()'s bounds checks after the prompt returns.
_ac_render_menu() {
    # Colors
    local reset=$'\e[0m'
    local c_title=$'\e[1;36m'
    local c_dim=$'\e[2m'
    local c_num=$'\e[1;33m'
    local c_name=$'\e[0;37m'
    local c_prompt=$'\e[1;32m'
    local c_accent=$'\e[0;36m'
    local c_update=$'\e[1;33m'

    _ac_clear

    # Display menu
    printf "\n"
    printf "  %s╭─────────────────────────────────────╮%s\n" "$c_title" "$reset"
    local _ac_hdr="anycli-launcher"
    local _ac_ver="v$_AC_VERSION"
    local _ac_pad=$(( 37 - 2 - ${#_ac_hdr} - 2 - ${#_ac_ver} ))
    printf "  %s│%s  %s%*s%s%s%s  %s│%s\n" "$c_title" "$reset" "$_ac_hdr" "$_ac_pad" "" "$c_dim" "$_ac_ver" "$reset" "$c_title" "$reset"
    # Show CLI tool version on second line
    if [[ -z "${_AC_CLI_VERSION_CACHED:-}" ]]; then
        _AC_CLI_VERSION_CACHED="$($_AC_CLI_TOOL --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || echo "")"
    fi
    if [[ -n "$_AC_CLI_VERSION_CACHED" ]]; then
        local _cli_label="$_AC_CLI_TOOL $_AC_CLI_VERSION_CACHED"
        local _cli_pad=$(( 37 - 2 - ${#_cli_label} - 2 ))
        printf "  %s│%s  %s%s%*s%s  %s│%s\n" \
            "$c_title" "$reset" "$c_dim" "$_cli_label" "$_cli_pad" "" "$reset" "$c_title" "$reset"
    fi
    if _ac_update_available; then
        local _ac_update_msg=""
        if [[ "${_AC_CLI_UPDATE_AVAILABLE:-0}" == "1" ]]; then
            if [[ -n "$_AC_CLI_LATEST_VERSION" ]]; then
                _ac_update_msg="$_AC_CLI_TOOL ${_AC_CLI_LATEST_VERSION}"
            else
                _ac_update_msg="$_AC_CLI_TOOL"
            fi
        fi
        if [[ "${_AC_LAUNCHER_UPDATE_AVAILABLE:-0}" == "1" ]]; then
            if [[ -n "$_ac_update_msg" ]]; then
                _ac_update_msg="${_ac_update_msg} + launcher"
            else
                _ac_update_msg="launcher"
            fi
        fi
        if [[ "${_AC_APPS_UPDATE_AVAILABLE:-0}" == "1" ]]; then
            local _app_count
            _app_count=$(wc -w <<< "$_AC_APPS_UPDATE_LIST")
            if [[ -n "$_ac_update_msg" ]]; then
                _ac_update_msg="${_ac_update_msg} + ${_app_count} app$( (( _app_count > 1 )) && echo "s" || echo "")"
            else
                _ac_update_msg="${_app_count} app$( (( _app_count > 1 )) && echo "s" || echo "")"
            fi
        fi
        local _update_label="⬆ ${_ac_update_msg} available"
        local _upd_pad=$(( 37 - 2 - ${#_update_label} - 2 ))
        printf "  %s│%s  %s%s%*s%s  %s│%s\n" \
            "$c_title" "$reset" "$c_update" "$_update_label" "$_upd_pad" "" "$reset" "$c_title" "$reset"
    fi
    printf "  %s╰─────────────────────────────────────╯%s\n" "$c_title" "$reset"

    printf "\n"

    index=1

    # Entry 1: terminal (cd only)
    printf "   %s%2d%s  %s● %s%s\n" "$c_num" "$index" "$reset" "$c_accent" "terminal" "$reset"
    ((index++))

    # Entry 2: root (cd + kiro)
    local _root_base="${_AC_PROJECTS_DIR%/}"
    printf "   %s%2d%s  %s● %s (root)%s\n" "$c_num" "$index" "$reset" "$c_accent" "${_root_base##*/}" "$reset"
    ((index++))

    # Entry 3 (only when this CLI has a known config folder): cd + launch,
    # same shape as "root" above. _ac_config_dir_resolved is set by the
    # caller (ac()) before it invokes this function. Many config folders
    # (~/.claude, ~/.kiro, dotfiles-managed or not) are themselves git repos
    # people track, so it gets the same git-status suffix as a project entry
    # -- otherwise edits sitting there uncommitted are easy to forget about.
    if [[ -n "${_ac_config_dir_resolved:-}" ]]; then
        local _cfg_base="${_ac_config_dir_resolved%/}"
        local _cfg_git_suffix=""
        if [[ "${_AC_SHOW_GIT_STATUS:-false}" == "true" ]]; then
            local _cfg_git_info
            _cfg_git_info="$(_ac_git_summary "$_ac_config_dir_resolved")"
            [[ -n "$_cfg_git_info" ]] && _cfg_git_suffix="$(printf "  %s%s%s" "$c_dim" "$_cfg_git_info" "$reset")"
        fi
        printf "   %s%2d%s  %s● %s (config)%s%s\n" "$c_num" "$index" "$reset" "$c_accent" "${_cfg_base##*/}" "$reset" "$_cfg_git_suffix"
        ((index++))
    fi

    if [[ -n "$_ac_filter" ]]; then
        printf "   %s%s%s  %sfilter: %s%s  %s(/ to clear)%s\n" \
            "$c_dim" "──" "$reset" "$c_accent" "$_ac_filter" "$reset" "$c_dim" "$reset"
        if (( ${#dirs[@]} == 0 )); then
            printf "   %sno projects match%s\n" "$c_dim" "$reset"
        fi
    fi

    # Entry 3+: projects
    local dir
    for dir in "${dirs[@]+"${dirs[@]}"}"; do
        local _dirname="${dir##*/}"
        local _safe_dirname
        _safe_dirname="$(_ac_safe_name "$_dirname")"
        local _display_name="${_ac_label_cache[$dir]-}"
        [[ -n "${_ac_label_cache[$dir]+set}" ]] || _display_name="$(_ac_project_label "$dir")"
        local _git_info=""
        if [[ "${_AC_SHOW_GIT_STATUS:-false}" == "true" ]]; then
            _git_info="$(_ac_git_summary "$dir")"
        fi
        local _app_ver="" _app_has_update=""
        # Check if this project is a tracked app and get its version
        local _app_name _app_path
        for _app_name in "${_AC_TRACKED_APP_NAMES[@]+"${_AC_TRACKED_APP_NAMES[@]}"}"; do
            _app_path="$(_ac_app_field "$_app_name" path)"
            if [[ -n "$_app_path" && ( "$_app_path" == "$dir" || "$_app_path" == "${dir%/}" ) ]]; then
                _app_ver=$(_ac_app_version "$_app_name")
                # Check if this app has an update available
                if [[ " ${_AC_APPS_UPDATE_LIST:-} " == *" $_app_name "* ]]; then
                    _app_has_update=1
                fi
                break
            fi
        done
        local _git_suffix=""
        [[ -n "$_git_info" ]] && _git_suffix="$(printf "  %s%s%s" "$c_dim" "$_git_info" "$reset")"
        # Session suffix: last-session marker + title + age, opt-in and cached.
        local _session_suffix=""
        if [[ "${_AC_SHOW_SESSIONS:-false}" == "true" ]]; then
            local _sess_info
            _sess_info="$(_ac_session_suffix "$dir")"
            [[ -n "$_sess_info" ]] && _session_suffix="$(printf "  %s%s%s" "$c_dim" "$_sess_info" "$reset")"
        fi
        if [[ -n "$_app_ver" && -n "$_app_has_update" ]]; then
            printf "   %s%2d%s  %s%s%s  %sv%s%s  %s⬆%s%s%s\n" "$c_num" "$index" "$reset" "$c_name" "$_display_name" "$reset" "$c_dim" "$_app_ver" "$reset" "$c_update" "$reset" "$_git_suffix" "$_session_suffix"
        elif [[ -n "$_app_ver" ]]; then
            printf "   %s%2d%s  %s%s%s  %sv%s%s%s%s\n" "$c_num" "$index" "$reset" "$c_name" "$_display_name" "$reset" "$c_dim" "$_app_ver" "$reset" "$_git_suffix" "$_session_suffix"
        else
            printf "   %s%2d%s  %s%s%s%s%s\n" "$c_num" "$index" "$reset" "$c_name" "$_display_name" "$reset" "$_git_suffix" "$_session_suffix"
        fi
        ((index++))
    done

    # Footer options
    printf "\n"
    # A "?" help key is offered only when the configured CLI supports the
    # session triggers, so the help screen always has something to show.
    local _help_key=""
    if [[ -n "$(_ac_cli_resume_flag "$_AC_CLI_TOOL")" || -n "$(_ac_cli_picker_flag "$_AC_CLI_TOOL")" ]]; then
        _help_key="$(printf "   %s ?%s  help" "$c_num" "$reset")"
    fi
    if _ac_update_available; then
        printf "   %s c%s  config    %s u%s  update ⬆   %s v%s  changelog   %s n%s  new   %s q%s  quit%s\n" \
            "$c_num" "$reset" "$c_num" "$reset" "$c_num" "$reset" "$c_num" "$reset" "$c_num" "$reset" "$_help_key"
    else
        printf "   %s c%s  config    %s v%s  changelog   %s n%s  new   %s q%s  quit%s  %s(type to filter)%s\n" \
            "$c_num" "$reset" "$c_num" "$reset" "$c_num" "$reset" "$c_num" "$reset" "$_help_key" "$c_dim" "$reset"
    fi
    # Session triggers (r<N> resume, p<N> pick) are documented behind the
    # "?" help key rather than shown inline, to keep the menu uncluttered.

    printf "\n  %s›%s " "$c_prompt" "$reset"
}

ac() {
    # zsh compatibility: scope bash-flavored option semantics (0-based arrays,
    # bash-style $BASH_REMATCH capture indexing, etc.) to this call tree only.
    # `-L` makes it local to this function and everything it calls, and it
    # reverts automatically on return — the caller's interactive shell (their
    # prompt, plugins, everything else) is never touched. No-op under real
    # bash, where `emulate` doesn't exist as a builtin.
    [[ -n "${ZSH_VERSION:-}" ]] && emulate -L bash -o bash_rematch

    # --- Non-interactive forms ---
    if (( $# > 0 )); then
        _ac_load_config

        # Detect a resume/picker flag anywhere in the arguments and strip it, so
        # "ac <name> --resume", "ac -r <name>", and "ac <name> --resume-picker"
        # all work. The remaining args are re-examined by the dispatch below.
        # _ac_resume carries the mode: "picker" or "1" (most recent).
        local _ac_resume=""
        local -a _ac_args=()
        local _a
        for _a in "$@"; do
            case "$_a" in
                --resume-picker) _ac_resume="picker" ;;
                --resume|-r) _ac_resume="1" ;;
                *) _ac_args+=("$_a") ;;
            esac
        done
        set -- "${_ac_args[@]+"${_ac_args[@]}"}"

        # "ac resume <name>" — explicit resume subcommand. A project literally
        # named "resume" takes precedence (mirrors help/config/list handling).
        if [[ "${1:-}" == "resume" ]] && ! _ac_project_exists_exact "resume"; then
            shift
            if [[ -z "${1:-}" ]]; then
                printf 'ac: resume needs a project name (usage: ac resume <name>)\n' >&2
                return 2
            fi
            _ac_launch_by_name "$1" "1"
            return $?
        fi

        # "ac pick <name>" — explicit session-picker subcommand. A project named
        # "pick" takes precedence.
        if [[ "${1:-}" == "pick" ]] && ! _ac_project_exists_exact "pick"; then
            shift
            if [[ -z "${1:-}" ]]; then
                printf 'ac: pick needs a project name (usage: ac pick <name>)\n' >&2
                return 2
            fi
            _ac_launch_by_name "$1" "picker"
            return $?
        fi

        # If a resume/picker flag was given but no positional args remain, there
        # is nothing to launch.
        if [[ -n "$_ac_resume" && $# -eq 0 ]]; then
            local _flag_name="--resume"
            [[ "$_ac_resume" == "picker" ]] && _flag_name="--resume-picker"
            printf 'ac: %s needs a project name\n' "$_flag_name" >&2
            return 2
        fi

        case "${1:-}" in
            -h|--help)    _ac_usage; return 0 ;;
            -V|--version) printf 'anycli-launcher %s\n' "$_AC_VERSION"; return 0 ;;
            -l|--list)    _ac_list_projects; return 0 ;;
            --)           shift; _ac_launch_by_name "${1:-}" "$_ac_resume"; return $? ;;
            -*)
                printf 'ac: unknown option: %s\n' "$1" >&2
                _ac_usage >&2
                return 2
                ;;
            help|version|list|config|doctor|setup)
                # A project of the same name takes precedence over the subcommand
                if _ac_project_exists_exact "$1"; then
                    _ac_launch_by_name "$1"
                    return $?
                fi
                case "$1" in
                    help)    _ac_usage ;;
                    version) printf 'anycli-launcher %s\n' "$_AC_VERSION" ;;
                    list)    _ac_list_projects ;;
                    config)  _ac_config_menu ;;
                    doctor)  _ac_doctor ;;
                    setup)   _ac_setup ;;
                esac
                return $?
                ;;
            *)
                _ac_launch_by_name "$1" "$_ac_resume"
                return $?
                ;;
        esac
    fi

    # First-run guided setup (issue #14). When there is no config file yet and
    # we are attached to an interactive terminal, walk the user through a short
    # setup instead of erroring out with "projects directory not found". Skipped
    # when: a config already exists, a legacy config is present (it will be
    # migrated), stdin is not a TTY (piped/CI — keep the old error path so
    # scripts and tests behave), or AC_PROJECTS_DIR is already exported to a
    # valid directory. After the wizard writes the config, the loop below reads
    # it via _ac_load_config and renders the menu in the same invocation.
    local _ac_legacy_config="${HOME}/.config/anycli-launcher/config"
    if [[ ! -f "$_AC_CONFIG_FILE" && ! -f "$_ac_legacy_config" && -t 0 ]] \
        && [[ -z "${AC_PROJECTS_DIR:-}" || ! -d "${AC_PROJECTS_DIR:-}" ]]; then
        _ac_first_run_wizard || return 1
    fi

    # Run update check on first iteration only (time-gated internally)
    local _ac_checked_updates=0
    # Active name filter, so long project lists stay navigable
    local _ac_filter=""
    local _ac_cfg_mtime="" _ac_cfg_loaded=0 _ac_cfg_mtime_now

    while true; do
        # Skip the reload (full YAML re-parse + legacy migration scan) when
        # the config file hasn't changed since last read. Without this, every
        # keystroke that returns to this loop -- most commonly typing a
        # filter character -- re-parsed the entire config from scratch even
        # though nothing on disk changed. A submenu that does change the
        # config (e.g. the config menu) always writes the file, which bumps
        # its mtime and triggers a reload here on return, so in-memory state
        # never goes stale.
        _ac_cfg_mtime_now="$(_ac_file_mtime "$_AC_CONFIG_FILE")"
        if (( ! _ac_cfg_loaded )) || [[ "$_ac_cfg_mtime_now" != "$_ac_cfg_mtime" ]]; then
            _ac_load_config
            _ac_cfg_mtime="$_ac_cfg_mtime_now"
            _ac_cfg_loaded=1
        fi

        if [[ ! -d "$_AC_PROJECTS_DIR" ]]; then
            echo "Error: Projects directory not found: $_AC_PROJECTS_DIR"
            echo "Set AC_PROJECTS_DIR or configure ~/.config/anycli-launcher/config.yaml"
            return 1
        fi

        # Run update check once per ac session (the function handles 24h time-gating)
        if [[ "$_ac_checked_updates" == "0" ]]; then
            _ac_update_check
            _ac_checked_updates=1
        fi

        # Build project list (shared with the non-interactive path)
        _ac_collect_projects
        local -a all_dirs=("${_AC_PROJECT_DIRS[@]+"${_AC_PROJECT_DIRS[@]}"}")
        local -a dirs=()
        # Populated while filtering below, so the render loop further down
        # doesn't call _ac_project_label a second time per project (it's not
        # free -- it can call _ac_lookup_alias, which itself scans every
        # configured alias on a miss). Left empty when there's no filter, in
        # which case the render loop's fallback computes it directly, same
        # as before -- one call either way, never two.
        local -A _ac_label_cache=()
        if [[ -z "$_ac_filter" ]]; then
            dirs=("${all_dirs[@]+"${all_dirs[@]}"}")
        else
            local _f_lower="$(_ac_lower "$_ac_filter")" _f_dir _f_base _f_disp
            for _f_dir in "${all_dirs[@]+"${all_dirs[@]}"}"; do
                _f_base="${_f_dir##*/}"
                _f_disp="$(_ac_project_label "$_f_dir")"
                _ac_label_cache["$_f_dir"]="$_f_disp"
                if [[ "$(_ac_lower "$_f_base")" == *"$_f_lower"* || "$(_ac_lower "$_f_disp")" == *"$_f_lower"* ]]; then
                    dirs+=("$_f_dir")
                fi
            done
        fi

        local index
        # Declared here (not just inside _ac_render_menu) because the
        # timeout message below still needs them after the render call
        # returns and its own copies of these have gone out of scope.
        local reset=$'\e[0m' c_dim=$'\e[2m'
        # Config folder shows up as entry 3 only when one is known for the
        # current CLI (hardcoded default or a config-menu override), pushing
        # projects to start one slot later. _ac_proj_offset is how many
        # header entries (terminal/root[/config]) come before entry 1 in
        # "dirs", used below and by the resume/picker key handlers instead of
        # a hardcoded 3.
        local _ac_config_dir_resolved
        _ac_config_dir_resolved="$(_ac_resolved_config_dir)"
        # Only offer the shortcut for a folder that actually exists -- a
        # hardcoded/overridden path for a CLI that was never run here (or
        # whose config lives elsewhere on this machine) would otherwise be a
        # dead entry.
        [[ -n "$_ac_config_dir_resolved" && -d "$_ac_config_dir_resolved" ]] || _ac_config_dir_resolved=""
        local _ac_proj_offset=3
        [[ -n "$_ac_config_dir_resolved" ]] && _ac_proj_offset=4
        _ac_render_menu

        # Read input, with an idle timeout
        local key read_rc=0
        read -r -t "$_AC_MENU_TIMEOUT" key || read_rc=$?
        if (( read_rc > 128 )); then
            # Timed out. Say so rather than vanishing, which was
            # indistinguishable from a crash.
            printf "\n  %sNo input for %ss — exiting. Run ac again when ready.%s\n" \
                "$c_dim" "$_AC_MENU_TIMEOUT" "$reset"
            return 0
        elif (( read_rc != 0 )); then
            # EOF (pipe closed or Ctrl-D)
            return "$read_rc"
        fi

        # Quit
        if [[ "$key" == "q" || "$key" == "Q" ]]; then
            return
        fi

        # Config shortcut
        if [[ "$key" == "c" || "$key" == "C" ]]; then
            _ac_config_menu
            continue
        fi

        # Update
        if [[ "$key" == "u" || "$key" == "U" ]]; then
            _ac_do_update
            continue
        fi

        # Version/changelog
        if [[ "$key" == "v" || "$key" == "V" ]]; then
            _ac_show_changelog
            continue
        fi

        # Key help (session triggers etc.)
        if [[ "$key" == "?" ]]; then
            _ac_show_key_help
            continue
        fi

        # New project: create a folder (+ git repo) under the projects root
        # and optionally configure it, then fall back into the menu, which
        # re-scans the root every iteration so the new entry appears.
        if [[ "$key" == "n" || "$key" == "N" ]]; then
            _ac_new_project
            continue
        fi

        # "/" clears the filter; "/text" sets it explicitly
        if [[ "$key" == "/" ]]; then
            _ac_filter=""
            continue
        fi
        if [[ "$key" == /* ]]; then
            _ac_filter="${key#/}"
            continue
        fi

        # Resume launch: "r<N>" launches project N, continuing its most recent
        # session (only meaningful for projects, i.e. index >= _ac_proj_offset).
        # Advertised in the legend when the configured CLI supports resume.
        if [[ "$key" =~ ^[rR]([0-9]+)$ ]]; then
            local rkey="${BASH_REMATCH[1]}"
            if (( rkey >= _ac_proj_offset && rkey < index )); then
                local rselected="${dirs[$((rkey - _ac_proj_offset))]}"
                _ac_launch_project "$rselected" "1"
                if [[ -z "${_AC_LAUNCHED_NEW_TAB:-}" ]]; then
                    return
                fi
                unset _AC_LAUNCHED_NEW_TAB
                read -r -t 0.1 -n 10000 _ 2>/dev/null || true
            fi
            continue
        fi

        # Session picker: "p<N>" launches project N and lets the CLI present its
        # interactive session picker. Advertised only when the CLI supports it.
        if [[ "$key" =~ ^[pP]([0-9]+)$ ]]; then
            local pkey="${BASH_REMATCH[1]}"
            if (( pkey >= _ac_proj_offset && pkey < index )); then
                local pselected="${dirs[$((pkey - _ac_proj_offset))]}"
                _ac_launch_project "$pselected" "picker"
                if [[ -z "${_AC_LAUNCHED_NEW_TAB:-}" ]]; then
                    return
                fi
                unset _AC_LAUNCHED_NEW_TAB
                read -r -t 0.1 -n 10000 _ 2>/dev/null || true
            fi
            continue
        fi

        # Any other non-numeric input is treated as a name filter
        if [[ -n "$key" && ! "$key" =~ ^[0-9]+$ ]]; then
            _ac_filter="$key"
            continue
        fi

        # Validate
        if [[ ! "$key" =~ ^[0-9]+$ ]] || ((key < 1 || key >= index )); then
            continue
        fi

        # Handle selection — break out of loop to execute
        if ((key == 1 )); then
            cd "$_AC_PROJECTS_DIR" || return 1
            return
        elif ((key == 2 )); then
            cd "$_AC_PROJECTS_DIR" || return 1
            local flags
            local _root_base="${_AC_PROJECTS_DIR%/}"
            flags=$(_ac_get_flags "${_root_base##*/}")
            _ac_run_cli "$flags"
            return
        elif (( _ac_proj_offset == 4 && key == 3 )); then
            cd "$_ac_config_dir_resolved" || return 1
            local flags
            local _cfg_base="${_ac_config_dir_resolved%/}"
            flags=$(_ac_get_flags "${_cfg_base##*/}")
            _ac_run_cli "$flags"
            return
        else
            local selected="${dirs[$((key - _ac_proj_offset))]}"
            _ac_launch_project "$selected"
            # If launched in a new tab, keep menu open; otherwise we already cd'd
            if [[ -z "${_AC_LAUNCHED_NEW_TAB:-}" ]]; then
                return
            fi
            unset _AC_LAUNCHED_NEW_TAB
            # Flush any buffered input (focus events from tab switch)
            read -r -t 0.1 -n 10000 _ 2>/dev/null || true
            continue
        fi
    done
}
