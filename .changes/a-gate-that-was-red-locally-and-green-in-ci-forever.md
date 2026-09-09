# fixed

The prose gate refused files that can never ship, so it was red on a working
copy and green in CI on the same tree, permanently.

`prosecheck` selects files by walking the filesystem. CI checks out from git. So
a file that exists on a working copy and not in the repository is scanned by one
and invisible to the other. `PROGRESS.md` is exactly that: it sits at the
repository root, it is hidden through `.git/info/exclude` so it never appears in
a diff, and earlier handovers wrote commands of the shape
`git grep "install(" -- ee/web web` into it. Those are end of options markers
and they are correct as written, which is the collision that already stops the
double hyphen rule from being widened past the gated trees.

The result was five findings locally and none in CI, on every checkout, with no
way for CI to see the disagreement. Anyone running the gate before pushing got a
red that was not theirs, on a file they could not commit, and the two obvious
reactions, editing the handover or believing their own change broke it, were
both wrong.

The gate now skips what git IGNORES, and that is not the same as what git does
not yet track. An ignored file can never be committed, so it can never ship, so
styling it is meaningless. A merely untracked file is one `git add` from
shipping and is still read, because skipping it would pass a new page locally
and fail it in CI, which is the same disagreement pointing the other way. Both
directions are pinned by a test, and each was mutation tested: removing the
filter refuses the ignored and excluded files again, and widening it to drop
untracked files refuses the untracked one.

Outside a work tree, or when git cannot answer, every file is kept. This gate's
own tests drive it with temporary directories, and a check that quietly stopped
looking there would be worse than one that occasionally reads a file it need
not.
