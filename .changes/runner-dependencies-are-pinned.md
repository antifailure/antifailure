# fixed

The runner's dependencies were resolved fresh on every machine. Release
archives shipped `runner/package.json` and no `runner/package-lock.json`,
established by downloading the published `antifailure_0.1.1_darwin_arm64.tar.gz`
and listing it, and `af runner install` ran `npm install`. So `playwright`'s
`^1.49.0` became whatever the registry served that day, two people installing
one release got two different browsers driving their tests, and a failure one of
them saw was not reproducible by the other.

Release archives now carry the lockfile, `af runner install` runs `npm ci` when
one is present and says the tree is pinned, and `af runner check` reports an
unpinned tree as a warning rather than as the same ok a pinned one gets. A
source with no lockfile still installs, because refusing would strand somebody
pointing `--from` at a checkout, but it says plainly that the tree is not
pinned. `tools/relpack` runs the real release build and asserts what is inside
the archive, which nothing did before: every release gate checked signing,
publication and reproducibility, and all three are properties of the archive as
an opaque blob.
