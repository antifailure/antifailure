# fixed

The worktree collector decided a branch was disposable from two signals when it
needed three, and the third is the only one that can see work written after a
merge.

It matched the branch NAME against GitHub's merged pull request list and checked
that the working tree was clean. Both are true of a branch that landed. Both are
also true of a branch that landed and then carried on, because `just merge`
squashes: the squash is a new commit on main, the branch is left behind pointing
at its own history, and a commit written after the merge is invisible to a name
match. On 2026-09-11 six branches in this repository were in that state and
three of them had clean worktrees, so the collector would have removed the
worktree and run `git branch -D` over work that is in no other place:

    w-runtime-aca                9 commits after pull request 316
    w-detect-clouds-datastores   7 commits after pull request 308
    w-engine-red                 1 commit  after pull request 333

The third signal is now required and comes in two shapes. Either the branch has
zero commits main cannot already reach, so deleting the ref cannot orphan a
commit at all, or the branch tip is byte identical to the exact commit its
merged pull request squashed, so the branch holds nothing written after it.
Content diffing is not a substitute and is deliberately not used: a squash
followed by further evolution of the same files on main makes even a branch
merged hours ago report conflicts, which would have refused most of the 81
branches that really had landed.

The first run of the fixed collector against the real machine found the next
hole. A lane that has just branched from main has no commits of its own, so the
first shape calls it disposable, and it called seven live lanes removable, one
of them a session that was writing files that minute. A clean tree is not an
idle one, because a lane's handover and scratch are ignored by design and
`git worktree remove` deletes ignored files without asking. So three more
signals now stand between a proof and a removal: nothing ignored may be anything
but build output, no process may be working inside the worktree, and nothing in
it may have been written in the last day. Each worktree is judged again
immediately before it is removed, and the branch is deleted by compare and swap
against the tip that was proven, so a lane committing mid run fails the
deletion rather than losing the commit.

The collector also prints what it could NOT establish, by worktree and by
missing signal, and exits nonzero when that list is not empty, because a
collector that silently passes over a worktree it could not read looks exactly
like one that found nothing wrong. And it writes every tip it deletes to a ref
under `refs/worktree-gc/` first, so each deletion is reversible with one
command.

It lives in the repository now, as `tools/worktreegc`, with tests against real
fixture repositories. It was an untracked file in one checkout, which is why a
program that runs `git branch -D` had no history, no review and no test for as
long as it existed.
