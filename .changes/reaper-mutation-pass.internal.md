# fixed

The reaper and cost cap suites were mutation tested: 14 deliberate one-at-a-time
defects, each run alone against the suite that claims to catch it. All 14 went
red and none was vacuous. The set covers both directions of the reaper's
predicate, the sweep reporting success while destroying nothing, the lease
ceiling measured from the wrong instant, and a cap that always passes.

The real-daemon test now names the environments it spared rather than leaving
that to be inferred from a set difference.
