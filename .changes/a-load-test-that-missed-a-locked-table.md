# fixed

A load test ran through a table being unavailable for thirty seconds and
reported that nothing failed.

Measured on this repository on 2026-09-06. A migration was made to hold
`AccessExclusiveLock` on `events` and its partitions, and `pg_locks` sampled
from a second connection confirmed nine relations locked at once for the whole
window. `af load smoke` ran straight through it and reported 0.0 percent
failed, with p95 improving from 41ms to 17ms. The four `safe_routes` in this
repository's own manifest are written by hand and none of them reads that
table, so the run was blind to the outage it was running through. It was not a
weak result. It was a green one.

The fidelity report said worse than nothing about it. Any shape a source
produced was `reproduced`, so four routes somebody wrote from memory were
reported in the same words and with the same verdict as a mix read from a week
of production telemetry. On this repository's own manifest the sentence it
printed was `4 routes read from , at 5 requests a second`, with the source name
missing because there was no source.

`af traffic record` counts what production actually served, from an
OpenTelemetry trace export or a combined format access log that a collector or
a reverse proxy already wrote, and writes a committed profile: the endpoint
mix, the arrival rate, the peak concurrency and the per route p95. It carries
no request body, no header, no query string and no identifier. Nothing in it
opens a socket, there is no agent and no SDK, and the file is one a team
already has, which is what makes it committable and what makes it readable by
the pull request check that can reach production not at all.

Declared under `load.traffic.profile`, three things change. The fidelity
report's traffic dimension states the fraction of production's requests a run
actually sends and names the heaviest route it never touches, so a run that
cannot fail on a route says so instead of reporting a reproduction. A run with
no p95 baseline can take one from the profile, which is what
`load.thresholds.p95_increase` needed to be able to fire at all: this
repository's own manifest says of that threshold that "it was set to 0.5 here
and had never once been able to fire". And a profile older than
`load.traffic.max_age` is refused rather than quoted, the way a stale golden is.

`af traffic show` prints what production serves, busiest route first, with a
mark against every route the run reaches and the `safe_routes` lines that would
cover the ones it does not. It prints them. It does not write them: this
measures and states, and the manifest confirms it.
