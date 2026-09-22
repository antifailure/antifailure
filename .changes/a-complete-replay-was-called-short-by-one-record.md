# fixed

A crash recovery that replayed every byte was reported as replaying less than
the client saw flushed.

The check compared the flush position a writer read, which is where the
flushed log ENDS, with the position Postgres prints in "redo done at", which is
where the last record it replayed STARTS. Every supported major, 14 to 18,
prints the start. So whenever the last record flushed was the last record
replayed, the replay looked short by exactly that record, and the durability
proof raised `chaos.recovery.replay_short` against a run that had lost nothing.
It failed main's own test with 729 commits acknowledged and 729 present.

The end of replay is now read from the checkpoint Postgres takes the moment
crash recovery finishes: from its own log line on Postgres 16 and later, and
from the control file on 14 and 15, where it is accepted only when it has the
shape of that checkpoint. When neither is available the run says it could not
tell, rather than passing or failing. A replay that really stops short of the
flushed log is still reported, and the report's "replayed from X to Y" now
names where replay ended rather than where its last record began.
