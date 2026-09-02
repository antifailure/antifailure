# changed

The analytics migration is `0031`. It has been renamed six times, every time
because another branch landed ahead of it: from `0021`, `0024`, `0025`, `0026`,
`0027` and `0029`.

`migration-order` refuses a GAP as well as a duplicate, so a branch cannot hold
a number open for another branch that has not landed. The reservation has to be
made by whoever lands second, which means the number is only ever correct
relative to main at the moment of landing. A gap is visible to the branch that
has it; a duplicate is invisible to BOTH branches until one of them merges
main, so the queue cannot be validated from any single branch.

Renaming an applied migration is not free: the ledger records by name, so any
database that ran the old file re-runs the new one and fails on tables that
already exist. This file is create-only, which is the cheap shape to renumber:
drop the four analytics tables and the stale ledger rows, not the shared
container, which holds other branches' state. A migration that ALTERs a
pre-existing table costs an ordered undo instead, proportional to what it
altered, and that price is worth knowing before picking a number.
