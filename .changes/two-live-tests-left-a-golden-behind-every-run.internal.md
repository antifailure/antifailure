# fixed

Two live tests in the environment package left a golden behind on every run,
and nothing could see it.

`TestUpLive_AnEnvironmentHoldsAMaskedPostgresAndAMaskedClickHouse` refreshes a
golden and then brings an environment up from it. Its cleanup ran `af down`,
and `af down` removes what an environment made, which a golden is not: a
refresh makes one on purpose so the next `af up` can branch it, and it outlives
the environment by design. So each run left a Postgres image of about 170 MB
and a ClickHouse database. One laptop held fourteen of the images and eighteen
of the databases by 2026-09-13, the oldest dated 2026-09-07, when the test was
written.
`TestUpLive_AScopedValueReachesOnlyItsOwnContainer` declares no database, and
still leaves one, because the Docker provider is the default and `af up` builds
an empty golden when nothing has been made for the project yet.

CI could not see either leak. Its "Nothing was left behind" step counts managed
containers and networks, never images, and a CI runner is thrown away after the
job, so the only place the goldens accumulated was a developer's machine.

Both tests now record the versions they make and remove them after the
environment is gone. Each then checks that no golden made for its project is
left that was not on the machine before it started, in the Postgres image store
and, for the first test, on the ClickHouse server too. Goldens an earlier,
interrupted run left behind are not touched.
