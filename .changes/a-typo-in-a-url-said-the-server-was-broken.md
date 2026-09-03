# fixed

Any path the control plane has no route for answered 500 instead of 404.

The rate limit gate refuses to serve an endpoint with no declared limit, which
is a good rule: an endpoint nobody remembered to bound is the one nobody load
tested. But the gate runs in middleware, and middleware runs before routing, so
it could not tell a route that exists with no limit from a path that matches no
route at all. It answered both the same way. On the deployed control plane
`GET /v1/health`, `GET /v1/version`, `GET /v1/status` and anything else made up
under `/v1/` all returned 500, which told every monitor and load balancer that
the server was broken because somebody mistyped a URL, and made a real outage
indistinguishable from a typo.

The body was the second half of it. It read "Add it to ENDPOINT_LIMITS with the
reason for the number", an instruction to a maintainer of this codebase served
to anybody who could reach the port. It described the server's own gate design
to a stranger and told the one person who could act on it nothing, because
maintainers read logs rather than other people's error bodies.

The gate now asks the router which of the two cases it is looking at, and it
asks the router itself rather than a second list of paths that could drift. A
path with no route is a 404 that names `GET /openapi.json`, which this same
process serves, so the answer cannot rot. A route that exists with no declared
limit is still refused with a 500, still refused before its handler runs, and
the sentence naming the catalog key to add moved to the log line beside it.

`HEAD` was refused on every endpoint the API owns, for a related reason. The
framework answers a HEAD by dispatching it again as a GET, and the catalog is
keyed by method and holds no HEAD entries, so `HEAD /v1/environments/af-1`
returned 500 while the GET beside it answered normally. A HEAD now resolves
through the GET it will actually be dispatched as.
