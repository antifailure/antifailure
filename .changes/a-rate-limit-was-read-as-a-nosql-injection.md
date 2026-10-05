# fixed

The injection check reported a NoSQL operator smuggled into pages that have no
database behind them, and quietly skipped most of what it was meant to fuzz.

It sends hundreds of requests from one address. Against an application with a
per address rate limit it spent the limit itself, and from then on most of its
requests were answered 429. Those answers were handed to the checks as if the
application had refused the value. When a request was let through just after
one that had been refused, the NoSQL check read the pair as "an operator turned
a refusal into an answer". Our own nightly run reported exactly that against
static Next.js pages on three nights out of five, on a commit that had not
changed, naming a different page each night. Measured against the same limit
(20 a second, a burst of 120), the old check skipped about 330 of its 486
comparisons and reported between none and six false findings, depending on how
long each request took.

The check now waits as long as a 429 or a 503 with Retry-After asks before it
tries again, and never hands one of those answers to a check. A difference
counts only when it shows up a second time with the requests sent in the
opposite order, so one caused by timing does not survive. If the limit never
lifts, the check says it could not complete rather than reporting a pass.

When the check is stopped partway, by the limit or by the run's deadline, the
note says how many comparisons it made out of how many it planned. Anything it
had already proved is still reported beside that note, so a partial check
reads as unfinished, never as clean.
