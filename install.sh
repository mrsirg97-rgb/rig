#!/bin/sh
# rig installer (specs/SPEC_BUILD.md 5): fetch the release binary for this
# platform, verify it, land it in ${RIG_BIN:-$HOME/.local/bin} - never sudo.
# Every failure names what it was doing.
set -eu

repo="mrsirg97-rgb/rig"

version=""
if [ -n "${RIG_VERSION:-}" ]; then
  version="$RIG_VERSION"
elif [ "$#" -gt 0 ]; then
  version="$1"
fi
if [ -z "$version" ]; then
  latest="https://github.com/$repo/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    version=$(curl -fsSL -L -o /dev/null -w '%{url_effective}' "$latest" | sed 's|.*/tag/||')
  elif command -v wget >/dev/null 2>&1; then
    version=$(wget -q -S -O /dev/null "$latest" 2>&1 | awk -F'[ /]' '/Location:/ { v=$NF } END { print v }')
  else
    echo "install: no curl or wget to resolve the latest release" >&2
    exit 1
  fi
  [ -n "$version" ] || { echo "install: could not resolve the latest release" >&2; exit 1; }
fi
case "$version" in v*) tag="$version" ;; *) tag="v$version" ;; esac
os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "install: unknown OS '$os' (Linux/Darwin only)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "install: unknown architecture '$arch'" >&2; exit 1 ;;
esac
asset="rig_${os}_${arch}"
base="https://github.com/$repo/releases/download/$tag"
tmp="$(mktemp -d)" || { echo "install: mktemp failed" >&2; exit 1; }
trap 'rm -rf "$tmp"' EXIT
down() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    echo "install: no curl or wget to download $1" >&2
    return 1
  fi
}

down "$base/checksums.txt" "$tmp/checksums.txt" || { echo "install: failed to download checksums.txt" >&2; exit 1; }
down "$base/$asset" "$tmp/$asset" || { echo "install: failed to download $asset" >&2; exit 1; }

# verify BEFORE anything moves.
sum=$(awk -v a="$asset" '$0 ~ a { print $1 }' "$tmp/checksums.txt")
[ -n "$sum" ] || { echo "install: checksums.txt has no line for $asset" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
else
  echo "install: no sha256sum or shasum to verify the download" >&2
  exit 1
fi
[ "$got" = "$sum" ] || { echo "install: checksum mismatch for $asset ($got != $sum)" >&2; exit 1; }

bindir="${RIG_BIN:-$HOME/.local/bin}"
mkdir -p "$bindir" || { echo "install: mkdir -p $bindir failed" >&2; exit 1; }
install -m 0755 "$tmp/$asset" "$bindir/rig" || { echo "install: install into $bindir failed" >&2; exit 1; }

case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "install: $bindir is not on your PATH; add it:"; echo "  export PATH=\"\$PATH:$bindir\"" ;;
esac
"$bindir/rig" -version
