# fixed

`af net log` left `via` blank on tunnelled CONNECT, which is most of the
traffic, so a reader could not tell "arrived transparently" from "nobody filled
this in".

The proxy has four paths that record a decision, and three of them said how the
request reached it: `serveHTTP` says `proxy`, `inspectTLS` says `inspect`, and
both transparent paths say `transparent`. `serveConnect` said nothing. That is
the tunnel every HTTPS request opens when a client honours its proxy
environment variables toward a host the policy does not read inside, so the
field was empty on the majority of records while being populated on the
minority. An empty field that looks like a value is worse than a missing one,
because it reads as data rather than as an omission.

`host_only` was missing from the same record for the same reason. A tunnel
nobody reads inside is decided from the host and the port and nothing else,
exactly as the transparent path is, and the transparent path has always set it.
An inspected tunnel is still not marked, because the requests inside it are
decided on their paths and carry their own records.

Anybody reading `af net log -o json`, or building on the record shape, now gets
the same four fields on every decision rather than on three paths out of four.
