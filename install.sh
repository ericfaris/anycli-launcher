#!/bin/sh
# install.sh: install the ac binary (anycli-launcher v4) from a GitHub release.
#
#   curl -fsSL https://github.com/ericfaris/anycli-launcher/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version v4.0.0 --setup
#
# It downloads the archive for this OS/arch and checksums.txt, refuses to
# install unless the archive's sha256 matches, puts `ac` in ~/.local/bin (or
# $AC_INSTALL_DIR / --dir), and prints the shell-integration line to add.
# Shell rc files are only edited with --setup.
#
# Options:
#   --version <tag>   install that release (e.g. v4.0.0; default: latest)
#   --dir <dir>       install directory (default: $AC_INSTALL_DIR or ~/.local/bin)
#   --shell <name>    bash, zsh, fish or pwsh (default: from $SHELL)
#   --setup           also add the integration line (and PATH, if needed) to
#                     the shell's rc file, once
#   -h, --help        show this help
#
# Environment: AC_INSTALL_DIR, AC_VERSION, AC_RELEASES_URL (download base,
# default https://github.com/ericfaris/anycli-launcher/releases).
set -eu

REPO_RELEASES="https://github.com/ericfaris/anycli-launcher/releases"
NAME="anycli-launcher"

say() { printf '%s\n' "$*"; }
err() { printf 'install.sh: %s\n' "$*" >&2; }
die() { err "$*"; exit 1; }

usage() {
	cat <<'EOF_USAGE'
Usage: install.sh [--version <tag>] [--dir <dir>] [--shell <name>] [--setup]

Installs ac (anycli-launcher) from a GitHub release after verifying its
sha256 against the release's checksums.txt.

  --version <tag>   release to install, e.g. v4.0.0 (default: latest)
  --dir <dir>       install directory (default: $AC_INSTALL_DIR or ~/.local/bin)
  --shell <name>    bash, zsh, fish or pwsh (default: from $SHELL)
  --setup           also add the integration line (and PATH, if needed)
                    to that shell's rc file, once
  -h, --help        show this help
EOF_USAGE
}

version="${AC_VERSION:-}"
dir="${AC_INSTALL_DIR:-}"
shell_name=""
setup=0

while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || die "--version needs a tag"
		version="$2"
		shift 2
		;;
	--version=*) version="${1#*=}"; shift ;;
	--dir)
		[ $# -ge 2 ] || die "--dir needs a path"
		dir="$2"
		shift 2
		;;
	--dir=*) dir="${1#*=}"; shift ;;
	--shell)
		[ $# -ge 2 ] || die "--shell needs a name"
		shell_name="$2"
		shift 2
		;;
	--shell=*) shell_name="${1#*=}"; shift ;;
	--setup) setup=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown option: $1 (see --help)" ;;
	esac
done

# --- platform -------------------------------------------------------------

os=$(uname -s)
case "$os" in
Linux) os=linux ;;
Darwin) os=darwin ;;
MINGW* | MSYS* | CYGWIN*) os=windows ;;
*) die "unsupported OS: $os (download a release by hand: $REPO_RELEASES)" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture: $arch" ;;
esac
# An x86_64 shell under Rosetta on Apple silicon: install the native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
	[ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
	arch=arm64
fi

if [ "$os" = windows ]; then
	asset="${NAME}_${os}_${arch}.zip"
	exe="ac.exe"
else
	asset="${NAME}_${os}_${arch}.tar.gz"
	exe="ac"
fi

case "$version" in
"" | latest) base="${AC_RELEASES_URL:-$REPO_RELEASES}/latest/download" ;;
v*) base="${AC_RELEASES_URL:-$REPO_RELEASES}/download/$version" ;;
*) base="${AC_RELEASES_URL:-$REPO_RELEASES}/download/v$version" ;;
esac

[ -n "$dir" ] || {
	[ -n "${HOME:-}" ] || die "HOME is not set; pass --dir"
	dir="$HOME/.local/bin"
}

# --- tools ------------------------------------------------------------------

if command -v curl >/dev/null 2>&1; then
	if [ -n "${AC_RELEASES_URL:-}" ]; then
		fetch() { curl -fsSL -o "$2" "$1"; }
	else
		# GitHub only, and never let a redirect drop to plain http.
		fetch() { curl -fsSL --proto '=https' --proto-redir '=https' -o "$2" "$1"; }
	fi
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	die "need curl or wget"
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
elif command -v openssl >/dev/null 2>&1; then
	sha256() { openssl dgst -sha256 "$1" | awk '{print $NF}'; }
