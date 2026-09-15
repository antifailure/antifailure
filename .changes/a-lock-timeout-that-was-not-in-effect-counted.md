# fixed

The migration lint's `no_lock_timeout` rule counted a `lock_timeout` that was not
in effect when the lock was taken. It asked only whether a `SET lock_timeout`
appeared anywhere in the migrations. So a `SET` after the `ALTER`, a `RESET` or a
`SET` to `0` before it, a `SET LOCAL` whose transaction had already committed, a
`ROLLBACK` that undid the `SET`, and an `ALTER ROLE` or `ALTER DATABASE` with
`SET lock_timeout`, which reaches only sessions that start later, all silenced the
rule while the `ALTER` they were meant to bound would have queued with no limit.

The rule now follows the migrations in the order one session runs them, one
transaction per file, and judges each lock against the timeout in effect at that
moment. `SELECT set_config('lock_timeout', value, is_local)` is recognised as
`SET`, or as `SET LOCAL` when `is_local` is true, when both are written out. A
value the file does not spell out never counts, and the finding says the timeout
could not be read statically. A test runs each of these orderings on a real
Postgres session and requires the rule to name exactly the lock that Postgres ran
with no timeout.
