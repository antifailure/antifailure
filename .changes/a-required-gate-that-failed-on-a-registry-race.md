# fixed

The spelling gate failed for twenty minutes because npm was mid publish.

`npx --yes cspell` resolved whatever the tag pointed at, and on 2026-09-08 that
was a version whose own sibling package was not published yet. The step died
with "No matching version found for cspell-gitignore@10.3.0", the required
context `www` went red on a pull request whose prose was clean, and the same
command passed on the next pull request twenty minutes later. Nothing was
checked in between and nothing said so.

Every action in this workflow is pinned to a commit and vale is pinned to a
version and a checksum. cspell was the one gate left floating, so it was the
one that could report a verdict about a change without having read it. It is
now pinned to 10.3.0 in the workflow and in the justfile recipe that has to
match it.
