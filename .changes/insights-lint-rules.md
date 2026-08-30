# added

Eleven more DDL lint rules on the migration rehearsal, seventeen in total. The
one that matters most is `lock_timeout`: a migration that waits for a lock does
not merely wait, it queues every subsequent query on that table behind its own
lock request, so a four millisecond `ALTER TABLE` blocked behind one long
running transaction stops all writes for as long as that transaction runs. The
rule reads `current_setting('lock_timeout')` from the branch before it fires, so
a project that sets the timeout on the role or on the database rather than in
the migration file is not told it has none, and where the rehearsal saw the lock
the finding carries how long it was really held on a table with production's row
counts.

The rest are `SET NOT NULL` on a column that already exists, a `CHECK`
constraint added without `NOT VALID`, `ADD CONSTRAINT ... UNIQUE` building its
index in place, a backfill in the same transaction as the schema change it
belongs to, and `DROP INDEX`, `REINDEX`, `VACUUM FULL`, `CLUSTER`, `DROP TABLE`
and `TRUNCATE`. Each names the lock mode, the real row count of the table on the
branch, and the multi deploy sequence that avoids the problem.
