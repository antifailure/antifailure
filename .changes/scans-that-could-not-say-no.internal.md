# fixed

Four checks that were satisfied by examining nothing, the largest of them the
permission matrix.

The matrix is not a list of routes somebody maintains. It reads the router at
load time and generates one test per route per role, which is what makes it
worth having and is also what nobody was checking. Its cells are emitted inside
a loop over the route list, so a list that came back empty did not fail the
matrix, it produced no cells at all: no route, no role, no refusal checked, and
a green run reporting that the permission matrix passed. The three scans beside
it fail more quietly still, because they do run. Every route declares a
permission, every route has a sample input, every permission guards a route:
each one filters that same list and reports nothing wrong when handed nothing.

Proven rather than argued. With the route list forced to return empty, the
matrix passes in under three seconds instead of six minutes, having emitted
nothing, and only the new assertions go red.

The same shape in three more places. The rate limit check for every procedure
filters the same route list. The schema drift check reads the database catalogue
and reports every table nothing types, which is empty when the query matches no
tables. The tenant scoped table list compares two derived lists, and two broken
derivations agree perfectly.

The enterprise writer scan had it twice over. Nothing proved its matcher could
match, so a matcher that stopped matching would report no production writer for
every table, which is exactly what those tests already expect: the file would
stay green through the event it exists to catch, an SSO connection gaining a
real writer. It was also a case sensitive substring test, so it matched
`sso_connections_archive` when asked about `sso_connections` and missed SQL
written in the other case. It is now word bounded and case insensitive, like the
sibling scan it was modelled on, which had carried that lesson in a comment for
months without it being carried back.

Every assertion added here was watched failing first, by blinding the scan it
guards and confirming that everything else in the file stayed green.
