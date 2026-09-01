# fixed

A load manifest could set `source: access_log` together with
`thresholds.p95_increase` and the engine accepted both. A combined format log
line carries no duration, so every route read from one arrives with no
baseline, and the threshold evaluation skips every route without one. The
headline check never fired, never errored and never said anything, and the run
went green having compared nothing.

The manifest now refuses `p95_increase` under `access_log` and under `none`,
naming `otel` as the source that carries a baseline. The engine no longer
applies its own 0.25 default under those sources either, because filling in a
threshold it cannot evaluate is the same defect with the engine as the author.
What remains is a trace export whose routes were all seen too few times to earn
a baseline: `af load` reports that as AF-LOD-016 and exits non-zero, the way a
scenario that proved nothing already does.

`thresholds.query_count_increase` is refused outright. It reached the schema,
the Go type and the normalizer, and nothing anywhere counts statements for a
load run, so it never affected a verdict. `insights.query_regression` is the
check it describes, judged by `insights.regression_factor`.
