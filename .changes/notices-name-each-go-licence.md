# changed

`THIRD_PARTY_NOTICES.md` now attributes everything Antifailure ships, in a
section per artifact: the af binary, the community control plane image, and
what the enterprise control plane image adds. It used to list each Go module's
path and version and nothing else, and said nothing at all about the npm
packages and the console the images carry.

For the binary it names the licence of every Go module it links, as an SPDX
expression read from the files the module itself ships, and reproduces the
NOTICE files those modules distribute. For the images it replays each image's
own `npm ci`, read out of its Dockerfile with the same flags, and reads each
installed package's licence text; the console is attributed from what its
static export actually bundles, measured from a build with source maps. A
package that ships no licence text is attributed from its declaration and the
notice says so.

The licence is recognised, not guessed, and a gap stops the build instead of
shipping: the generator fails naming the module or package it cannot identify,
and fails naming the cause when npm or the network is not there, rather than
falling back to a lockfile. The file travels with each artifact, in the release
archives and at `/usr/share/doc/antifailure/THIRD_PARTY_NOTICES.md` in both
images, which also name that path in the `dev.antifailure.notices` label.
