# added

Installing the GitHub App opens a pull request that adds the workflow file.

Installing the App connected a repository and then nothing happened. The
installation was recorded, the repository was listed in the console, and no
check ever ran on any pull request, because a check needs a workflow file in
the repository and nothing had put one there. The documentation sent people to
copy a file of over five hundred lines by hand. Most did not, and a repository
that read as connected for a month with zero runs was the ordinary outcome
rather than the exception.

The App now opens a pull request titled "Check every pull request with
Antifailure" on each repository it is installed on. It commits the same
short workflow that `af init` writes to a branch of its own,
`antifailure/setup`, and never touches the default branch. The body says what
happens once it is merged, that nothing runs until then, that pull requests
from forks wait for the `antifailure:allow` label, that no secret is required,
which secrets are optional and what each one unlocks, and the one repository
variable the hosted control plane needs. A repository that already has the
file gets no pull request.

The webhook delivery that records the installation only enqueues the work,
because a delivery is answered inside seconds and opening the pull request is
four calls to GitHub. A sweeper beside the teardown sweeper does the calls
under a lease, retries a failure five times and then says so, and reuses a
branch or a pull request a dead process left behind rather than opening a
second one. An installation that does not hold Contents write is recorded as
needing the permission, with the remedy, and is not retried against a refusal
that would answer the same way forever; accepting the permission is what puts
it back in the queue.

The console's Environments page shows the repositories that are connected and
not yet checked under **Getting connected**: the pull request as a link, the
missing permission with its two steps, or the last error. The card is gone
once every repository has the file.

The App's Contents permission is Read and write from here on, and
`self-hosting/production` says why and what to do on an App that already
exists.
