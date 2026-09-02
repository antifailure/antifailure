# changed

The analytics migration is `0026`, renamed three times: from `0021` after
another branch pushed that number first, from `0024` once the ordering gate
proved a branch cannot hold a number open for somebody else, and from `0025`
when the studio branch grew a fourth migration ahead of it.

`migration-order` refuses a GAP as well as a duplicate, so a branch that skips
a number to reserve it for another branch fails on its own tree. The
reservation has to be made by whoever lands second, which is why this number
follows the queue ahead of it rather than being held.

Renaming an applied migration is not free: the ledger records by name, so any
database that ran the old file re-runs the new one and fails on tables that
already exist. The shared disaster-recovery container had exactly that on the
first rename. Anything that applied `0024_analytics.sql` or
`0025_analytics.sql` needs the same treatment: drop the four analytics tables
and the stale ledger rows, not the container, which holds other branches'
state.
