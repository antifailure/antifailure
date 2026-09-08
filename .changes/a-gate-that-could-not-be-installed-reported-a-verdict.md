# fixed

A required context reported a verdict about a change it never read, and then healed itself.

`npx --yes cspell` floated on the dist tag. On 2026-09-08 it resolved a version
whose own sibling package had not finished publishing, and the spelling step of
the `www` job died with "No matching version found for cspell-gitignore@10.3.0".
Twenty minutes later the identical command passed on another pull request.
Nothing was checked in between and nothing said so.

That failure is the worst shaped one this repository has. In the summary view it
is indistinguishable from a real spelling mistake, and then the registry catches
up, so the next person re-runs the red job, sees green, and learns that
re-running a red job is how you fix one.

`cspell` was pinned in an earlier change. This adds the gate for the class.
`TestEveryToolIsFetchedAtAPinnedVersion` refuses a workflow or a recipe that
fetches a package with `npx --yes` and no version. It reads the justfile as well
as the workflow, because an unpinned recipe beside a pinned CI step is exactly
the disagreement `gatecheck` exists to stop: the developer's run and the gate's
run would resolve different software.

What it cannot see is written into it rather than implied. `apt-get install`
resolves against Ubuntu's archive every run and is not practically pinnable. And
a pin answers which thing, never whether it arrived: a tool pinned to a commit
and to a version still failed the same day with exit 22 fetching its own
tarball, which is a different failure with a different remedy.
