# fixed

A load run that hit its own timeout was recorded as having succeeded. The
projection read only the engine's `outcome`, which cannot say more than failed
or not, so `timed_out` was a value in the state enum that nothing could ever
reach and an interrupted run answered green. The engine's own terminal state is
read first now.

An engine's heartbeat also answers whether a cancel is waiting. Without it the
only way a cancel reached a run already going was a poll of the command queue,
which cost a minute of latency and took a lease on unrelated commands on the
way past. The column was already on the row the heartbeat updates.
