# install.ps1: install ac (anycli-launcher v4) on Windows from a GitHub release.
#
#   irm https://github.com/ericfaris/anycli-launcher/releases/latest/download/install.ps1 | iex
#   & ([scriptblock]::Create((irm https://github.com/ericfaris/anycli-launcher/releases/latest/download/install.ps1))) -Version v4.0.0 -Setup
#
# Downloads the zip for this architecture and checksums.txt, refuses to
# install unless the zip's SHA256 matches, puts ac.exe in
# %LOCALAPPDATA%\Programs\anycli-launcher (or $env:AC_INSTALL_DIR / -Dir), and
# prints the line to add to $PROFILE. Your profile and user PATH are only
# changed with -Setup.
#
# Environment (for `irm | iex`, which can't pass parameters): AC_VERSION,
# AC_INSTALL_DIR, AC_RELEASES_URL (download base, default
# https://github.com/ericfaris/anycli-launcher/releases).
[CmdletBinding()]
param(
    [string]$Version = $env:AC_VERSION,
    [string]$Dir = $env:AC_INSTALL_DIR,
    # Profile that -Setup writes to (default: $PROFILE of this PowerShell).
    [string]$ProfilePath,
    [switch]$Setup
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is slow with a progress bar on 5.1

# throw, not exit: under `irm | iex` exit would close the user's session.
# Run as a file (pwsh -File install.ps1), an uncaught throw exits 1.
function Fail([string]$msg) {
    throw "install.ps1: $msg"
}

$onWindows = ($PSVersionTable.PSVersion.Major -le 5) -or $IsWindows
if (-not $onWindows) { Fail 'this installer is for Windows; on Linux/macOS use install.sh' }

# Windows PowerShell 5.1 may default to TLS 1.0.
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

$arch = $env:PROCESSOR_ARCHITEW6432
if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
switch ($arch) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Fail "unsupported architecture: $arch" }
}
$asset = "anycli-launcher_windows_$arch.zip"

$releases = $env:AC_RELEASES_URL
if (-not $releases) { $releases = 'https://github.com/ericfaris/anycli-launcher/releases' }
$releases = $releases.TrimEnd('/')
if (-not $Version -or $Version -eq 'latest') {
    $base = "$releases/latest/download"
    $label = 'latest'
} else {
    if (-not $Version.StartsWith('v')) { $Version = "v$Version" }
    $base = "$releases/download/$Version"
    $label = $Version
}
if (-not $Dir) {
    if (-not $env:LOCALAPPDATA) { Fail 'LOCALAPPDATA is not set; pass -Dir' }
    $Dir = Join-Path $env:LOCALAPPDATA 'Programs\anycli-launcher'
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("ac-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Output "Downloading $asset ($label)"
    $zip = Join-Path $tmp $asset
    $sums = Join-Path $tmp 'checksums.txt'
    foreach ($f in @(@("$base/$asset", $zip), @("$base/checksums.txt", $sums))) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri $f[0] -OutFile $f[1]
        } catch {
            Fail "download failed: $($f[0]) ($($_.Exception.Message))"
        }
    }

    $want = $null
    foreach ($l in Get-Content -LiteralPath $sums) {
        $parts = $l.Trim() -split '\s+', 2
        if ($parts.Count -eq 2 -and ($parts[1] -eq $asset -or $parts[1] -eq "*$asset")) {
            $want = $parts[0].ToLowerInvariant()
            break
        }
    }
    if (-not $want) { Fail "$asset is not listed in checksums.txt; nothing installed" }
    $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    if ($got -ne $want) { Fail "checksum mismatch for $asset (expected $want, got $got); nothing installed" }
    Write-Output "Checksum OK (sha256 $got)"

    $x = Join-Path $tmp 'x'
    Expand-Archive -LiteralPath $zip -DestinationPath $x
    $src = Join-Path $x 'ac.exe'
    if (-not (Test-Path -LiteralPath $src -PathType Leaf)) { Fail "ac.exe not found in $asset" }

    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    $dest = Join-Path $Dir 'ac.exe'
    $part = Join-Path $Dir ".ac.exe.tmp.$PID"
    Copy-Item -LiteralPath $src -Destination $part -Force
    try {
        Move-Item -LiteralPath $part -Destination $dest -Force
    } catch {
        Remove-Item -LiteralPath $part -Force -ErrorAction SilentlyContinue
        Fail "cannot write $dest (is ac running?): $($_.Exception.Message)"
    }
    Write-Output "Installed $dest"
} finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

# Not plain `ac`: PowerShell has a built-in `ac` alias (Add-Content).
$line = 'Invoke-Expression (& (Get-Command ac -CommandType Application | Select-Object -First 1).Source init pwsh | Out-String)'
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$norm = { param($p) $p.TrimEnd('\').ToLowerInvariant() }
$onPath = @(($userPath -split ';') + ($env:Path -split ';') | Where-Object { $_ } | ForEach-Object { & $norm $_ }) -contains (& $norm $Dir)
$profilePath = $ProfilePath
if (-not $profilePath) { $profilePath = $PROFILE }
if (-not $profilePath) { $profilePath = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'PowerShell\Microsoft.PowerShell_profile.ps1' }

if ($Setup) {
    if (-not $onPath) {
        $new = if ($userPath) { "$Dir;$userPath" } else { $Dir }
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
        Write-Output "Added $Dir to your user PATH"
    }
    $profileDir = Split-Path -Parent $profilePath
    if (-not (Test-Path -LiteralPath $profileDir)) { New-Item -ItemType Directory -Force -Path $profileDir | Out-Null }
    $has = (Test-Path -LiteralPath $profilePath) -and (Select-String -LiteralPath $profilePath -SimpleMatch -Quiet -Pattern $line)
    if (-not $has) {
        Add-Content -LiteralPath $profilePath -Value "`n# anycli-launcher shell integration`n$line"
        Write-Output "Updated $profilePath. Open a new PowerShell, then run: ac"
    } else {
        Write-Output "$profilePath already has the ac line. Open a new PowerShell, then run: ac"
    }
    return
}

Write-Output ''
if (-not $onPath) {
    Write-Output "Add $Dir to your PATH, e.g.:"
    Write-Output "  [Environment]::SetEnvironmentVariable('Path', `"$Dir;`" + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')"
    Write-Output ''
}
Write-Output "Next, add this line to $profilePath (it lets ac cd your shell):"
Write-Output ''
Write-Output "  $line"
Write-Output ''
Write-Output 'Then open a new PowerShell and run: ac'
Write-Output '(or rerun this script with -Setup to do both for you)'
