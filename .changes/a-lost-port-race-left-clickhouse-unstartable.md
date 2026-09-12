# fixed

Starting the local ClickHouse could fail for good after one unlucky moment.
The engine picks a free port, then asks Docker to publish the server on it, and
anything else on the machine can take that port in between. When that happened
the engine gave up rather than trying another port, and it left behind a
container that could never start, still holding the fixed name `af-clickhouse`
and the port it had lost. Every later `af` command that needed ClickHouse found
that container, tried to start it, and failed the same way until somebody
removed it by hand.

Now a lost port is retried on a different port, up to three times, the way the
Postgres provider already did. A container that fails to start is removed
before the error is reported, whatever the reason it failed, so the next
attempt starts clean. A ClickHouse that already exists and holds your data is
never removed by this: only a container the same attempt created and could not
start.

CI hit this on main in two of twenty five runs, as ten live ClickHouse tests
failing together with "Bind for 127.0.0.1:43000 failed: port is already
allocated".
