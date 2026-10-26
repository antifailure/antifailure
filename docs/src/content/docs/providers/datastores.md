---
title: Datastore providers
description: Every store an environment holds other than the primary Postgres, the stance each one declares, and why there is no default.
sidebar:
  order: 12
---

A datastore is a store the environment holds that is not the primary Postgres:
a ClickHouse, a Redis, a Kafka, an Elasticsearch, a Mongo.

Before the `datastores` list existed there was one golden, one masking pass,
one verification scan and one branch, all of them Postgres, and every other
store a manifest declared came up as an empty container that no part of the
fidelity report mentioned. For a stack whose events live in ClickHouse, that
means a twin holding masked Postgres metadata and zero events, with the
instrument whose job is to tell you your twin is not production reporting it as
faithful.

```yaml
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: CLICKHOUSE_PRODUCTION_URL

  - name: cache
    engine: redis
    stance: empty
    because: "a cache is rebuilt from the primary and a copy would be noise"
```

The `database:` block normalizes into the entry named `primary`, so a manifest
that declares only a database already has this list and does not have to write
it. `primary` is reserved for that entry.

## The stance is the feature

**Not every datastore should be cloned, and pretending otherwise is its own
failure.** A Redis used purely as a cache is CORRECT to start empty, and a plan
that copied it would be copying noise and calling it fidelity. Kafka usually
wants topics and consumer groups rather than a replay of production traffic. An
Elasticsearch index is often better rebuilt from the Postgres branch than
cloned, because a clone can be stale against the branch in a way a rebuild
cannot.

So what an environment does with a store's contents is DECLARED per store:

| Stance | What it means | Also needs |
| --- | --- | --- |
| `golden` | A masked, verified copy that environments branch from | `source_url_env`, the variable holding production's connection string |
| `empty` | Starts with nothing in it, on purpose | `because`, in the words of whoever chose it |
| `derived` | Rebuilt from another store once that one is ready | `from`, naming that store |
| `topics_only` | Topics and consumer groups, with no messages | |

**There is no default, and a datastore that declares no stance is refused at
validation.** That is the whole design. An empty store nobody chose and an
empty store somebody decided on look identical in a running environment, and a
silent default is exactly how somebody ends up trusting a blank ClickHouse.

`because` is required for `empty` and is carried into the fidelity report as
written. It is the only thing that tells the two empties apart afterwards.

## Every stance is visible in the fidelity report

Which is what makes this honest rather than convenient. `empty` is a legitimate
answer; an INVISIBLE `empty` is not. The report names every declared store with
the stance somebody chose, so a store that holds nothing appears in the
denominator rather than outside the fraction.

## What a stance does today

The `golden` stance is brought up: the store is refreshed, masked, verified,
attested and branched like the primary database. Any other stance is validated,
carried into the report, and announced at `af up` as a store this build did not
start, by name and by stance. It is said out loud rather than skipped silently,
because an unimplemented stance that says nothing is the same failure as an
undeclared empty store wearing a manifest entry.

## What ships

| Provider | Engine | Mechanism | Holds a golden | Branch shares storage |
| --- | --- | --- | --- | --- |
| `clickhouse` | `clickhouse` | `ATTACH PARTITION FROM` against a local ClickHouse the engine starts | yes | usually |
| `mysql` | `mysql` | `INSERT ... SELECT` against a local MySQL 8.4 the engine starts | yes | no |

ClickHouse branch time is the interesting column and the answer is measured
rather than assumed in either direction. `ATTACH PARTITION FROM` hardlinks the
golden's parts when the source and the destination sit on one disk, and a
branch of ten thousand rows and a branch of a million then take the same few
hundred milliseconds. What the provider cannot see from the client is the
server's storage policy: with a multi disk policy, or a source and a
destination on different volumes, ClickHouse copies the parts instead and
branch time becomes proportional to size. So the capability is declared false
and the fast case is a bonus rather than a promise.

### MySQL

```yaml
datastores:
  - name: billing
    engine: mysql
    stance: golden
    source_url_env: BILLING_PRODUCTION_URL
```

The variable holds `mysql://user:password@host:port/database`. A password
holding `@`, `/` or `:` is percent encoded, as in any URL. The user needs
`SELECT` on the database. Any other form of address, a `mariadb://` URL, a
`jdbc:` URL or a Go driver DSN, is refused rather than read as no source, so a
typo cannot publish an empty golden that looks like a full one.

