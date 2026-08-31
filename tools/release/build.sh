#!/usr/bin/env bash
# Build and package one platform's release artifact.
#
# One copy, called by both .github/workflows/release.yml and `just
# build-release`, because the last time this logic existed twice the two copies
# drifted and the drift shipped. Both stamped BuildDate with $(date -u): the
# gate compared two local builds and the workflow built the release, and neither
# noticed that every build of one commit differed. A second copy of a build
# command is a second thing to keep true, and this repository has already paid
# for that once.
#
# The -X flags are written out here in full rather than assembled from
# variables, and tools/ldcheck reads this file to prove each one names a symbol
# that exists. The linker accepts -X for a symbol it cannot find and says
# nothing, which is how v0.1.0 shipped four platforms that all reported
# themselves as "dev". A check a one line change can defeat is not worth having,
# so the flags stay literal.
set -euo pipefail

if [ "$#" -ne 7 ]; then
  echo "usage: $0 <goos> <goarch> <version> <commit> <commit-date> <dist-dir> <stage-dir>" >&2
  echo "  version     without a leading v, as it appears in the artifact name" >&2
  echo "  commit-date RFC 3339, from git show -s --format=%cI" >&2
  exit 2
fi

goos="$1"; goarch="$2"; version="$3"; commit="$4"; commit_date="$5"
dist="$6"; stage="$7"

root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$dist" "$stage"

name="antifailure_${version}_${goos}_${goarch}"
binary="$stage/$name/af"
mkdir -p "$stage/$name/runner"

# Static, so the binary runs on a distroless image and on a distribution whose
# libc is older than the builder's. -trimpath so the build directory does not
# reach the artifact: without it the same commit built in two directories
# produces two different binaries, and a user cannot reproduce either.
#
# The date comes from the commit rather than the clock. That is the whole
# reason two builds of one commit now agree, and it still means something to
# somebody reading `af version`.
(
  cd "$root/engine"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w \
      -X github.com/antifailure/antifailure/engine/internal/cli.Version=$version \
      -X github.com/antifailure/antifailure/engine/internal/cli.Commit=$commit \
      -X github.com/antifailure/antifailure/engine/internal/cli.BuildDate=$commit_date" \
    -o "$binary" ./cmd/af
)

# The runner travels with the binary rather than being fetched later, so the
# source a release was tested with is the source it runs.
cp -R "$root/runner/src" "$root/runner/package.json" "$root/runner/tsconfig.json" \
      "$root/runner/README.md" "$stage/$name/runner/"
cp "$root/LICENSE" "$root/README.md" "$stage/$name/"

# reltar rather than `tar -czf`. tar takes the mtime of every entry from the
# filesystem, which cp has just set to now, and gzip writes its own timestamp
# into the header, so the archive carried the wall clock twice over. The
# binaries were reproducible and the archives never were, which matters because
# the archive is what a person downloads and what checksums.txt names.
go run "$root/tools/reltar" -C "$stage" -o "$dist/$name.tar.gz" -mtime "$commit_date" "$name"

(cd "$dist" && shasum -a 256 "$name.tar.gz" > "$name.tar.gz.sha256")

echo "$dist/$name.tar.gz"
