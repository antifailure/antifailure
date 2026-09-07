# added

A manifest asking for three instances of a service now gets three.

`replicas` had been in the schema and in the reference since version one, and
nothing read it. Both Kubernetes Deployments hardcoded a single replica and the
local runtime never mentioned the field at all, so a service declaring three
ran one. The release before this one closed the silence by refusing the key,
which was the right answer for exactly as long as the field did nothing.

It does something now. Both runtimes start the number asked for, behind the one
name every other service resolves, so requests and lookups spread across the
instances. The migration runs once for the service rather than once per
instance, the forwarder is one for the service rather than one per instance,
and readiness waits for every instance instead of the first one to answer.

The point is not scale. Nobody needs three copies of a worker on a laptop. What
more than one instance buys is a class of bug that cannot be reproduced at one:
a nightly job with no leader election that sends its email once per instance, a
consumer that reads a row and then claims it so two instances do the same work,
a session or a cache held in one process's memory that the next request does
not reach. Every one of those passes at a single instance, which is why a
runtime that quietly ran one was worse than useless to somebody who wrote
`replicas: 3` because they suspected exactly this: the green run read as the
bug being absent.

Two of those classes are now demonstrated rather than described, in tests that
pass at one instance and fail at three with nothing changed but the number. The
runtime conformance suite gained the instance count as a behavior, and a second
behavior that asks the three processes to identify themselves, because a count
is a number a runtime writes down and a runtime running one instance while
reporting three says exactly what a correct one says.

`af status` names the count when it is more than one, and the fidelity report
says how many instances are running against how many were asked for. A cron
service may not ask for more than one, because every instance runs the schedule
and three of them send the nightly email three times.

Found while wiring it: `health_timeout` was validated, defaulted, printed by
`af explain` and reported over MCP, and never assigned into the spec a runtime
receives. A service given ten minutes to start was killed after three. Both
runtimes now see the number, and the boundary the two fields were lost at has a
test on it for the first time.
