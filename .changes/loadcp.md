# added

`engine/cmd/loadcp` points `engine/internal/load`, the traffic generator
customer environments use, at the hosted control plane's own API for the first
time. Its bundled profile weights each route by its own declared rate limit
from `web/apps/api/src/limits.ts`, labelled `declared_limits` rather than
`production` because no real production traffic has been captured yet. See
`docs/self-hosting/operations#load-testing-the-control-plane-itself` for how
to run it and what a real local run found: the database connection pool, not
the rate limiter, was the first thing to saturate.
