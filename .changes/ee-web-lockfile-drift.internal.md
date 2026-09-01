# fixed

`ee/web/package-lock.json` records the two `bin` entries that `web/apps/api` and
`web/packages/db` declare, so `npm --prefix ee/web install` no longer rewrites
it on every run.

`ee/web` resolves `@antifailure/api` and `@antifailure/db` out of `web/` with
`file:` dependencies, so its lockfile carries a copy of those two manifests.
That copy fell behind twice. `af-control-plane-backup` was added to
`web/apps/api` on 2026-08-26 and the lockfile's own last regeneration on
2026-08-28 did not pick it up; `af-seed-staging` was added to
`web/packages/db` on 2026-08-29, after it. The lockfile recorded one bin where
the manifests declare three.

The visible cost was in CI. `ci.yml` installs `ee/web` with `npm install` rather
than `npm ci`, which repairs the drift in the working copy, so the enterprise
job has been running with a modified lockfile in its tree on every pull request.
A second install is now a no-op, which is the property that was missing.