else
	die "need sha256sum, shasum or openssl to verify the download; nothing installed"
fi

# --- download + verify --------------------------------------------------------

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t ac-install)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' INT TERM

say "Downloading $asset (${version:-latest})"
fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: $base/checksums.txt"

want=$(awk -v f="$asset" '$2 == f || $2 == "*" f {print $1; exit}' "$tmp/checksums.txt")
[ -n "$want" ] || die "$asset is not listed in checksums.txt; nothing installed"
got=$(sha256 "$tmp/$asset")
if [ "$got" != "$want" ]; then
	die "checksum mismatch for $asset (expected $want, got $got); nothing installed"
fi
say "Checksum OK (sha256 $got)"

mkdir "$tmp/x"
if [ "$os" = windows ]; then
	command -v unzip >/dev/null 2>&1 || die "need unzip"
	unzip -q "$tmp/$asset" -d "$tmp/x"
else
	tar -xzf "$tmp/$asset" -C "$tmp/x"
fi
[ -f "$tmp/x/$exe" ] || die "$exe not found in $asset"

# --- install --------------------------------------------------------------------

mkdir -p "$dir" || die "cannot create $dir"
# Copy next to the target, then rename: a running ac is never half-written.
cp "$tmp/x/$exe" "$dir/.$exe.tmp.$$"
chmod 755 "$dir/.$exe.tmp.$$"
mv -f "$dir/.$exe.tmp.$$" "$dir/$exe" || {
	rm -f "$dir/.$exe.tmp.$$"
	die "cannot write $dir/$exe"
}
say "Installed $dir/$exe"
"$dir/$exe" --version 2>/dev/null | head -n 1 || true

# --- shell integration ------------------------------------------------------------

[ -n "$shell_name" ] || shell_name=$(basename "${SHELL:-sh}")
case "$shell_name" in
bash)
	rc="$HOME/.bashrc"
	# shellcheck disable=SC2016
	line='eval "$(ac init bash)"'
	path_line="export PATH=\"$dir:\$PATH\""
	;;
zsh)
	rc="${ZDOTDIR:-$HOME}/.zshrc"
	# shellcheck disable=SC2016
	line='eval "$(ac init zsh)"'
	path_line="export PATH=\"$dir:\$PATH\""
	;;
fish)
	rc="${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish"
	line='ac init fish | source'
	path_line="fish_add_path \"$dir\""
	;;
pwsh | pwsh-preview)
	rc="${XDG_CONFIG_HOME:-$HOME/.config}/powershell/Microsoft.PowerShell_profile.ps1"
	# Not plain `ac`: Windows PowerShell has an `ac` alias, macOS /usr/sbin/ac.
	line='Invoke-Expression (& (Get-Command ac -CommandType Application | Select-Object -First 1).Source init pwsh | Out-String)'
	path_line="\$env:PATH = \"$dir\" + [IO.Path]::PathSeparator + \$env:PATH"
	;;
*)
	rc=""
	line=""
	path_line=""
	;;
esac

on_path=0
case ":${PATH:-}:" in
*":$dir:"* | *":$dir/:"*) on_path=1 ;;
esac

if [ -z "$line" ]; then
	say ""
	say "Add the shell integration for your shell (it lets ac cd your shell):"
	say "  bash: eval \"\$(ac init bash)\"    zsh: eval \"\$(ac init zsh)\"    fish: ac init fish | source"
	[ "$on_path" = 1 ] || say "and put $dir on your PATH."
	[ "$setup" = 0 ] || err "--setup: unknown shell '$shell_name'; pass --shell"
	exit 0
fi

if [ "$setup" = 1 ]; then
	mkdir -p "$(dirname "$rc")"
	touch "$rc"
	added=0
	if [ "$on_path" = 0 ] && ! grep -qF "$path_line" "$rc"; then
		printf '\n# anycli-launcher: ac on PATH\n%s\n' "$path_line" >>"$rc"
		added=1
	fi
	if ! grep -qF "$line" "$rc"; then
		printf '\n# anycli-launcher shell integration\n%s\n' "$line" >>"$rc"
		added=1
	fi
	if [ "$added" = 1 ]; then
		say "Updated $rc. Open a new shell (or source it), then run: ac"
	else
		say "$rc already has the ac lines. Run: ac"
	fi
	exit 0
fi

say ""
say "Next, add to $rc (the init line lets ac cd your shell):"
say ""
[ "$on_path" = 1 ] || say "  $path_line"
say "  $line"
say ""
say "Then open a new shell and run: ac"
say "(or rerun this script with --setup to add it for you)"
