# fixed

The v1.0.0 release notes said "Nothing else moves on its own. No deployment is
triggered by this tag, and no existing environment is upgraded." Both halves
were wrong, and a reader could check them faster than we could.

`.github/workflows/cd.yml` triggers on `push: tags: ['v*']` as well as on a
push to `main`. Its staging job's condition excludes only a manual dispatch
aimed at something other than staging, so a tag push satisfies it and staging
deploys. Its production job's condition is literally
`startsWith(github.ref, 'refs/tags/v')`, gated behind the `production`
environment and its required reviewers, so production is queued for a human
rather than skipped.

So the tag does deploy, it just deploys ours rather than the reader's. That
distinction is the thing the section was trying to make and it made it by
saying something false instead. The section now says what the tag does to our
infrastructure and what it does to the reader's, separately.
