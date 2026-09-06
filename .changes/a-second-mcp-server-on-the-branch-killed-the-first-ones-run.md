# fixed

A second `af mcp` process on the same checkout reported every run the first
one had in flight as "The server stopped while this run was in progress",
while the first server was still driving the browser through it.

Every `af mcp` process settled every queued or running run at startup, with no
record of which process was running them. Two editor windows, or one shared
preview that several people point an agent at, share one state directory, so
each new server told the others' callers that a server had stopped and to
submit again, and `retryable` said false. The run store now records the
process that owns each run and settles only those whose process has exited, by
the branch lock's own liveness rule. A run another live server is running is
left to it, and `get_rehearsal_run` and `cancel_rehearsal_run` reach it from
either server through the shared store.

A tool that could not take the branch because another process holds it
answered `SAFETY_UNAVAILABLE` with a list of usual causes that were not the
cause. It answers `BRANCH_LOCKED` now, naming the holder's process id, its
command and when it took the lock, with the same code, message and next step
`af` prints at a terminal. Over MCP a short operation is waited for, up to
fifteen seconds, and a long one is refused at once with the holder named; at a
terminal nothing changes.

Listing goldens, the fidelity inventory, the invariant check and the masking
plan and verification read the branch and write nothing, and no longer take
the branch lock at all. `af golden list` during `af up` answers instead of
printing AF-RUN-003.
