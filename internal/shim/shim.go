// Package shim prints the shell integration installed with
// `eval "$(ac init bash)"`. The shim defines an `ac` function that runs the
// binary with --cd-file <tmp> and cds to whatever path the binary wrote
// there; the binary never needs to change its parent's cwd.
package shim

import (
	"fmt"
	"sort"
)

const posix = `# anycli-launcher shell integration (%[1]s). Install: eval "$(ac init %[1]s)"
ac() {
  local __ac_tmp __ac_rc __ac_dir
  __ac_tmp="$(mktemp "${TMPDIR:-/tmp}/ac-cd.XXXXXX")" || return 1
  command ac --cd-file "$__ac_tmp" "$@"
  __ac_rc=$?
  if [ -s "$__ac_tmp" ]; then
    __ac_dir="$(cat -- "$__ac_tmp")"
    if command -v cygpath >/dev/null 2>&1; then
      __ac_dir="$(cygpath -u -- "$__ac_dir")"
    fi
    [ -d "$__ac_dir" ] && cd -- "$__ac_dir"
  fi
  rm -f -- "$__ac_tmp"
  return $__ac_rc
}
`

const fish = `# anycli-launcher shell integration (fish). Install: ac init fish | source
function ac
    set -l __ac_tmp (mktemp)
    or return 1
    command ac --cd-file $__ac_tmp $argv
    set -l __ac_rc $status
    if test -s $__ac_tmp
        set -l __ac_dir (cat $__ac_tmp)
        if test -d "$__ac_dir"
            cd $__ac_dir
        end
    end
    rm -f $__ac_tmp
    return $__ac_rc
end
`

// Windows PowerShell ships a built-in "ac" alias (Add-Content) that would
// shadow the function, so it is removed first.
const pwsh = `# anycli-launcher shell integration (pwsh). Install: Invoke-Expression (& ac init pwsh | Out-String)
Remove-Item -Path Alias:ac -Force -ErrorAction SilentlyContinue
function ac {
    $__acExe = Get-Command -Name ac -CommandType Application -ErrorAction Stop | Select-Object -First 1
    $__acTmp = [System.IO.Path]::GetTempFileName()
    try {
        & $__acExe.Source --cd-file $__acTmp @args
        $__acRc = $LASTEXITCODE
        $__acDir = Get-Content -LiteralPath $__acTmp -Raw -ErrorAction SilentlyContinue
        if ($__acDir) {
            $__acDir = $__acDir.Trim()
            if (Test-Path -LiteralPath $__acDir -PathType Container) { Set-Location -LiteralPath $__acDir }
        }
    } finally {
        Remove-Item -LiteralPath $__acTmp -Force -ErrorAction SilentlyContinue
    }
    $global:LASTEXITCODE = $__acRc
}
`

var scripts = map[string]string{
	"bash": fmt.Sprintf(posix, "bash"),
	"zsh":  fmt.Sprintf(posix, "zsh"),
	"fish": fish,
	"pwsh": pwsh,
}

// Script returns the integration for shell.
func Script(shell string) (string, error) {
	if shell == "powershell" {
		shell = "pwsh"
	}
	s, ok := scripts[shell]
	if !ok {
		return "", fmt.Errorf("unsupported shell %q (want one of %v)", shell, Shells())
	}
	return s, nil
}

// Shells lists supported shell names.
func Shells() []string {
	var out []string
	for k := range scripts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