The source connection carries production's credential and its unmasked rows,
so a source on another machine is reached over verified TLS unless the
address says otherwise: with no `tls=`, the server must present a certificate
this machine trusts for that host name, and a server that cannot is refused
with AF-DB-046 naming the host. An address that is literally a loopback
address, such as `127.0.0.1` or `::1`, or exactly `localhost`, defaults to
`tls=preferred` instead, because a local server rarely has such a certificate
and its traffic crosses nobody else's network. Any other name is verified even
if it resolves to this machine, because whoever controls that resolution
decides where it points.
To accept the risk for a remote source, write the mode into the address:

| `tls=` | What it does | What it exposes |
| :- | :- | :- |
| `true` | Verified TLS. The default for a remote host. | Nothing on the path. |
| `preferred` | TLS when the server offers it, certificate never checked. | A plaintext downgrade or an impostor reads the credential and the rows. |
| `skip-verify` | TLS, certificate never checked. | An impostor reads the credential and the rows. |
| `false` | No TLS. | Anyone on the path reads the credential and the rows. |

`af explain` prints the mode each MySQL source will use, and whether the
address chose it or the default did, without printing the address.

A refresh reads every table inside one read only transaction, the way
`mysqldump --single-transaction` does, so the golden is production at one
instant rather than each table at the moment it was copied. Rows are copied as
the bytes each column stores, so a `latin1` column, a binary column holding
bytes that are not UTF-8, a zero date and a `TIMESTAMP` all arrive unchanged.
Masking then rewrites each row by its primary key, in the same rule file and
with the same transforms as Postgres, and the verification scan reads the
result back before anything is published.

Rules name a MySQL table as `<database>.<table>`, using the source's database
name, such as `billing_prod.invoices`. Masking derives each column's key from
that name too, so an address masks to the same value on every refresh.

A branch is a database on the same local server, with each table created from
the golden's own definition and filled from it by the server. That is a copy,
so branch time grows with the golden's size; MySQL has nothing that lets two
tables share storage the way a ClickHouse part can. A branch keeps the golden's
foreign keys, its indexes and each table's `AUTO_INCREMENT` position. A service
reaches it at `AF_DATASTORE_BILLING_URL` as a `mysql://` URL.

What a MySQL golden does not hold, said rather than discovered:

- **Views.** A view's definition names the source's database and its definer,
  neither of which exists where the golden lives. The golden holds the tables a
  view reads, and each refresh names every view it left out.
- **Triggers, stored routines and events.** None of them are copied, and none
  of them fire while a golden is masked.
- **A table with no primary key that a rule rewrites.** MySQL has no row
  identifier a statement can address, so masking refuses such a table when it
  plans rather than partway through it. Add a primary key, or give the table's
  columns rules that leave them as they are.

A table whose primary key is itself rewritten, such as a UUID key masked with
`uuid_remap`, is read in one pass rather than in chunks, because resuming by a
key the run is changing would read some rows twice.

The local server is MySQL 8.4. A MariaDB image in a compose file is detected as
`mysql`, and a MariaDB source is untested: a definition using a type only
MariaDB has fails to create, and the refresh says which table.

`engine` is an open string in the manifest rather than a closed list, which is
deliberate: a manifest naming an engine this build has no provider for is
refused BY NAME by the provider lookup, and that says more than an unknown
enum value would. The refusal lists the engines the build can provide.

## Choosing a provider for an engine

`provider` selects an implementation where more than one thing can provide an
engine. Omit it for the engine's own default. A registered provider is
consulted after the built in one and never before it, so a registration adds an
implementation and can never take one over.

## Writing one

Implement `provider.Datastore` and declare `DatastoreCaps`: the engine name,
whether an environment can get its own copy, whether the store can hold a
masked verified copy at all, and whether a branch shares storage with its
golden.

**Declaring `Golden: false` is a legitimate answer rather than a missing
feature.** A cache that is correct to start empty says so, and the conformance
suite then skips the golden behaviours by name instead of running behaviours
the provider never claimed.

```go
func TestMyDatastore(t *testing.T) {
    conformance.RunDatastore(t, factory, conformance.DatastoreOptions{})
}
```

The datastore suite ships with a broken fake and a self test in the same
commit, which breaks the fake one behaviour at a time and requires each break
to turn the suite red.
