# fixed

The gate's Postgres test set a `lock_timeout` in its fixture. The DDL lint grew a
`no_lock_timeout` rule after the test was written, so the relaxed half of the test
saw a warning that had nothing to do with lock hold times.
