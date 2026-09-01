# fixed

A change that failed 100 percent of requests under load merged green.

`af ci --load` read both thresholds out of the manifest and passed one of them
on: `p95, _ := o.Thresholds()` and then `res.Breaches(p95, 0)`. `Breaches`
short circuits on `errorRate > 0`, so a zero limit builds no error rate breach
at all. The list of regressions came back empty, `policy.load_regression` was
never consulted, and the check passed, while `af load run` on the same manifest
and the same result exited non zero. Two commands, one manifest, opposite
answers.

It was invisible for a specific reason. `p95_increase` is refused under the
`access_log` and `none` sources, so those projects called `Breaches(0, 0)` and
got nil from a comparison with nothing to compare. This repository's own
manifest is `source: none` with `error_rate: 0.02`, so the dogfooding that
would have caught it could not.

`af ci` also says when a p95 threshold was in force and no route had a baseline
to compare against. `af load run` has reported that for a while, on the
argument that a threshold which measured nothing is not a threshold that held,
and `af ci` said nothing, so a report that could not compare read exactly like
one that compared and was happy.

And the third thing that block dropped: the second return of `o.Load`, the
routes the generator would not send because nothing in the manifest named them
safe. It was discarded at the call site, so `af ci --load` said the same thing
whether the safe list let through every route or one out of forty. The request
count cannot show it, because 500 requests at one route looks like 500 across
forty. `af load run` has always reported it.
