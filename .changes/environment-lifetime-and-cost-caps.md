# added

Environments now have a lifetime that is enforced. `runtime.ttl` had been
declared, validated, defaulted and printed by `af explain` since the manifest
existed, and read by nothing: every environment lived until somebody remembered
it, holding a database branch, a network and a container per service the whole
time.

`af env reap` removes the environments whose lifetime has ended, and nothing
else. The expiry is stamped on the resources when they are created and read
back off them, never taken from the manifest the sweep was run with, so a
repository with a two hour lifetime cannot remove another project's week long
environment on the same machine. An environment whose resources state no
lifetime is never removed, because reading "states nothing" as "already over"
would turn an upgrade into a machine wipe; use `af env prune --older-than` for
those. An environment something is running against is deferred to the next
sweep rather than pulled out from under a running command.

`af env extend` keeps an environment you are still using. It is bounded by the
new `runtime.max_ttl`, measured from when the environment was created rather
than from now, so extending repeatedly cannot walk the limit forward.

`runtime.ttl` now defaults to `24h` rather than `168h`. The week was chosen
when nothing read the field, so it expired nothing and cost nothing; it is now
the default `runtime.max_ttl`, which means an environment that genuinely needs
a week can still have one by asking for it.

The control plane refuses a run that would exceed a per-run or a rolling daily
cap on environment-hours, naming the cap, the usage, and that an owner of the
organization can change the plan. Reaching a cap refuses the next creation and
never removes anything that exists.

See `docs/reference/environment-lifetime`.
