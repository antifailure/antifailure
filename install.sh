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

# The runner source travels with the binary rather than being fetched later,
# and it lands where `af runner install` looks for it.
#
# It used to land at $PREFIX/runner, which is where af LOOKS FOR AN INSTALLED
# runner, not where it looks for a source to install from. The two effects,
# both reproduced on a clean machine against v0.1.1:
#
#   af runner install  AF-AGT-004, no runner source was found, having searched
#                      $PREFIX/bin/runner and $PREFIX/share/antifailure/runner
#                      and neither of the two checkout paths. Its remediation
#                      is "install it with af runner install", so the second
#                      command the installer prints was a dead end that told
#                      you to run itself.
#   af runner check    "ok runner", because it stats src/main.ts, on a tree
#                      with no node_modules. So the breakage surfaced later,
#                      inside af test, as a node error.
#
# $PREFIX/share/antifailure/runner is one of the paths runnerSource already
# checks, resolved from the binary's own directory. Nothing in the engine
# changes; the file just goes where the engine was already looking.
rm -rf "$PREFIX/share/antifailure/runner"
mkdir -p "$PREFIX/share/antifailure"
cp -R "$tmp/$name/runner" "$PREFIX/share/antifailure/runner"

# A tree left at the old location by an earlier installer is a source with no
# dependencies, and af test finds it before it finds anything else. Removing it
# turns a mysterious node failure into AF-AGT-004, whose remediation now works.
if [ -d "$PREFIX/runner" ] && [ ! -d "$PREFIX/runner/node_modules" ]; then
  rm -rf "$PREFIX/runner"
fi

say ""
say "Installed $VERSION to $BIN_DIR/af"

# ---------------------------------------------------------------------------
# Putting af where the shell will actually find it.
#
# This block used to be two blocks that did not talk to each other. The first
# tested whether BIN_DIR was on PATH and, on the miss, printed an export line.
# The second then printed three bare `af` commands to run next, unconditionally.
# So the script knew af was unreachable and told the reader to run it three
# times anyway, and all three said "command not found". The export it printed
# was session only besides, so a reader who did paste it lost af again on
# closing the terminal, with nothing having said that would happen.
#
# The first fix printed better instructions and left the writing to the reader.
# That was still the wrong shape: an install that ends in homework is an install
# that failed, and every comparable tool has concluded the same. So the default
# now finishes the job. One export line, appended to the profile the login shell
# actually reads, printed in full so nothing is a surprise and anybody can undo
# it by deleting the line it just showed them.
#
# Prompting first is not available: a script piped into sh has consumed stdin,
# and there is no terminal to ask on. What makes taking the action without
# asking acceptable is that it is one line, it is shown, it is reversible, and
# AF_NO_MODIFY_PATH=1 declines it in advance. What makes NOT taking it
# unacceptable is that the alternative is the bug this block exists to fix.
#
# Every branch below ends in commands that work. Where PATH was set up, they are
# bare. Where it was declined, could not be written, or the shell is one this
# does not know, they carry the full path instead, because printing `af doctor`
# to somebody who cannot run `af doctor` is the whole defect.
# ---------------------------------------------------------------------------

# AF_PREFIX with no HOME set is a real combination inside a container, and this
# has to reach the end without dereferencing HOME when it is not there. An empty
# $home would also turn every "$home"/* pattern below into /*, which matches
# every absolute path, so each one is guarded.
home=${HOME:-}

# The line written into a profile names $HOME rather than the expanded path, so
# a home directory that moves does not leave a dead entry behind.
path_ref=$BIN_DIR
under_home=0
if [ -n "$home" ]; then
  case "$BIN_DIR" in
    "$home"/*)
      path_ref="\$HOME/${BIN_DIR#"$home"/}"
      under_home=1
      ;;
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

# Which file a new terminal reads is a per shell question, and now that this
# writes by default, getting it wrong is worse than the bug: a line appended to
# ~/.bashrc on a zsh machine is a silent failure wearing the costume of a fix.
#
# $SHELL is the login shell, which is the one a new terminal will start. The
# shell running this script is sh either way, so $0 would say nothing useful. A
# container that runs this with no SHELL at all is common enough to be worth the
# second lookup; bash in sh mode fills SHELL in from the passwd entry by itself,
# so getent only matters where /bin/sh is dash.
shell_path=${SHELL:-}
if [ -z "$shell_path" ] && command -v getent >/dev/null 2>&1; then
  shell_path=$(getent passwd "$(id -u)" 2>/dev/null | cut -d: -f7)
fi
shell_name=${shell_path##*/}

profile=""
profile_line=""
session_line=""
case "${home:+$shell_name}" in
  zsh)
    profile="${ZDOTDIR:-$home}/.zshrc"
    profile_line=$export_line
    session_line="$export_line && af doctor"
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
    session_line="$export_line && af doctor"
    ;;
  fish)
    profile="$home/.config/fish/config.fish"
    profile_line=$fish_line
    session_line="$fish_line && af doctor"
    ;;
esac

# Both spellings, because a reader who added the line by hand may well have
# written the expanded path where this writes $HOME. Missing that would append a
# duplicate on every run, and this runs by default now.
in_profile() {
  [ -n "$profile" ] && [ -f "$profile" ] || return 1
  grep -qF "$BIN_DIR" "$profile" 2>/dev/null && return 0
  grep -qF "$path_ref" "$profile" 2>/dev/null && return 0
  return 1
}

