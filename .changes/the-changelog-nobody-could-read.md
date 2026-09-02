# added

A published changelog at https://antifailure.dev/changelog, built from the
entries in `.changes/` that this repository has been writing since its first
week and that nothing had ever rendered. There were 125 of them, written by
whoever made each change, and the only thing in the whole tree that had ever
opened one was a gate that checks documentation paths.

91 are public. The 34 marked internal stay in the repository and are never
published: they are real changes with nothing a user of Antifailure could
observe, and a changelog full of them teaches a reader that the changelog is
not about them.

A date is the day an entry landed on the main branch, read from the commit
that brought it there rather than typed. Nothing is backfilled. `v0.1.0` and
`v0.1.1` carry no entries, because both were cut before the convention began
and neither tag's tree contains the directory at all; the page says that
rather than filling them in with work that plausibly shipped in them.

# changed

A change to anything a user can see is now refused by CI unless it says what
changed. `just changecheck` is the gate and it runs the same range CI runs, so
the answer arrives locally rather than twenty minutes later. It asks whether
anything a user could notice changed, not whether anything changed: a test, a
fixture, a documentation page, a workflow, a lock file and a generated file
all pass in silence. For the genuine exception, a `Changelog-None:` trailer
carrying a reason exempts the change and leaves the reason in the history.

CONTRIBUTING.md has promised that gate since the first week and there was
none. The Developer Certificate of Origin rule in the same document went the
same way: unenforced, and 65 of the first 80 commits had no trailer by the
time anybody counted.
