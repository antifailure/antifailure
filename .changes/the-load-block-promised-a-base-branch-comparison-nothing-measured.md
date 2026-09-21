# added

The manifest promised a base branch comparison that nothing measured.

`schemas/manifest.v1.json` said of the whole load block that traffic is
"compared between the base branch and this one" and that results "are always
deltas", and said of its thresholds that they are "applied to the difference
against the base branch, never to absolute numbers". Nothing did that. The one
threshold with a baseline, `p95_increase`, divides a measured p95 by
PRODUCTION's own p95 for that route, taken from the traffic source. That is a
useful number answering a different question: is this route slower than the
fleet serves it, never did this change make it slower. The same file contradicted
itself, because the description on the `p95_increase` key itself has always
described the production baseline accurately, and so has the prose
documentation. Only the two container descriptions claimed a comparison, and
they were the ones a reader met first.

Throughput was the sharper half of the gap. A load run has always recorded the
rate it actually achieved, and no threshold anywhere read it, so a build
serving half as many requests per second as the base branch passed every check
this product had: each request it did complete was fast, and half as many
completed.

`af load compare` is the comparison the schema described. It brings a second
environment up from the base revision, branches the SAME golden for both so the
two sides answer queries over identical rows, sends both the same weighted mix
in the same order under the same seed, and differences the two. It reuses the
oracle's baseline mechanism rather than reinventing it, as the side effect
family's base twin already does. `load.comparison.thresholds` carries
`p95_increase` against the base branch, `throughput_drop`, and
`error_rate_increase`, and the two container descriptions now say which of the
two baselines each key is measured against instead of claiming one for all of
them.

A threshold that could not be evaluated reports unverified and exits non-zero,
never pass. A route present on one side only is the case that matters most: a
build that stopped serving a route has no p95 to be slower than, and calling
that a clean comparison would hide the loudest result a run can produce.

The comparison says what it cannot control, on every report. Two runs against
two environments are not a controlled experiment. They are sequential, because
two environments sending traffic at once on one host would contend with each
other and measure that instead, and the seed makes the request sequence the
same while making nothing else the same. A difference is a difference, and the
threshold is what turns one into a verdict.

Separately, `af workload compare` computed a per route table and printed none
of it. The routes were in the JSON and absent from the text a person reads, so
a route that doubled in p95, and a route that vanished between two runs, were
both invisible to anybody not piping the output through a parser.
