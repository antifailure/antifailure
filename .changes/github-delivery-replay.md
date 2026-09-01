# security

A verified GitHub delivery is now handled exactly once, however many times it
arrives.

The HMAC over the raw body says a delivery is genuine. It says nothing at all
about it being new, so a delivery captured off the wire, or replayed out of
GitHub's own redelivery log, verified exactly as well the thousandth time as the
first. Every handler downstream of that endpoint writes something.

Each delivery is claimed by its `x-github-delivery` identifier before it is
handled and stamped after. A second copy is answered without the handler
running, a copy arriving while the first is still being handled is answered 503
with a `Retry-After` rather than a success it has not earned, and a handler that
throws gives its claim back so its own retry can take it. A delivery with no
identifier is refused rather than handled unfenced.
