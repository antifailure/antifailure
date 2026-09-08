---
title: Datastore providers
description: Every store an environment holds other than the primary Postgres, the stance each one declares, and why there is no default.
sidebar:
  order: 8
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

ClickHouse branch time is the interesting column and the answer is measured
rather than assumed in either direction. `ATTACH PARTITION FROM` hardlinks the
golden's parts when the source and the destination sit on one disk, and a
branch of ten thousand rows and a branch of a million then take the same few
hundred milliseconds. What the provider cannot see from the client is the
server's storage policy: with a multi disk policy, or a source and a
destination on different volumes, ClickHouse copies the parts instead and
branch time becomes proportional to size. So the capability is declared false
and the fast case is a bonus rather than a promise.

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
