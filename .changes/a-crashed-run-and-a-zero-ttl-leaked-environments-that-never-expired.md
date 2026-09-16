# changed

An environment could still be born with no expiry, and the reaper never
collects one of those, so it lived until somebody remembered it and read the
bill. Two ways in, both closed now.

`runtime.ttl: 0h` passed validation: `0h` is a duration, so the check that a
lifetime is a duration accepted it, and the runtimes stamp the reaper's expiry
label only when the lifetime is positive. The result was an environment with no
label, invisible to `af env reap` for ever. A non-positive `ttl` is now refused
at load, with a message that says to set a positive duration or leave it unset
for the default. A manifest that states no `ttl` still inherits the `24h`
default, so "no lifetime stated" means the default and never means immortal.

A `af ci` run stamped its throwaway environment with the day-long default, and a
run that CRASHED, killed for memory or with its runner pulled out from under it,
never reached the teardown that would have removed it. The environment sat for a
day carrying that default. `af ci` now bounds its environment to the run's own
budget, its `--timeout` plus a grace and never more than `runtime.ttl`, so a
crashed run's leaked environment is collected within the hour rather than the
next day. The normal path still tears it down at once; only the crash path is
changed, and only for the better.
