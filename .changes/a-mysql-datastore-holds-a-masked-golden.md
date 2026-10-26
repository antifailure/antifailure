# added

A MySQL datastore can hold a masked, verified golden.

`engine: mysql` with `stance: golden` copies the production database named by
`source_url_env`, masks it with the same rules and transforms as Postgres,
scans it with the same verification, and gives every environment its own
branch at `AF_DATASTORE_<NAME>_URL`. Until now a MySQL beside the primary
Postgres could only be declared empty, so that half of the product was blank in
every twin.

The copy is production at one instant, read in one transaction, and it keeps
what a naive dump loses: `latin1` and binary columns byte for byte, zero dates,
`TIMESTAMP` values, JSON, foreign keys and each table's `AUTO_INCREMENT`
position. Views, triggers, stored routines and events are not copied, and a
refresh names every view it left out. A branch is a full copy rather than
shared storage, so it takes longer the larger the golden is.

A source on another machine is reached over verified TLS by default, and a
server that cannot present a certificate this machine trusts is refused by
name with AF-DB-046. `?tls=preferred`, `?tls=skip-verify` or `?tls=false` in
the address accepts the risk explicitly, a source on this machine defaults to
`preferred`, and `af explain` prints which mode each source uses.

The source must be a `mysql://` URL. Any other form, a `mariadb://` URL or a
driver DSN, is refused rather than read as no source, and a column no rule
covered that the scan cannot read, such as non UTF-8 bytes in a column named
like a key, refuses the golden rather than publishing it.

`af init` now proposes a MySQL it finds beside a Postgres as a golden store.
The engine runs MySQL 8.4 locally for the goldens and branches, in a container
called `af-mysql`. See the datastores page for the details.