# af doctor is the first of the three, and the paste line below already runs it,
# so a list printed after that line starts at the second.
next_steps() {
  say "  af doctor          check this machine"
  next_steps_rest
}
next_steps_rest() {
  say "  af runner install  finish the agent runner, which needs node"
  say "  af init            read your repository and write a manifest"
}

# The same three for a branch that could not put af on the PATH, so the reader
# gets something that runs rather than a name that will not resolve. No aligned
# gloss column here: a full path plus a description does not fit in eighty
# columns, and a wrapped command is a command somebody pastes wrong.
next_steps_full() {
  say "  $1/af doctor"
  say "  $1/af runner install"
  say "  $1/af init"
}

# Said by the installer rather than left for af runner install to discover,
# because the installer knows now and the reader is reading now. A missing
# dependency named at the end of a successful install is a minute of somebody's
# time; the same one discovered three commands later is a bug report.
node_note() {
  command -v node >/dev/null 2>&1 && return 0
  say ""
  say "node was not found, and af runner install needs node 22.6 or newer."
  if [ "$os" = "darwin" ]; then
    say "Get it from https://nodejs.org, or with: brew install node"
  else
    say "Get it from https://nodejs.org, or from your distribution's packages."
  fi
}

wrote=""
reason=""
if [ -n "${GITHUB_PATH:-}" ]; then
  reason=ci
elif [ -n "${AF_NO_MODIFY_PATH:-}" ]; then
  reason=declined
elif in_profile; then
  reason=already
elif [ -z "$profile" ]; then
  reason=unknown_shell
elif on_path && [ "$under_home" = 0 ]; then
  # A directory the reader chose outside their home and has already put on
  # PATH is one they are managing themselves, and a line in their profile for
  # something that already works is the unrequested change worth not making.
  reason=managed
else
  # The braces matter. `printf ... >>"$profile" 2>/dev/null` applies the
  # redirections left to right, so the failing append reports "Permission
  # denied" to a stderr that has not been silenced yet, and the reader gets a
  # raw shell error above the message written to explain it.
  if mkdir -p "$(dirname "$profile")" 2>/dev/null \
    && { printf '\n# Added by the Antifailure installer. Delete this line to undo it.\n%s\n' \
           "$profile_line" >> "$profile"; } 2>/dev/null; then
    wrote=$profile
  else
    reason=write_failed
  fi
fi

say ""
if [ "$reason" = ci ]; then
  # Every step gets a fresh process and a fresh PATH, so the step that runs af
  # is never the one that installed it. GITHUB_PATH is what the runner reads
  # between steps, and appending is guarded because the runner replays this
  # file into every later step.
  grep -qxF "$BIN_DIR" "$GITHUB_PATH" 2>/dev/null \
    || printf '%s\n' "$BIN_DIR" >> "$GITHUB_PATH"
  say "Added $BIN_DIR to PATH for the rest of this job."
  say ""
  say "Next:"
  next_steps
elif [ -n "$wrote" ]; then
  say "Added this to $(display_path "$wrote"), so every new terminal finds af:"
  say ""
  say "  $profile_line"
  say ""
  say "Delete that line to undo it, or install with AF_NO_MODIFY_PATH=1 to skip"
  say "this step."
  if on_path; then
    say ""
    say "Next:"
    next_steps
  else
    say ""
    say "This terminal started before that line existed, so start here:"
    say ""
    say "  $session_line"
    say ""
    say "Then:"
    say ""
    next_steps_rest
  fi
elif [ "$reason" = already ] || [ "$reason" = managed ]; then
  if on_path; then
    say "Next:"
    next_steps
  else
    say "$(display_path "$profile") already puts af on the PATH. This terminal started"
    say "before that line existed, so start here:"
    say ""
    say "  $session_line"
    say ""
    say "Then:"
    say ""
    next_steps_rest
  fi
else
  # PATH was not set up and will not be, so nothing below may print a bare af.
  case "$reason" in
    declined)
      if [ -n "$profile" ]; then
        say "AF_NO_MODIFY_PATH is set, so $(display_path "$profile") was left alone and af is"
        say "not on your PATH. Add this line to it to put it there:"
      else
        say "AF_NO_MODIFY_PATH is set, so no profile was touched and af is not on your"
        say "PATH. Add this to the file your shell reads at startup:"
      fi
      ;;
    write_failed)
      say "$(display_path "$profile") could not be written, so af is not on your PATH."
      say "Add this to it, or to any file your shell reads at startup:"
      ;;
    *)
      if [ -z "$home" ]; then
        say "HOME is not set, so this installer cannot tell which file your shell reads"
        say "at startup and did not guess at one. Add this to it:"
      elif [ -n "$shell_name" ]; then
        say "Your login shell is $shell_name, and this installer does not know how to make"
        say "that permanent for it, so it did not guess at a file. Add this line to the"
        say "one your shell reads at startup, which is usually ~/.profile:"
      else
        say "This installer could not tell which shell you use, because SHELL is not"
        say "set, so it did not guess at a file. Add this to the one your shell reads"
        say "at startup, usually ~/.profile:"
      fi
      ;;
  esac
  say ""
  if [ "$shell_name" = "fish" ]; then
    say "  $fish_line"
  else
    say "  $export_line"
  fi
  say ""
  say "Until then af answers to its full path. Check the machine, finish the runner,"
  say "which needs node, then read your repository into a manifest:"
  say ""
  next_steps_full "$(display_path "$BIN_DIR")"
fi
node_note
