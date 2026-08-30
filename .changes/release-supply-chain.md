# security

Release archives are reproducible and now proven to be. Two builds of one commit
produced four different archives every time, because `tar` takes each entry's
timestamp from the filesystem and `gzip` writes another into its own header. The
binaries always matched, which is why the old check passed: it compared
`bin/af`. Archives are written by `tools/reltar` now, with a fixed modification
time from the commit, no ownership and sorted entries, and the check builds
twice in two directories and compares the archive, on every pull request.

The bill of materials described nothing. It was generated from a directory of
`.tar.gz` files, which the generator does not open, so every release would have
carried a valid SPDX document listing one package instead of the 363 in the
binaries. It reads the binaries now, and `tools/sbomcheck` validates it against
the SPDX 2.3 schema and requires it to record the SHA256 of every binary that
ships.

Signing could not have worked at all. The pinned installer supplies cosign v3,
where `sign-blob --output-signature` is deprecated and the command refuses to
run without `--bundle`. Releases sign a bundle now, the cosign version is
pinned, and the workflow verifies both signatures and requires a copy with one
byte changed to be rejected before it publishes anything.

There is a new page on verifying a release, including how to rebuild one
yourself and compare it to the published hash.
