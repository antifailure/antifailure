#!/usr/bin/env bash
# G11. Two builds of one commit produce the same release artifact.
#
# The artifact, not the binary. The previous version of this check compared
# bin/af, the binaries were already identical, and it passed for months while
# every .tar.gz it was standing in for differed between builds. Measured on this
# repository: four platforms, four archives, four different hashes, every time.
# The archive is what a person downloads, what checksums.txt names, and what the
# signature therefore covers, so it is the thing whose reproducibility is worth
# asserting.
#
# Both builds happen in copies of the tree at deliberately different paths. That
# is not decoration either: -trimpath is what keeps the build directory out of
# the binary, and without it the same commit built in two places produces two
# binaries. Building twice in one directory cannot see that, so this builds
# twice in two.
#
# The second build gets its own build cache, so it genuinely recompiles rather
# than reprinting the first build's answer. A cache hit would make this check
# pass by not doing the work.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
version="${1:-v0.0.0-gate}"
bare="${version#v}"

commit=$(git -C "$root" rev-parse HEAD)
commit_date=$(git -C "$root" show -s --format=%cI HEAD)

# The host platform. One platform is enough to decide this: the packaging is the
# same code for all four, and the build flags differ only in GOOS and GOARCH.
# Cross-compiling four here would multiply the cost of the gate by four to
# re-answer a question the first one settled.
goos=$(go env GOOS)
goarch=$(go env GOARCH)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

# Two paths of different lengths, so a build directory that leaked into the
# binary changes the bytes rather than cancelling out.
one="$work/one"
two="$work/two/a-noticeably-longer-directory-name"
mkdir -p "$one" "$two"

# The source as it stands: tracked files with their working tree contents, plus
# anything new that is not ignored. Reading the commit instead would be tidier
# and would test the wrong tree, because a developer running this before
# committing would be told about HEAD while their own change went unexamined.
# Ignored files are left out so that a stale bin/ or node_modules cannot decide
# the answer.
copy_source() {
  ( cd "$root" && git ls-files -z --cached --others --exclude-standard \
      | tar -cf - --null -T - ) | tar -x -C "$1"
}
copy_source "$one"
copy_source "$two"

echo "commit      $commit"
echo "commit date $commit_date"
echo "platform    $goos/$goarch"
echo

build() {
  local where="$1" cache="$2"
  ( cd "$where" && GOCACHE="$cache" \
      ./tools/release/build.sh "$goos" "$goarch" "$bare" "$commit" "$commit_date" \
      "$where/dist" "$where/stage" > /dev/null )
}

echo "building once"
build "$one" "$work/cache-one"
echo "building again, in another directory, with a cold cache"
build "$two" "$work/cache-two"

name="antifailure_${bare}_${goos}_${goarch}"
failed=0

compare() {
  local what="$1" a="$2" b="$3"
  local ha hb
  ha=$(shasum -a 256 "$a" | cut -d' ' -f1)
  hb=$(shasum -a 256 "$b" | cut -d' ' -f1)
  if [ "$ha" = "$hb" ]; then
    echo "same  $what  $ha"
    return 0
  fi
  echo "DIFF  $what"
  echo "        $ha"
  echo "        $hb"
  failed=1
  return 1
}

echo
compare "the binary"  "$one/stage/$name/af"      "$two/stage/$name/af"      || true
compare "the archive" "$one/dist/$name.tar.gz"   "$two/dist/$name.tar.gz"   || true

if [ "$failed" = "0" ]; then
  echo
  echo "two builds of this commit produce the same release artifact"
  exit 0
fi

echo
echo "Two builds of one commit disagree, so nobody can check that a published"
echo "artifact was built from the source it claims."
echo
echo "The binary differing means something in the compile reads the moment it"
echo "ran or the directory it ran in: a timestamp in an -X flag, or -trimpath"
echo "having been dropped."
echo
echo "The archive differing while the binary matches means the packaging is"
echo "reading the clock, the umask, or the filesystem. tar takes each entry's"
echo "mtime from disk and gzip writes its own into the header, which is why"
echo "tools/reltar exists and why the packaging has to go through it."
echo
echo "What differs inside the archive:"
diff <(tar -tvf "$one/dist/$name.tar.gz") <(tar -tvf "$two/dist/$name.tar.gz") || true
exit 1
