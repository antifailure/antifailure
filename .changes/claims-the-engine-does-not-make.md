# fixed

Four claims the site made that the engine does not.

The flagship migration finding said "adding a column with a default rewrites the
table". Postgres stopped rewriting for a constant default in version 11, `af
init` writes Postgres 17, and the engine correctly reports no rewrite and emits
no lint finding for that statement. The example is now a type widening,
`ALTER TABLE subscriptions ALTER COLUMN plan_id TYPE bigint`, which does rewrite
and which `alter_column_type` names, so every beat of the film is something the
product measures.

"84 statements queued behind it" is gone from the home page, `/product`,
`/product/migrations` and `/solutions/devtools`. `insights.LockHold` records one
boolean, `Blocking`, whether another session was ever seen waiting. There is no
count of waiters and no list of their statements anywhere in the engine.

The load page had its two shape sources inverted: it disclaimed OpenTelemetry,
which is connected and is the only source that carries a p95 baseline, and
credited that baseline to an access log, which carries no duration at all. It
also said the manifest accepts Datadog and New Relic, which the schema enum
refuses at parse time.

`/product/twins` named thirteen orchestrator states that existed in that
component and nowhere else in the repository. It now names the six in the
control plane's `environment_state` enum, and each phase is named by an event
the engine emits. The teardown row cited `AF-RUN-045`, which only the Kubernetes
runtime raises, and now names the label-scoped teardown the Docker conformance
suite proves.

The analyzer and lint-rule counts, twelve and six against a real thirteen and
seventeen, are replaced by the property rather than by a new number.
