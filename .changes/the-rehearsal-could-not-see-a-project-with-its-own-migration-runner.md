# fixed

The migration rehearsal answered INCONCLUSIVE for a project that applies its own SQL files.

A project whose migrate command is a script of its own, applying a directory of
numbered `.sql` files and recording each one in a table it made, has no Prisma
or Flyway marker for the rehearsal to recognise. It was told "no migration tool
was recognised in this repository", the verdict was INCONCLUSIVE, and the lock
thresholds in `policy.migration_lock` never fired. The product's own control
plane was such a project, with forty files under `web/packages/db/migrations`.

`database.migrations` in the manifest now names the directory, and optionally
the ledger table, so the rehearsal replays those files and computes the pending
set the way the project's runner does: a file is applied when its name, stem
or leading number is in the ledger. Without the declaration, a directory of
numbered files anywhere reasonable in the tree is recognised, nearest the
service that migrates. A file that carries its own `BEGIN` and `COMMIT` is
applied as the one transaction its author meant.

Separately, every Postgres the Docker provider starts now preloads
`pg_stat_statements`, and the insights create the extension themselves where
the role may, so statement timing is measured on a local branch instead of
being reported unavailable. Where it still cannot be, the report says which of
three reasons it was, because each has a different fix.

The lint's CREATE INDEX findings named the wrong table when the index name
contained the letters "on": `environment_usage_org_idx ON environment_usage`
was reported as locking `ment_usage_org_idx`, a table that does not exist, and
its row count was looked up under that name. The keyword is now matched as a
word.
