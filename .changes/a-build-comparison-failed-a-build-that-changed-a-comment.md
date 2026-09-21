# fixed

`af load compare` failed a build that differed from its base by one comment.
Measured on one host on 2026-09-21, three comparisons of two such builds each
labelled routes as moved beyond resolution, in both directions and by up to
four fold, and two of the three failed. Three comparisons of a real
regression never once flagged the route the change had slowed, and failed each
time on a route it did not touch. A regression check that fails identical code
teaches a team to stop reading it.

The resolution band added in the last release was not wrong about what it
measures: how far one run's p95 could land from itself if its requests were
independent draws from one steady distribution. On a real host latency comes in
correlated spikes, and two environments, or two runs a minute apart, differ by
far more than that. The comparison was being decided by the noise between runs,
and nothing measured it.

Now each side is sent a short warm-up that is thrown away, and then eight short
rounds, interleaved so that neither side always goes first, with round k sent
under the same seed at both. Each route's change is measured round against
round, and the interval around it comes from how much the rounds disagreed, so
it is as wide as the host's own noise. The verdict then places that interval
against the limit exactly as before: entirely above is a failure, entirely at
or below is a pass, and a limit inside it is neither, with the smallest change
this host could have resolved on that route printed beside it. On a noisy
machine the honest answer is now "too close to say", where it used to be a
failure. The JSON report carries every round's p95 per route, so the interval
can be recomputed by hand. `--rounds` and `--warmup` set the schedule, and
`--rounds 1 --warmup 0s` is the old single pass, whose notes now say what it
cannot see.

A second defect went with it. The duration and the scale were left for each
side to resolve, and the base side resolves against the base revision's
manifest, so a branch that changed `load.duration` or `load.scale` compared two
different workloads and called the answer a regression. Both are now settled
once, from this build's manifest, and sent to both sides as values.
