# fixed

A Docker daemon with no address ranges left was told to run `af doctor` and
`af down`, and neither could free one.

Every environment gets two networks and Docker hands each network one range
from a fixed set, about thirty one by default, counted across everything on the
machine. When they ran out, `af up` reported AF-RUN-040, "the environment could
not be placed", and pointed at `af doctor`, which reported the runtime healthy,
and `af down`, which removes only the checked out branch's environment. The
daemon that failed held thirty networks, and fourteen of them were Antifailure
networks nothing was attached to, left by test runs killed before their
teardown ran. Nothing in the product could list them, and `af env prune`'s
only selector, age, could not reach them without also reaching the environments
in use.

That refusal now has its own code, AF-RUN-052, which counts the daemon's
networks and how many of them are Antifailure networks with no container
attached, and names `af env prune --orphaned`. That new selector lists the
environments holding networks with nothing attached and nothing running, and
removes them with `--yes`. It waits an hour from an environment's newest
resource, so one being brought up is never taken, and it never considers a
network without the Antifailure label. `af doctor`'s leftover environments
check now counts them too.
