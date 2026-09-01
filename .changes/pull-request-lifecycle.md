# added

Antifailure now runs on a pull request and reports there itself.

One **check run per commit**, named `Antifailure`, so a branch protection rule
can require it. The name is stable on purpose: changing it would silently
un-require the check on every repository that named it.

Seven states, and **blocked and unverified are not passes**. GitHub's `neutral`
conclusion reads as "nothing to say" and lets a required check pass, so a pull
request whose agents never ran would merge behind a green tick. That is the
defect this whole change is downstream of: `af ci` exits zero on a run that
verified nothing, so a green job means the job exited rather than that anything
was checked. A run that finishes without reporting is `unverified`, a run that
never reports at all is `timed_out`, and neither merges.

One **comment per pull request**, edited in place, and its first line carries
the commit it is about. Somebody pushes while a check is running, the first run
is cancelled, the cancellation finishes after the second run started: without
that fence the comment reports a commit that is no longer the head and nothing
says so. A stale result the reader cannot detect is worse than no result. A run
whose commit is no longer the head updates its own check, which is correct, and
leaves the comment alone.

**No repository secret to paste.** The job proves who it is with a GitHub
Actions workflow identity token and exchanges it for a credential scoped to one
commit and one run, expiring within the hour. GitHub does not grant that
identity to a pull request job running on a fork, so the fork case is closed by
GitHub's own rules rather than by this remembering to check, and it is closed a
second time here: a fork's commit gets no credential until a maintainer adds the
`antifailure:allow` label, and the approval covers that exact commit. The next
push withdraws it, because a maintainer approved code they read.

`af ci --report-json` writes the same report as JSON, which is what the job
posts.

The whole surface is fenced against the orderings GitHub does not promise: the
run event arriving before the pull request event, a job reporting before its
check exists, a push during a run, a close during a run, a reopen during a
teardown, and the same delivery twice.
