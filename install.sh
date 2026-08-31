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

# ---------------------------------------------------------------------------
# Putting af where the shell will actually find it.
#
# This block used to be two blocks that did not talk to each other. The first
# tested whether BIN_DIR was on PATH and, on the miss, printed an export line.
# The second then printed three bare `af` commands to run next, unconditionally.
# So the script knew af was unreachable and told the reader to run it three
# times anyway, and all three said "command not found". That is the first thirty
# seconds of the product.
#
# The export it printed was also session only, so a reader who did paste it lost
# af again the moment they closed the terminal, with nothing having said that
# would happen.
#
# What this deliberately does NOT do is edit a shell profile uninvited. A script
# piped into sh has no stdin left to ask consent on, and a tool that appends to
# somebody's ~/.zshrc because it felt entitled to is a tool people stop piping
# into sh. So the default is to print the two lines that fix it, the permanent
# one first, and to make every command printed after that point reachable.
# AF_ADD_TO_PATH=1 is how to say yes in advance, which is consent given rather
# than assumed. GitHub Actions is the exception: GITHUB_PATH exists precisely so
# an install step can extend PATH for the steps after it, and using it changes
# nothing outside the job.
# ---------------------------------------------------------------------------

# AF_PREFIX with no HOME set is a real combination inside a container, and the
# script has to reach the end of this block without dereferencing HOME when it
# is not there. An empty $home would also turn every "$home"/* pattern below
# into /*, which matches every absolute path, so each one is guarded.
home=${HOME:-}

# The line written into a profile names $HOME rather than the expanded path, so
# a home directory that moves does not leave a dead entry behind.
path_ref=$BIN_DIR
if [ -n "$home" ]; then
  case "$BIN_DIR" in
    "$home"/*) path_ref="\$HOME/${BIN_DIR#"$home"/}" ;;
  esac
fi
export_line="export PATH=\"$path_ref:\$PATH\""
fish_line="fish_add_path \"$path_ref\""

display_path() {
  if [ -n "$home" ]; then
    case "$1" in
      "$home"/*) printf '~/%s\n' "${1#"$home"/}" ;;
      *) printf '%s\n' "$1" ;;
    esac
  else
    printf '%s\n' "$1"
  fi
}

on_path() {
  case ":$PATH:" in
    *":$BIN_DIR:"*) return 0 ;;
    *) return 1 ;;
  esac
}

# Which file a new terminal reads is a per shell question and getting it wrong
# is worse than not answering it: a line appended to ~/.bashrc on a zsh machine
# is a second silent failure wearing the costume of a fix.
#
# $SHELL is the login shell, which is the one a new terminal will start. The
# shell running this script is sh either way, so $0 would say nothing useful.
#
# A container that runs this with no SHELL at all is common enough to be worth
# the second lookup. bash in sh mode fills SHELL in from the passwd entry by
# itself, so this only matters where /bin/sh is dash, which is Debian and every
# image built on it.
shell_path=${SHELL:-}
if [ -z "$shell_path" ] && command -v getent >/dev/null 2>&1; then
  shell_path=$(getent passwd "$(id -u)" 2>/dev/null | cut -d: -f7)
fi
shell_name=${shell_path##*/}
profile=""
profile_line=""
case "${home:+$shell_name}" in
  zsh)
    profile="${ZDOTDIR:-$home}/.zshrc"
    profile_line=$export_line
    ;;
  bash)
    # macOS terminals start login shells, which read .bash_profile and never
    # .bashrc unless something sources it. Linux terminals start interactive
    # non login shells, which read .bashrc and never .bash_profile.
    if [ "$os" = "darwin" ]; then
      profile="$home/.bash_profile"
    else
      profile="$home/.bashrc"
    fi
    profile_line=$export_line
    ;;
  fish)
    profile="$home/.config/fish/config.fish"
    profile_line=$fish_line
    ;;
esac

# Both spellings, because a reader who added the line by hand may well have
# written the expanded path where this writes $HOME. Missing that would append
# a duplicate on every re-run, which is the one thing an installer must not do.
in_profile() {
  [ -n "$profile" ] && [ -f "$profile" ] || return 1
  grep -qF "$BIN_DIR" "$profile" 2>/dev/null && return 0
  grep -qF "$path_ref" "$profile" 2>/dev/null && return 0
  return 1
}

