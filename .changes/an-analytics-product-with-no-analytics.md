# added

An environment can hold a masked ClickHouse beside its masked Postgres, and
`af up` puts one there.

A twin of an analytics product held a masked Postgres and zero events. The
events live in ClickHouse, the environment started ClickHouse as an empty
container, and every query path that mattered ran against no rows while the
run went green. A manifest could say `datastores:` and declare one `golden`,
and nothing read that at run time.

Now `af golden refresh` makes a golden of every declared store as well as of
the database, and `af up` branches each of them into the environment. A
ClickHouse golden is copied from the address `source_url_env` names, masked,
read back by the same verification scanner the Postgres golden is published on,
and branched as a database with the golden's partitions attached to it, which
ClickHouse hardlinks: a branch of three rows and a branch of a million take
about the same few hundred milliseconds. A golden that fails verification is
never published and can never be branched, which is the rule the primary
database has always had, enforced in the provider rather than in a checklist.

One `masking.yaml` covers the whole twin. A rule about `distinct_id` applies to
the column wherever it is, so one customer masks to one fake customer in both
stores and a join across them still returns one person.

Three things that came out of building it:

- The masking is a value map joined into a shadow table, not the per row
  statement the dialect describes. One `ALTER TABLE ... UPDATE` per row is a
  ClickHouse mutation, which rewrites every part holding a matching row:
  measured at 1.81 rows a second on a ten thousand row table, where the shape
  that shipped did the same ten thousand rows in 0.13 seconds. A test masks the
  same fixture both ways and requires the results to be identical.
- A store the environment provides is no longer also started as a service.
  Declaring a datastore called `events` and a service called `events` is
  declaring one thing twice, and starting both put two ClickHouses on one
  network under one name.
- A name inside the environment is no longer decided against the egress policy
  when it arrives through the explicit proxy port. The sidecar's resolver has
  always treated one as internal and forwarded it; the proxy had never heard of
  the list, so a client that reads `http_proxy` and ignores `no_proxy`, which
  busybox wget does, had its call to another service in its own environment
  refused as egress.

`provider.Datastore` gained `ListGoldens` and `DestroyGolden`. Without the
first the engine had to refresh on every command, because it cannot choose a
version it cannot see; without the second a datastore could make goldens and
had no way to remove one, which the conformance suite had written down as the
one place it could not clean up after itself.
