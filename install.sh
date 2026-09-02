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

# The checksum is checked rather than assumed, and there is no path through
# this block that installs an unverified archive.
#
# IT USED TO FAIL OPEN THREE WAYS, and README told people it did not. A missing
# checksums.txt printed a warning and installed; a machine with neither shasum
# nor sha256sum printed a warning and installed; a checksums.txt with no line
# for this archive skipped the comparison in silence. Only a positive mismatch
# stopped anything. Every one of those is the case an attacker arranges: the
# whole point of tampering with a download is that you also control what else
# the same server hands out, so "the checksum file was not there" is not the
# benign case, it is the interesting one.
#
# A warning inside `curl | sh` is worth nothing anyway. It scrolls past on a
# machine that is already executing the thing being warned about.
#
# The cost of closing it is real and it is small: an installer that stops on a
# release with no checksums.txt. Both published releases have one, the release
# workflow builds it from the per-artifact .sha256 files and signs it with
# sigstore, and it verifies the archives against it before publishing. So this
# refuses nothing that exists today, and it refuses everything that should be
# refused tomorrow.
# Three tools rather than two. openssl reports either "SHA256(f)= hash" or
# "SHA2-256(f)= hash" depending on its major version, so the hash is taken as
# the last field rather than by matching the label.
need_sum() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    return 1
  fi
}

fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null \
  || die "no checksums.txt was published for $VERSION, so the download cannot be verified; refusing to install"

expected=$(grep " $name.tar.gz\$" "$tmp/checksums.txt" | awk '{print $1}' | head -1)
[ -n "$expected" ] \
  || die "checksums.txt for $VERSION names no $name.tar.gz, so the download cannot be verified; refusing to install"

actual=$(need_sum "$tmp/$name.tar.gz") \
  || die "no sha256 tool was found, so the download cannot be verified; install one of shasum, sha256sum or openssl and run this again"

[ -n "$actual" ] \
  || die "the sha256 tool on this machine produced no hash, so this download cannot be verified; refusing to install"
[ "$actual" = "$expected" ] \
  || die "the download does not match its published checksum; refusing to install"
say "Checksum verified"

# Everything below unpacks and places what the archive holds, and every step of
# it reports its own failure.
#
# It used to let `set -e` deliver somebody else's error, which turned out not to
# be true either. `A || { B && C; }` is an AND-OR list, and this machine's
# /bin/sh does not apply -e to it at all: with no af inside the archive, cp
# printed "No such file or directory" and the script went on to print
# "Installed $VERSION to $BIN_DIR/af", write the profile line, and exit 0. So a
# release assembled wrong reported a successful install of a file that was not
# there. Each step is an explicit `if !` now, for that reason.
tar -C "$tmp" -xzf "$tmp/$name.tar.gz" 2>/dev/null \
  || die "$name.tar.gz could not be unpacked, although it matched its published checksum; the release archive is damaged, so please report it at https://github.com/$REPO/issues"

# The archive is checked against what it promises before anything is placed. A
# hash proves the bytes arrived intact; it says nothing about the release having
# been assembled with every file in it, and that is a mistake made at build time
# rather than in transit, so the hash cannot see it.
#
# Only the files without which the install does not work. The runner's
# package-lock.json is checked separately below and NOT required, and the
# distinction is not a softening: this script ships on every push to main,
# independently of release.yml, so it runs against releases that were built
# before it existed. Every archive up to and including v0.1.1 shipped no
# lockfile, and requiring one here would have refused `AF_VERSION=v0.1.1 curl |
# sh` outright, turning a dependency pinning defect into an installer that
# installs nothing. Refuse what cannot be verified; say what is missing where
# the thing still works.
for want in af runner/src/main.ts runner/package.json; do
  [ -e "$tmp/$name/$want" ] \
    || die "$name.tar.gz unpacked with no $want in it, so this release is incomplete; refusing to install, and please report it at https://github.com/$REPO/issues"
done

# Said loudly and once, rather than silently or fatally. Without the lockfile
# `af runner install` resolves the version ranges in package.json afresh, so the
# runner this release was tested with is not the runner it runs, and two people
# installing one release get two different trees. af runner check reports the
# same thing about the installed tree.
unpinned=0
[ -e "$tmp/$name/runner/package-lock.json" ] || unpinned=1

mkdir -p "$BIN_DIR" "$PREFIX"
if ! install -m 0755 "$tmp/$name/af" "$BIN_DIR/af" 2>/dev/null; then
  cp "$tmp/$name/af" "$BIN_DIR/af" 2>/dev/null \
    && chmod 0755 "$BIN_DIR/af" 2>/dev/null \
    || die "af could not be written to $BIN_DIR; check that you can write to it, or set AF_PREFIX to somewhere you can"
