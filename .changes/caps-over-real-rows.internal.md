# fixed

The cost caps now have a test that runs over rows the ingestion path created
rather than over arithmetic it was handed. Every environment it sums was
brought into existence by an event arriving at `/v1/events`, which is the only
path a real engine has, and it asserts the number is not zero and that the
per-day cap refuses when the day is spent.

It goes red on this branch alone with "the cap is summing over an empty table,
so it can never trip", which is the negative control, and green once the
environments projection lands alongside it.
