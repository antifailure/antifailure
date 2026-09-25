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
# probe_url asks a URL what it answers, WITHOUT following a redirect and
# without -f, and prints "<status> <location>". The location is empty unless the
# answer was a redirect.
#
# -f is the reason this exists. It prints nothing and exits non zero for every
# status from 400 up, so a caller reading its output cannot tell a refusal from
# an empty answer, and a caller inside a pipeline or a command substitution does
# not see the exit status either. That is exactly how a rate limited 403 came
# out of this script as "no release was found": see the version block below.
#
# 000 is what curl writes when it never got an answer at all, and that stays the
# contract for both implementations, because "the server said no" and "nothing
# said anything" are different facts and every message below depends on which
# one it has. `|| answer=` at the call site rather than `|| echo 000`: curl
# writes the 000 itself, and #574 landed here once already with a message
# reading "returned 000000" because both did it.
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  # -m bounds it, because this runs to explain a failure and an explanation
  # that hangs is worse than the failure. The body is one redirect's worth of
  # nothing, so thirty seconds is not a limit any working network reaches.
  probe_url() { curl -sS -m 30 -o /dev/null -w '%{http_code} %{redirect_url}' "$1" 2>/dev/null; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  # The same two facts out of wget, which reports them only in its own log, so
  # they are read back from that. -q silences the server response as well, so
  # -nv is the quietest setting that still prints the headers, and
  # --max-redirect=0 stops it following the redirect whose target is the answer.
  # A machine that never got a response leaves wget with no status line, where
  # curl prints 000, so awk supplies the 000 and the two tools keep one
  # contract. --tries, because wget retries twenty times by default and a
  # diagnosis must not take minutes.
  probe_url() {
    wget -nv -S --max-redirect=0 --tries=2 --timeout=30 -O /dev/null "$1" 2>&1 | awk '
      /^[ \t]*HTTP\/[0-9.]+[ \t]+[0-9][0-9][0-9]/ { status = $2 }
      /^[ \t]*[Ll]ocation:[ \t]*/ { location = $2 }
      END { if (status == "") status = "000"; print status, location }'
  }
else
  die "curl or wget is required and neither was found"
fi

# status_of and location_of split one probe_url answer, and they exist because
# ${answer#* } returns the whole string when there is no space in it, which
# would report a status where a location belongs.
status_of() {
  case "$1" in
    "") printf '000' ;;
    *) printf '%s' "${1%% *}" ;;
  esac
}

location_of() {
  case "$1" in
    *' '*) printf '%s' "${1#* }" ;;
    *) printf '' ;;
  esac
}

# why_not says what the server answered rather than what we had hoped for.
#
# Every download failure in this script used to name the conclusion it had
# jumped to: an archive that did not arrive "could not be downloaded", and a
# checksums.txt that did not arrive "was not published for $VERSION". Both
# sentences are true of a 404 and false of a timeout, a proxy, a DNS failure and
# a rate limit, and the reader has no way to tell which one they are holding. So
# the URL is asked what happened, and that is what is reported.
#
# $2 is a noun phrase for the thing that did not arrive, $3 is what its absence
# means for this install. The extra request is only ever made on the way to an
# error, so nothing on the working path pays for it.
why_not() {
  wn_url=$1
  wn_what=$2
  wn_then=$3
  wn_answer=$(probe_url "$wn_url") || wn_answer=""
  wn_status=$(status_of "$wn_answer")
  case "$wn_status" in
    000)
      printf '%s' "nothing answered at $wn_url, so $wn_what did not arrive and $wn_then. Check that this machine can reach github.com, then run this again"
      ;;
    403|429)
      printf '%s' "github.com answered $wn_status for $wn_url, which is what it tells an address that has asked for too much, so $wn_what did not arrive and $wn_then. Wait and run this again"
      ;;
    404)
      # A 404 on a release asset has two causes and they send the reader in
      # opposite directions. `AF_VERSION=v1.6` is a typo and there is no such
      # release; a real version with no archive for this platform is a gap in
      # the release. Saying "release v1.6 does not include the build for darwin
      # arm64" to the first one sends somebody hunting a platform problem, which
      # is this whole change's defect in a smaller sentence. The release page
      # answers which it is, and asking costs one request on a path that has
      # already failed.
      wn_tag=$(probe_url "https://github.com/$REPO/releases/tag/$VERSION") || wn_tag=""
      case "$(status_of "$wn_tag")" in
        404)
          printf '%s' "there is no release $VERSION: github.com answered 404 for $wn_url and for https://github.com/$REPO/releases/tag/$VERSION, and $wn_then. The releases that do exist are listed at https://github.com/$REPO/releases"
          ;;
        2*|3*)
          printf '%s' "github.com answered 404 for $wn_url, so release $VERSION does not include $wn_what and $wn_then. What it does include is listed at https://github.com/$REPO/releases/tag/$VERSION"
          ;;
        *)
          # The release page could not be read either, so which of the two this
          # is was not established and is not asserted.
          printf '%s' "github.com answered 404 for $wn_url, so $wn_what did not arrive and $wn_then"
          ;;
      esac
      ;;
    *)
      printf '%s' "github.com answered $wn_status for $wn_url, so $wn_what did not arrive and $wn_then"
      ;;
  esac
}

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

