# added

The concurrent SQL workload was reachable from `af load sql` and from nowhere
else, and nothing could see it.

`run_sql_workload` puts it on the MCP server. An agent asks for clients on their
own connections running whole transactions against the branch's database, and
reads back transactions per second, transaction and per statement latency
percentiles, deadlocks, serialization failures, retries and the rows the
statements actually touched. `run_load_test` sends HTTP traffic, so the number
it reports is the application's latency with the database somewhere inside it,
reachable only through whatever the application does on a route the manifest
names safe, which is the right measurement for an application change and the
wrong one for a change to an index, a lock, a storage parameter or a query.

The gap survived because no instrument in this repository asks whether a
capability reaches more than one surface. `surfacecheck` is about which packages
may be imported and whether an export changed shape, `wirecheck` about whether a
documented variable can be delivered by an installation route, `routecheck`
about whether a route the site calls is served. A capability on exactly one
surface passes all three, so the instruments were green and could not have been
anything else.

`tools/paritycheck` is the one that asks. The inventory is the exported method
set of `*env.Orchestrator` read with the type checker, and the surfaces are the
packages that import it, so neither is a hand written list that drifts. Every
capability is reachable from every surface, or carries a row in
`tools/docs/surface-exemptions.tsv` with a kind and a written reason, and each
kind has a structural precondition that has to hold: a capability taking a
context can never be filed as an accessor, and a row goes stale and fails the
moment the gap it excuses is closed. A fourth package importing the capability
API fails the gate rather than being ignored, because a surface it cannot
classify is "I could not look" and not "I looked and it was fine".

Pointed at the tree before this change it names three: the SQL workload, its
thresholds, and `af load compare`, which landed the same day and is on the
command line alone.
