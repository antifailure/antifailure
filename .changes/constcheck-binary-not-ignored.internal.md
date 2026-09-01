# fixed

`.gitignore` now covers the `constcheck` binary, so building the tool at the
repository root cannot leave a platform specific executable for the next
`git add -A` to commit.

Every other tool already had its entry. This one was missed when the tool was
added, and the test that catches it only started running again once the full Go
suites moved to `-count=1`: the check shells out to `git check-ignore`, whose
file reads are invisible to Go's test cache, so it had been reporting a cached
pass over a tree it never examined.
