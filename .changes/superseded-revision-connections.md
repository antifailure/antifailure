# fixed

The staging control plane stopped answering `/readyz` because Postgres had no
connection slots left: `remaining connection slots are reserved for roles with
privileges of the "pg_use_reserved_connections" role`.

The deploy script left every superseded revision active, on the written theory
that a revision at zero traffic costs nothing. It does not. In Multiple revision
mode an active revision keeps `min_replicas` running, and each of those replicas
is a whole control plane process holding a pool of `AF_POOL_MAX` connections and
sweeping the database every five minutes for as long as it lives. Forty six
deploys had left forty six active revisions and forty six running replicas
against a burstable server that hands an ordinary role thirty five connections.

A deploy now deactivates the revisions it superseded, keeping the one a rollback
would shift onto, and then checks the arithmetic it just changed: replicas
actually running, times the pool each one holds, against what the server will
actually hand out. A shape that cannot fit fails the deploy loudly instead of
becoming a 503 at the next traffic peak. The same sum is checked at plan time in
Terraform, where `pool_max` is now set per environment rather than inherited:
staging holds five connections per replica against its thirty five, production
ten against its eight hundred and forty four.

The connection alert could not have warned about any of this. Its threshold was
eighty percent of `max_connections`, which ignores the fifteen slots Postgres
reserves, so on the burstable SKU it sat at forty when the server had already
started refusing the application at thirty five. It also averaged over fifteen
minutes, and the exhaustion is a burst: every replica sweeps on the same five
minute timer, so the measured profile was six to eleven connections for four
minutes out of five and thirty three to thirty nine in the fifth, which averages
to twelve. The threshold now comes from the connections the server will really
hand out, and the criterion reads the peak.
