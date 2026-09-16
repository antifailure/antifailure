# changed

The injection security family shipped able to fuzz an endpoint and prove a SQL,
command, template, NoSQL or path-traversal vulnerability, and then fuzzed
nothing. It reads the routes to exercise from `security.Input.Routes`, and the
collector that builds that Input wired the field to `nil`, because no producer
existed. A `nil` route source is UNAVAILABLE by the family's own contract, so on
every run the family reported blocked and moved on. The capability looked
shipped and did nothing.

The producer exists now. The collector sources the routes from the browser
exploration the run actually performed: every page it stood on and every
navigation it made is a route the run OBSERVED reaching, which is what the family
is meant to fuzz, rather than a route a manifest merely declared. A concrete id
in a path is templated to `{id}` and only query parameter names cross into a
route, never their values, so a route stays a location and never a row id or a
user's input. The three states the family reads as opposite verdicts are kept
exactly: a populated slice is fuzzed, an empty slice is the quiet pass of a run
that reached routes but none worth fuzzing, and `nil` stays UNAVAILABLE for a run
with no exploration to read, so a missing source never reads as a clean bill of
health.
