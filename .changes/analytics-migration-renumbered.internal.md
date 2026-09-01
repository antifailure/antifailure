# changed

The analytics migration is `0025`, renamed twice: from `0021` after another
branch pushed that number first, and then from `0024` once the ordering gate
proved a branch cannot hold a number open for somebody else. `migration-order`
refuses a GAP as well as a duplicate, so a branch that skips a number to
reserve it for another branch fails on its own tree. The reservation has to be
made by whoever lands second, which puts analytics after the two studio
migrations rather than between them.

Renaming an applied migration is not free: the ledger records by name, so any
database that ran the old file re-runs the new one and fails on tables that
already exist. The shared disaster-recovery container had exactly that, and the
fix is to drop the stale tables and the stale ledger row rather than the
container, which holds other branches' state too. Anything that applied
`0024_analytics.sql` needs the same treatment for the same reason.
