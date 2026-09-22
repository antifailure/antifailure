# fixed

The chaos report's `unreachable` figure could never be shorter than the
settle, however quickly the database came back.

It was timed from the fault to the first query answered after the proof had
waited out its settle, undone the fault and stopped its writers, and it made
no attempt to reach the database before then. A database that was ready 1.66
seconds after its checkpointer was killed was reported unreachable for 3.1
seconds, and the terminal, the pull request comment and the MCP answer all
repeated it.

A probe now runs beside the fault from the moment it is injected. It opens a
connection and runs `SELECT 1` every 100 milliseconds, and the outage runs from
the first attempt that went unanswered to the first answer after it. The line
prints its resolution, `110ms, probed every 100ms`, and says `never` when every
attempt was answered instead of printing a zero. Against a real Postgres it
agreed with the database's own log, from the line saying the checkpointer was
killed to the line saying it was ready to accept connections, to within 100
milliseconds.

The chaos guide also explains why the first crash after `af up` can take
several seconds longer to recover than later ones: Postgres syncs its whole
data directory to disk before replaying, and on the first crash that includes
everything written when the branch was created.
