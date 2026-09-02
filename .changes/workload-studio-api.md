# added

Workload Studio has a backend. A workload is a named definition of something to
run against a preview environment: observed load, an HTTP scenario, a browser
workflow, or an exploration. Versions are immutable, runs carry the version they
ran, and what a run measured is stored as an aggregate, per route metrics,
threshold verdicts and evidence rather than as one blob.

The four kinds stay four kinds. There is no shared intermediate representation
behind them, because a weighted traffic mix has no order, a journey has no
browser, a workflow has no request rate, and an exploration has no pass. A
constraint refuses a result of one kind wearing another kind's columns.

`environments.teardown` now reaches a runtime. It used to mark a column and
carry a comment saying the engine reads the row; nothing reads the row, so the
containers stayed up while the console said the environment was gone. It now
writes a durable command, dispatches `af down` through the customer's own
workflow, and is acknowledged by the engine's own `env.destroyed` event. A
teardown nothing confirmed says so instead of expiring in silence.
