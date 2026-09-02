# added

`af start` says where you are on the first run and names the one command that
moves you forward. The first run is nine commands long and any of them can be
interrupted, and until now coming back meant reconstructing the state from
memory: `af init` refuses a repository that already has a manifest, `af up` on a
running environment is a no-op nobody recognises as one, and neither says where
you actually are.

It derives every answer from the machine rather than from a record of what it
last did, so tearing an environment down by hand or switching branches moves the
answer with you. It runs nothing and writes nothing, which is the only way it
can be honest: a command that ran the steps would have to report on work it did.

Each step reports one of four states and never collapses one into another: done,
not yet, blocked, and not checked. The fourth is the point. Listing goldens takes
this branch's lock, so a status command safe to run while `af up` is in flight
cannot ask, and that step says so and names `af golden list` rather than
reporting a golden it never looked for. Exit 0 means every step is either done or
not reached yet, which is the normal state of a first run in progress. Exit 3
means something is broken.
