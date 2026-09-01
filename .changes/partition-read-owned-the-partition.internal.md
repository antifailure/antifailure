# fixed

The partition read test now owns the partition it counts, so it passes on a
second run against the same database.

`readPartition` reads a whole partition rather than one tenant's rows, because
archiving a month is what it is for. The fixture seeded 25 rows under a fresh
org each run and never removed them, so the count grew by 25 every time: green
on a fresh database, red afterwards, and the failure text blamed pagination for
a fixture problem. Reproduced on main at 125 rows against an expected 25.
