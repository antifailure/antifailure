# fixed

The image pin gate read files git has never heard of, and refused a fully
pinned repository because of one.

The check added in #338 enumerated what it scans by walking the working tree
for shell scripts. A working tree holds more than a repository declares. The
harness that mutation tested that very gate is an untracked script in a lane's
worktree, it carries a moving tag in one variable and a deliberately wrong
digest in another, and the gate read both and reported the tree as unpinned
while every real declaration in it was correct.

Every required context stayed green throughout, because a clean checkout has no
scratch file. So the only person who could ever see this is somebody running
`just gate` with a scratch script open, and from there it reads as the gate
being broken rather than as the gate looking at the wrong thing. That is how a
working gate gets switched off.

The enumeration now comes from `git ls-files`, because what this repository
declares is what git tracks. A listing that cannot be obtained fails the test
rather than skipping it, since "I could not look" and "there was nothing to
find" are different answers and only one of them is a pass.
