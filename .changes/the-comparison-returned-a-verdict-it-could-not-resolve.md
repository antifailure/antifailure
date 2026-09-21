# fixed

`af load compare` returned a confident verdict on a difference it could not see.

An identical build was compared against itself, two commits whose only
difference is a comment in one Go file, so every number either run produced was
noise. Two samples, minutes apart, on a loaded host: the first called every
route better, headlined a p95 57.4 percent lower, and passed; the second called
every route worse, headlined 192.3 percent higher, and FAILED six routes
against a 60 percent limit. A real regression measured on the same machine
headlined at 253.7 percent, which is smaller than the 585.9 percent a comment
produced.

Both readings were reported as decisions. A reader of the second opens a pull
request about a regression that does not exist; a reader of the first ships a
real one believing it an improvement.

The cause was countable and already in the document. Those runs sent 145
requests across seven routes, about twenty per route, and a p95 taken from
twenty samples by nearest rank IS the nineteenth of twenty, so it sits one slow
request from the maximum. Nothing asked whether twenty samples could support
the question being put to them.

Every comparison now measures its own resolution first. A percentile estimated
from n samples is an order statistic whose rank is itself random, so the run's
own sample count and its own recorded distribution give the distance that
number could have landed from itself with nothing changing. Each route prints
what it can see beside what it saw, and the verdict is decided by where the
declared limit falls relative to that interval: entirely above it is a breach,
entirely at or below it holds, and a limit inside the interval is one this run
cannot place, which is reported as unverified and never as a pass.

It does not widen anybody's threshold. A limit is a declared tolerance for a
real change, and loosening it to silence a false alarm would hide real
regressions. A run that CAN see the difference still decides: a genuine
regression of 600 percent against a 100 percent limit, on a run whose
resolution is 200, is still a failure, because the interval around it sits
entirely above the limit.

A direction is withheld on the same evidence. The identical build reported
routes better by 86 percent and worse by 586 percent on consecutive samples,
and the arrow was as wrong as the number, so a difference smaller than the
distance it could have moved now reads "too close to say" rather than better or
worse.

The band covers the sampling error a single run can see and does not cover
drift between two runs on a busy host, which is larger: the two samples above
disagree by more than the band around either. So it is a floor on the
uncertainty rather than the whole of it, and the documentation says so.
