# fixed

The manual deploy to production could never have run. `cd.yml` offers two ways
into the `production` job, a version tag or a manual run asking for production,
and the second was skipped every time by a rule nobody had reason to think
about: a job whose needed job was skipped is skipped too, and `staging` skips
itself on exactly that manual run. The condition now names each dependency's
result, so the tag path behaves as it always did and the escape hatch works.

There is also a release runbook now, at
`docs/src/content/docs/self-hosting/releasing.md`: what a `v*` tag sets off in
both workflows, what green looks like at every stage, and what to do when a
stage goes red.
