# added

`af ci` now runs the Postgres native checks and puts what they found in the
pull request comment. The engine has rehearsed migrations on a throwaway branch
of the environment's own golden, sampled `pg_locks` every 250ms and diffed query
plans since phase 3, and none of it reached a pull request: `af insights` was a
separate command somebody ran by hand. A migration that holds an exclusive lock
past `policy.migration_lock.fail_ms` now fails the check and names the table.

# added

The Safety Report carries sanitization status and cleanup proof.
`report.Verification` had existed with no producer, so the report said nothing
about masking; `af ci` now reads the environment's own branch back before the
workflows run and reports what it covered. Teardown moved ahead of the report,
so a run that left a resource behind says so and, by default, does not ship.

# fixed

A run that tried to reach a host the manifest does not mention no longer
reports `pass`. The request was always refused, but the attempt reached the
comment and changed nothing about the verdict.