fi

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
cp -R "$tmp/$name/runner" "$PREFIX/share/antifailure/runner" 2>/dev/null \
  || die "the runner could not be written to $PREFIX/share/antifailure; check that you can write to it, or set AF_PREFIX to somewhere you can"

# A tree left at the old location by an earlier installer is a source with no
# dependencies, and af test finds it before it finds anything else. Removing it
# turns a mysterious node failure into AF-AGT-004, whose remediation now works.
if [ -d "$PREFIX/runner" ] && [ ! -d "$PREFIX/runner/node_modules" ]; then
  rm -rf "$PREFIX/runner"
fi

say ""
say "Installed $VERSION to $BIN_DIR/af"
if [ "$unpinned" = 1 ]; then
  say ""
  say "warning: $VERSION shipped no runner/package-lock.json, so af runner install"
  say "will resolve the runner's dependency ranges as they are today rather than"
  say "installing what this release was tested with. af runner check reports it."
fi

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
    session_line="$export_line && af start"
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
    session_line="$export_line && af start"
    ;;
  fish)
    # fish_add_path rather than a set -gx line, because it both persists and
    # applies to the shell it runs in, so fish is the one shell where the
    # profile line and the line that fixes this terminal are the same command.
    # It also refuses to add a path twice by itself.
    profile="$home/.config/fish/config.fish"
    profile_line=$fish_line
    session_line="$fish_line && af start"
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

# One command rather than three.
#
# This printed `af doctor`, `af runner install` and `af init`, in that order,
# which was right about the order and wrong about the shape. Three commands is a
# sequence, a sequence can be interrupted, and nothing in it told somebody who
# came back an hour later which of the three they had already run. Worse, the
# list stopped at the third: `af init` writes a manifest and leaves the reader
# in front of a manifest with no idea that `af up` comes next.
#
# `af start` is the whole path, and it reports where you are on it every time
# you run it, so the installer only has to name one thing and the product
# carries the rest. It reads the machine and never writes to it, so a reader who
# pastes it before they have decided anything has changed nothing.
next_steps() {
  say "  af start           where you are, and what to run next"
}
# The same command for a branch that could not put af on the PATH, so the reader
# gets something that runs rather than a name that will not resolve.
next_steps_full() {
  say "     $1/af start"
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
    # One line and no numbered list. This used to print the paste line as step
    # one and then "2. Then:" followed by three commands, which was right when
    # there were three. There is one now, the paste line already runs it, and a
    # step two that repeats step one reads as a second thing to do.
    say ""
    say "This terminal started before that line existed. Paste this to fix it"
    say "here and see where you are:"
    say ""
    say "     $session_line"
  fi
elif [ "$reason" = already ] || [ "$reason" = managed ]; then
  if on_path; then
    say "Next:"
    next_steps
  else
    say "$(display_path "$profile") already puts af on the PATH. This terminal started"
    say "before that line existed. Paste this to fix it here and see where you are:"
    say ""
    say "     $session_line"
  fi
else
  # PATH was not set up and will not be, so nothing below may print a bare af.
  # Numbered anyway, because step 2 depends on step 1 in exactly the way the
  # original defect denied: the full paths work today, the bare names only
  # after the line above is in a file.
  case "$reason" in
    declined)
      if [ -n "$profile" ]; then
        say "AF_NO_MODIFY_PATH is set, so $(display_path "$profile") was left alone and af is"
        say "not on your PATH."
        say ""
        say "1. Add this line to it to put it there:"
      else
        say "AF_NO_MODIFY_PATH is set, so no profile was touched and af is not on your"
        say "PATH."
        say ""
        say "1. Add this line to the file your shell reads at startup:"
      fi
      ;;
    write_failed)
      say "$(display_path "$profile") could not be written, so af is not on your PATH."
      say ""
      say "1. Add this line to it, or to any file your shell reads at startup:"
      ;;
    *)
      if [ -z "$home" ]; then
        say "HOME is not set, so this installer cannot tell which file your shell reads"
        say "at startup, and it did not guess at one."
        say ""
        say "1. Add this line to that file:"
      elif [ -n "$shell_name" ]; then
        say "Your login shell is $shell_name, and this installer does not know how to make"
        say "that permanent for it, so it did not guess at a file."
        say ""
        say "1. Add this line to the file your shell reads at startup, which for a"
        say "   POSIX shell is usually ~/.profile:"
      else
        say "This installer could not tell which shell you use, because SHELL is not"
        say "set, so it did not guess at a file."
        say ""
        say "1. Add this line to the one your shell reads at startup, which is"
        say "   usually ~/.profile:"
      fi
      ;;
  esac
  say ""
  if [ "$shell_name" = "fish" ]; then
    say "     $fish_line"
  else
    say "     $export_line"
  fi
  say ""
  say "2. Until then af answers to its full path. This says where you are on the"
  say "   first run and what to run next, every time you run it:"
  say ""
  next_steps_full "$(display_path "$BIN_DIR")"
fi
node_note
