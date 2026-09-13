# fixed

`af init` recognised a store the environment provides by searching the image
name for the store's name, and the images that stand beside a store are named
after it. `provectuslabs/kafka-ui`, a web console, was removed from the
services and reported as a second Kafka store. The same happened to
`postgrest/postgrest`, an API server, which became the application's Postgres,
and to each store's exporter, admin console and REST proxy, among them
`redis-commander`, `mongo-express`, `postgres-exporter`,
`opensearch-dashboards` and `cp-kafka-rest`.

A store is now recognised only by its repository name, against a list of the
images each store actually publishes, with any registry, tag or digest in
front. `postgis/postgis`, which the substring search missed, is recognised as
Postgres.

Once the console is a service, its `depends_on` is read, so a dependency on
a store is now decided by what the named service runs rather than by its
name. Confluent's compose files call their broker `broker`, and guessing from
that name would have written a dependency on a service the manifest never
declares, which `af init` then refuses. A service of your own called `db`
that compose builds is no longer dropped from `depends_on` for being named
like a database.
