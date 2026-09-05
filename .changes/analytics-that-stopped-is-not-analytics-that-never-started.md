# fixed

An installation that recorded analytics and then stopped said exactly what an
installation that never recorded says, and only one of them is a fault. Both
render as a dashboard that is not moving, both logged "analytics is NOT
recording: AF_ANALYTICS_SURROGATE_SECRET is not set" at start-up, and the page
offered the same instruction to switch it on. That sentence is correct and
useless for the installation that was recording until Tuesday, because the
numbers on the screen are real, they are frozen, and nothing says so.

The way to get there is a rollback. The surrogate secret and the operator
organization reach the process as environment variables, and rolling a Container
App back goes to an earlier revision with its environment attached, so a
rollback to any revision from before analytics was configured switches recording
off while everything else keeps working.

The absent variable cannot detect its own absence. A rolled back deployment and
a control plane that never wanted analytics present the identical environment,
and the second is a legitimate way to run this: staging does it, and so does any
self-hosted installation whose operator does not want the numbers. A rule that
refused a missing variable would refuse both.

So the question is asked of the database instead, which is the one party a
rollback does not move. `analytics_rollup_state.last_run_at` is null until the
rollup first runs and set forever after, and the application role could already
read it. Null with recording off means this installation never recorded, and the
existing wording is right. Set with recording off means it recorded and stopped,
and now the start-up log says so at error level with the timestamp of the last
rollup, and the dashboard says the numbers end where they end rather than
offering to switch on something that was already on.

This deliberately does not refuse to start. A rollback happens during an
incident, the image carries this code into whichever revision is rolled back to,
and Container Apps cannot add a variable to a revision that already exists, so a
start-up refusal would block the recovery at the moment it was needed and turn
lost analytics into a control plane that is down.