# The indent is a parameter because these three lines appear both under a bare
# "Next:" and as the second half of a numbered pair, and a list that does not
# line up with the step it belongs to reads as a different list.
next_steps() {
  say "$1af doctor          check this machine"
  say "$1af runner install  finish the agent runner, which needs node"
  say "$1af init            read your repository and write a manifest"
}

wrote_profile=""
if [ -n "${AF_ADD_TO_PATH:-}" ] && [ -n "$profile" ] && ! in_profile; then
  mkdir -p "$(dirname "$profile")"
  printf '\n# Added by the Antifailure installer.\n%s\n' "$profile_line" >> "$profile"
  wrote_profile=$profile
fi

if [ -n "${GITHUB_PATH:-}" ]; then
  # Appending is idempotent because the runner replays this file into every
  # later step, so a second install in the same job must not add it twice.
  grep -qxF "$BIN_DIR" "$GITHUB_PATH" 2>/dev/null \
    || printf '%s\n' "$BIN_DIR" >> "$GITHUB_PATH"
  say ""
  say "Added $BIN_DIR to PATH for the rest of this job."
  say ""
  say "Next:"
  next_steps "  "
elif on_path; then
  say ""
  say "Next:"
  next_steps "  "
else
  say ""
  if [ -n "$wrote_profile" ]; then
    say "Added af to your PATH in $(display_path "$wrote_profile"). New terminals will find it."
    say ""
    say "1. This terminal started before that line existed. To use af here too:"
    say ""
    if [ "$shell_name" = "fish" ]; then
      say "     $fish_line"
    else
      say "     $export_line"
    fi
  elif in_profile; then
    say "$(display_path "$profile") already puts af on the PATH. This terminal started before"
    say "that line existed, so it has not picked it up."
    say ""
    say "1. Open a new terminal, or run this in the one you are in:"
    say ""
    if [ "$shell_name" = "fish" ]; then
      say "     $fish_line"
    else
      say "     $export_line"
    fi
  elif [ "$shell_name" = "fish" ]; then
    say "That directory is not on your PATH, so running af by name will say"
    say "\"command not found\" until it is."
    say ""
    say "1. This is permanent for new shells and applies to this one:"
    say ""
    say "     $fish_line"
    say ""
    say "   Or re-run the installer with AF_ADD_TO_PATH=1 to have it write that"
    say "   into $(display_path "$profile") for you."
  elif [ -n "$profile" ]; then
    say "That directory is not on your PATH, so running af by name will say"
    say "\"command not found\" until it is."
    say ""
    say "1. Put it there. The first command is the one that survives closing this"
    say "   terminal. The second makes af work in the terminal you are in now."
    say ""
    say "     echo '$export_line' >> $(display_path "$profile")"
    say "     $export_line"
    say ""
    say "   Or re-run the installer with AF_ADD_TO_PATH=1 to have it write that"
    say "   first line for you."
  else
    say "That directory is not on your PATH, so running af by name will say"
    say "\"command not found\" until it is."
    say ""
    say "1. This works in the terminal you are in now:"
    say ""
    say "     $export_line"
    say ""
    if [ -z "$home" ]; then
      say "   HOME is not set, so this installer cannot tell where your shell reads"
      say "   its startup file from. Add the same line to it to make this permanent."
    elif [ -n "$shell_name" ]; then
      say "   Your login shell is $shell_name, which this installer does not know how to"
      say "   make that permanent for. Add the same line to the file it reads at"
      say "   startup. For a POSIX shell that is usually ~/.profile."
    else
      say "   This installer could not tell which shell you use, because SHELL is"
      say "   not set. Add the same line to whatever file your shell reads at"
      say "   startup to make this permanent."
    fi
    if [ -n "${AF_ADD_TO_PATH:-}" ]; then
      say ""
      say "   AF_ADD_TO_PATH was set and this installer did not act on it, because"
      say "   guessing at the file would leave you with a line in a file nothing"
      say "   reads, which is worse than this message."
    fi
  fi
  say ""
  say "2. Then:"
  say ""
  next_steps "     "
  say ""
  say "Until step 1, af answers to its full path: $BIN_DIR/af"
fi
