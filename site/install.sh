#!/bin/sh
# Antifailure installer.
#
# Downloads the release for this platform, checks it against the published
# checksum, and puts the binary and the runner where af expects them.
#
# Written in POSIX sh rather than bash, because the machine somebody pipes this
# into is as likely to be an Alpine container as a laptop, and a script that
# needs bash on a machine without it fails with a syntax error rather than a
# message.
set -eu

REPO="antifailure/antifailure"
VERSION="${AF_VERSION:-latest}"
PREFIX="${AF_PREFIX:-$HOME/.antifailure}"
BIN_DIR="${AF_BIN_DIR:-$PREFIX/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'antifailure: %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required and was not found"
}

need uname
need tar
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  read_url() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  read_url() { wget -qO- "$1"; }
else
  die "curl or wget is required and neither was found"
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) die "$os is not a platform this release supports; build from source with 'go build ./engine/cmd/af'" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "$arch is not an architecture this release supports" ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(read_url "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$VERSION" ] || die "no release was found; set AF_VERSION to install a specific one"
fi
bare="${VERSION#v}"

name="antifailure_${bare}_${os}_${arch}"
base="https://github.com/$REPO/releases/download/$VERSION"

tmp=$(mktemp -d)
# Removed whether this succeeded or not. A half downloaded archive left in
# /tmp is the kind of thing somebody finds a year later and cannot explain.
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $name"
fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" || die "could not download $base/$name.tar.gz"

# The checksum is checked rather than assumed. A download over a hijacked
# network is exactly the thing a tool that runs unreviewed code should not
# shrug at.
if fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  expected=$(grep " $name.tar.gz\$" "$tmp/checksums.txt" | awk '{print $1}' | head -1)
  if [ -n "$expected" ]; then
    if command -v shasum >/dev/null 2>&1; then
      actual=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')
    elif command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')
    else
      actual=""
      say "warning: no sha256 tool was found, so the download was not verified"
    fi
    if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
      die "the download does not match its published checksum; refusing to install"
    fi
    [ -n "$actual" ] && say "Checksum verified"
  fi
else
  say "warning: no checksum file was published for $VERSION"
fi

tar -C "$tmp" -xzf "$tmp/$name.tar.gz"
mkdir -p "$BIN_DIR" "$PREFIX"
install -m 0755 "$tmp/$name/af" "$BIN_DIR/af" 2>/dev/null \
  || { cp "$tmp/$name/af" "$BIN_DIR/af" && chmod 0755 "$BIN_DIR/af"; }

# The runner travels with the binary, so af test finds it without a flag.
rm -rf "$PREFIX/runner"
cp -R "$tmp/$name/runner" "$PREFIX/runner"

say ""
say "Installed $VERSION to $BIN_DIR/af"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    say ""
    say "Add it to your PATH:"
    say "  export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

say ""
say "Next:"
say "  af doctor          check this machine"
say "  af runner install  finish the agent runner, which needs node"
say "  af init            read your repository and write a manifest"
