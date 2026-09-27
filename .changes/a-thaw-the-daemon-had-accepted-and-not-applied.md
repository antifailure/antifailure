# fixed

A chaos run's thaw reported that it had left the database frozen, when the
daemon had accepted the thaw and had not applied it yet.

`ContainerUnpause` returning means the request was ACCEPTED, not that the cgroup
freezer has been written and the container's state recomputed. Under load those
are different instants. The undo read the state back ONCE, immediately, with no
wait, so it landed on the state the daemon still had and reported
`is still frozen after the daemon accepted the thaw` about an environment that
thawed a moment later.

The read back is why this is a wait rather than a grace. It stays, and it still
refuses to call a frozen container thawed, because asking FIRST whether the
container was paused is the older defect that turned a stumbled inspect into an
undo that reported success and left the database frozen. What changes is that
`paused` and `the inspect failed` are now read as NOT YET rather than as the
answer, for ten seconds, which is longer than the worst Docker control latency
measured on the hardware this was found on.

When the window does run out it still says no, and it says which of the two
things it saw: a container still frozen after a named duration, or a thaw nobody
could confirm after a named number of attempts. A cancelled context ends the wait
rather than spending the window on a caller that has already given up.

The cost of the missing wait was paid by unrelated changes. It reddened the
edition boundary gate on a pull request whose whole diff was a container registry
switch, and `tools/editioncheck` correctly reported COULD-NOT-LOOK rather than
naming a violation, having run the package three times with two different
verdicts.
