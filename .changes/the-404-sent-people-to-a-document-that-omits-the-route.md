# fixed

The API's 404 told callers to look somewhere the route was never going to be.

A request to a path with no route answered "No endpoint at this path. GET
/openapi.json lists every endpoint this control plane serves." The second half
of that was false. The published document describes the endpoints a client can
integrate with, and the transport behind the console, the `af` command line and
the engine is deliberately left out of it, route by route and with reasons
recorded. Fifty one of the sixty three routes the control plane registers are
not in the document.

So somebody who mistyped `/v1/applications` read that sentence, fetched the
document, did not find the route there either, and had been told by us to
conclude it does not exist. It does.

The 404 now says what the document is and what it leaves out, so a route
missing from it reads as possibly excluded rather than as absent. A test holds
the sentence to the register in both directions: the route it names has to be
one this process serves, and the disclaimer has to be there exactly while the
document is not exhaustive.
