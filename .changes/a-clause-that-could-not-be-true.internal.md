# fixed

The concurrency expression in `ci.yml`, `security.yml`, `crosslint.yml` and
`keyring.yml` excluded tags:

    cancel-in-progress: ${{ github.ref != 'refs/heads/main' && !startsWith(github.ref, 'refs/tags/') }}

None of those four workflows runs on a tag. Every one of their `on:` blocks is
a push to main plus a pull request, with a schedule or a manual dispatch on
some of them, and no tag pattern anywhere. So the second half of that
expression is evaluated against `refs/heads/main` or `refs/pull/N/merge` and
never against a tag. It read as protection and was a condition that could not
be true, with a comment above it claiming tags were excluded for a reason that
does not apply to a workflow tags do not reach.

The comment above `concurrency:` in `ci.yml` also still said, without
qualification, that a second push cancels the first run on the same branch,
three lines above the comment explaining that main is the exception.

The behaviour is unchanged. What changes is that the file now says what it
does, and it also now says what it does NOT do: a run that has started is
never cancelled, and GitHub still cancels a merely pending run when a newer
one queues behind the same in progress run. So the newest commit always
reaches a verdict and commits in the middle of a burst still get skipped.
