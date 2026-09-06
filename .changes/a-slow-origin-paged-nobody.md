# changed

A control plane that answered every request correctly and slowly paged nobody.

The alerting module watched failures: a 5xx count, a restart loop, replicas
below the configured minimum, a database that stopped answering, an
availability probe that failed outright. A saturated replica set, a blocked
connection pool or one slow query on the hot path produces none of those, and
the availability test's thirty second timeout stayed green through all of it.

A twelfth rule, `slow-responses`, now reads the Container Apps `ResponseTime`
metric and pages at severity 1 when the average across every request stays
above a threshold for fifteen minutes. The threshold is
`response_time_threshold_ms`, a new input on the alerting module and on the
control plane stack, and production sets it to 2000 with the measurement that
chose the number beside it. An operator running this stack picks up the rule on
the next full apply of the alerting module; it is outside what the deploy
pipeline's targeted configuration apply reaches. Its runbook is at
`/docs/self-hosting/runbooks/slow-responses`.
