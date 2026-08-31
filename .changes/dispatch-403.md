# fixed

The console's **Ask for an environment**, **Run agents** and **Run load**
controls put GitHub's raw JSON on the screen when a dispatch was refused, and
the sentence beside it named three possible causes at once. A refused dispatch
now says which one it is: the App was not granted Actions write, the App was
never given that repository, there is no repository of that name it can see,
there is no workflow file at that path on the default branch, the branch does
not exist, the workflow declares no `workflow_dispatch` trigger, or it does not
declare the inputs the console sends. Each carries its own remedy and none of
them carries a status code or a JSON body.

The same check now runs when a repository is chosen rather than when the button
is pressed, so a missing permission is visible before the form is filled in.

The permission behaviour was documented backwards: a missing `actions: write`
is a `403 Resource not accessible by integration`, not the 404 the
documentation claimed, and it is checked before the workflow file is looked
for, so it hides a missing file behind it.

`InstallationTokens.forget` had no callers anywhere in the tree. It is wired
now: a call that GitHub answers 401 drops the cached token and retries once
with a fresh one. That is not defensive programming, it is the state a person
is in the instant after they accept a permission on GitHub, because accepting
one invalidates every outstanding installation token while the cache holds the
old one for up to an hour. Without it the diagnosis above gives a confident
wrong answer, since a 401 makes every lookup report nothing wrong.

`.github/workflows/antifailure.yml` is added, so the console's controls have a
workflow to dispatch in this repository. It is dispatch only, because
`dogfood.yml` already runs the product against every pull request here.

`examples/github-workflow.yml` no longer cancels a dispatch when a second one
arrives. The console's three buttons run one after another against one branch,
so pressing Run agents used to cancel the run that was building the environment
Ask for an environment had just asked for.
