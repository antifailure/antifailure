# security

Broken access control ships green because a 200 looks like success.

A pull request adds a route, the handler returns the object, the review sees a
working feature, and nobody asks whether the caller was allowed to have it. The
regression that follows is the most common one there is: an endpoint reachable
without the authorization it needs, an object reachable across a tenant, an
admin surface reachable by a member. It passes every static reviewer, because
the code that should refuse compiles and the code that does refuse is missing.

The authorization family answers the one question a static reviewer cannot. It
exercises the sanitized twin and reads the outcome, and its verdict is never
"200 is bad." It is "the base revision refused this and the change allowed it,"
which is the regression the change introduced, so a pull request that merely
touches a pre-existingly open endpoint is not false-red. The load bearing rule
is that an outcome is decided by whether the victim's content came back, not by
the status code: row level security refuses by dropping the row, so a forbidden
object returns an empty 200 or a 404, never a 403, and a check that insisted on
403 would pass every correctly isolated target and fail every one that enforces
the boundary the modern way. A refusal is trusted only when a liveness arm
proves it is a refusal and not a dead control, because a renamed route, a
session that never established, and an app that is down all read denied too. A
finding names the rule, the level, the route and a bounded description, and
never the request body, the response or the row: those stay in the copy of
production the run drove.
