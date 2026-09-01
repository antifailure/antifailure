# changed

The analytics migration is `0024`, renamed from `0021` after another branch
pushed that number first. Renaming an applied migration is not free: the ledger
records by name, so any database that ran the old file re-runs the new one and
fails on tables that already exist. The shared disaster-recovery container had
exactly that, and the fix is to drop the stale tables and the stale ledger row
rather than the container, which holds other branches' state too.
