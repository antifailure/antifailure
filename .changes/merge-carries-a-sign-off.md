# fixed

A squash merge made without a sign-off stopped deployments, and nothing could
have caught it before it landed. `gh pr merge --squash --body ""` creates a
commit with no `Signed-off-by` trailer, which fails the required
`commits are attributed to their author` context on main. The deployment
workflow waits for CI on the same commit, reads that failure and refuses, so
its build, staging and production jobs are all skipped. Six merged pull
requests sat undeployed behind one missing line, and staging moved again only
when a later merge carried the trailer.

The commit hooks cannot reach this, because a squash commit is created on
GitHub's side and no hook runs there. `just merge <number>` now performs the
merge instead. It requires all nine of main's required contexts to report the
literal word success on the pull request's exact head sha, confirms that list
against branch protection rather than trusting its own copy, reads
`mergeStateStatus` rather than `mergeable`, signs off in the merging person's
own name and refuses to write anybody else's, never passes `--delete-branch`,
which deletes the branch even when the merge is refused and so closes the pull
request, and reads the commit back off the remote afterwards to prove the
trailer is really there.
