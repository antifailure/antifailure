# changed

The analytics migration is `0027`, renamed four times: from `0021` after
another branch pushed that number first, from `0024` once the ordering gate
proved a branch cannot hold a number open for somebody else, from `0025` when
the studio branch grew a fourth migration ahead of it, and from `0026` when the
admin operator migration was merged to main out of order and took `0023`.

`migration-order` refuses a GAP as well as a duplicate, so a branch that skips
a number to reserve it for another branch fails on its own tree. The
reservation has to be made by whoever lands second. A gap is visible to the
branch that has it; a duplicate is invisible to BOTH branches until one of them
merges main, so the queue cannot be validated from any single branch.

Renaming an applied migration is not free: the ledger records by name, so any
database that ran the old file re-runs the new one and fails on tables that
already exist. This file is create-only, which is the cheap shape to renumber:
drop the four analytics tables and the stale ledger rows, not the shared
container, which holds other branches' state. A migration that ALTERs a
pre-existing table costs an ordered undo instead, proportional to what it
altered, and that price is worth knowing before picking a number.