# Resolving "latest" without asking a rate limited API.
#
# THIS TOLD PEOPLE THERE WAS NO RELEASE WHEN THE TRUTH WAS THAT WE COULD NOT ASK.
# It read api.github.com/repos/$REPO/releases/latest, which allows an
# unauthenticated caller SIXTY requests an hour PER IP ADDRESS and answers 403
# once that is spent. `curl -f` turned the 403 into an empty string, the sed and
# head pipeline swallowed the exit status, and the empty version then fell into a
# die reading "no release was found; set AF_VERSION to install a specific one".
# The one thing we knew for certain was that no answer had arrived; the one thing
# the reader was told was the thing we did not know.
#
# It is not a rare corner. Sixty per hour is shared by everybody behind one
# address, so a corporate NAT, a cloud network and any shared CI runner reach it
# without doing anything unusual, and the advertised one line install is the
# first thing a stranger runs. Our own CI hit it on #576: the job that installs
# the way a customer's workflow does runs this eleven times, and its first
# invocation was told the product has no releases.
#
# github.com/$REPO/releases/latest answers the same question by redirecting to
# releases/tag/<tag>. It is the website rather than the API, it needs no token
# and it carries no per address budget, so the case that broke cannot happen.
#
# Serving the version from antifailure.dev instead was considered and refused.
# The site is a static export deployed when a merge lands on main, and a release
# is published when a v* tag is pushed; those are different events in different
# workflows, so a version file written at site build time is wrong for every
# release until the next unrelated merge happens to rebuild it. An installer that
# silently installs a version older than the one it was asked for is worse than
# one that says it could not ask.
if [ "$VERSION" = "latest" ]; then
  latest_url="https://github.com/$REPO/releases/latest"
  # `|| answer=` rather than a bare assignment: an assignment from a command
  # substitution carries that command's exit status, and set -e would leave
  # through it without saying anything, which is the shape of the defect above.
  answer=$(probe_url "$latest_url") || answer=""
  status=$(status_of "$answer")
  location=$(location_of "$answer")

  # A redirect is the one part of this exchange the far end chooses, so what
  # comes back is treated as input rather than as a version. It is only ever
  # used to build URLs on github.com that this script composes itself, and the
  # archive is still checked against its published checksum either way, but a
  # segment carrying a slash or a query string would compose a URL nobody
  # intended. Anything outside the characters a git tag is made of is no answer.
  VERSION=""
  refused=0
  # Where the redirect landed, because the three places it can land are three
  # different facts. A tag is the answer. The releases page is a repository that
  # has published nothing. ANYWHERE ELSE is not evidence about the repository at
  # all: it is what a proxy with its own certificate, a sign-in portal in front of
  # a network, or a repository that has been renamed answers with, and blaming the
  # product for it would be this script's own defect in a smaller sentence.
  landed=""
  case "$location" in
    */releases/tag/?*)
      tag=${location##*/}
      case "$tag" in
        *[!0-9A-Za-z._+-]*) refused=1 ;;
        *) VERSION=$tag ;;
      esac
      ;;
    */releases|*/releases/) landed=index ;;
    ?*) landed=elsewhere ;;
  esac

  # Five answers, five sentences. They used to be one sentence, and it named the
  # only one of the five that was never true when it printed.
  if [ -z "$VERSION" ]; then
    pick="Set AF_VERSION to a tag from https://github.com/$REPO/releases to install a specific release"
    # What was refused is deliberately not quoted back. It came from a redirect,
    # so it is somebody else's bytes, and an installer that prints them to a
    # terminal prints whatever control characters they hold.
    [ "$refused" = 0 ] \
      || die "$latest_url pointed at something that is not a release tag, so which release is the newest could not be established. $pick"
    case "$status" in
      000)
        die "nothing answered at $latest_url, so which release is the newest could not be established. Check that this machine can reach github.com, then run this again. $pick"
        ;;
      403|429)
        die "github.com answered $status for $latest_url, which is what it tells an address that has asked for too much, so which release is the newest could not be established. Wait and run this again. $pick"
        ;;
      404)
        die "github.com answered 404 for $latest_url, so there is no $REPO to install from, or it is not public"
        ;;
      *)
        case "$landed" in
          index)
            die "github.com answered $status for $latest_url and pointed at the list of releases rather than at one, so $REPO has published no release to install. $pick"
            ;;
          elsewhere)
            die "$latest_url was answered with $status and a redirect to somewhere that is not a release, which is what a proxy or a sign-in portal in front of this network answers with, so which release is the newest could not be established. $pick"
            ;;
          *)
            die "github.com answered $status for $latest_url and named no release, so which release is the newest could not be established. $pick"
            ;;
        esac
        ;;
    esac
  fi
fi
bare="${VERSION#v}"

name="antifailure_${bare}_${os}_${arch}"
base="https://github.com/$REPO/releases/download/$VERSION"

tmp=$(mktemp -d)
# Removed whether this succeeded or not. A half downloaded archive left in
# /tmp is the kind of thing somebody finds a year later and cannot explain.
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $name"
# The third answer this script used to collapse into one: a release that exists
# and has no build for this platform is not the same as a network that dropped
# the download, and "could not download" was said to both.
fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" \
  || die "$(why_not "$base/$name.tar.gz" "the build for $os $arch" "nothing was installed")"

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

# "no checksums.txt was published for $VERSION" is what this said for a network
# that dropped the file as well, which is the same lie the version lookup told:
# a refusal is reported as a fact about the release. It still refuses either way,
# and that is the point of the block above, but the reader is now told which of
# the two they have, because only one of them is worth running again.
fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null \
  || die "$(why_not "$base/checksums.txt" "checksums.txt" "the download cannot be verified, so this refuses to install")"

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
