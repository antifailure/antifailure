---
title: Inventory
description: What an environment reproduces, component by component, and what it could not.
sidebar:
  order: 7
---

An environment is a copy of production, and no copy is complete. The database
is masked. Some third party hosts are answered offline and some are refused
outright. The traffic is whatever the manifest could point at. Every one of
those is a deliberate choice, and each of them makes the copy differ from the
thing it is a copy of in a way somebody reading a green check ought to know
about.

`af fidelity` takes the inventory.

```
af fidelity
af fidelity -o json
```

## Where the numbers come from

Every line comes from something the engine already knew and was not telling
anybody.

| Dimension | What it reads |
| --- | --- |
| `services` | The services the manifest declares, against the containers the runtime reports running. |
| `database` | Which golden the branch came from, whether that golden is verified, whether its signed attestation still matches its own signature, and how many tables and rows the branch holds. |
| `third_party` | The hosts the egress policy names, the mode each is in, and which mock pack answers for the ones in mock mode. |
| `auth` | Whether each declared persona actually has a row in the branch, and whether the way it signs in can be carried out here. |
| `runtime` | Where the environment runs. |
| `traffic` | Which routes a load run would actually send, measured against the committed traffic profile of what production served, and how fast it sends against production's own rate. With no profile both are `unmeasured` and say so: four routes somebody wrote by hand used to report as a reproduction of production's traffic. |
| `datastores` | Every datastore in the environment other than the primary database, and whether anything reproduced its contents. One the manifest declares `golden` and this environment branched reports what the branch holds and which golden it came from, the way `database` does. One declared `golden` that nothing branched is `absent`. The others are `unmeasured` by name. |
| `topology` | How many instances of each service are running, against how many the manifest asked for. |

Nothing is estimated and nothing is a constant somebody typed because the
report needed a number.

## The states

A component is in one of five states, worst to best.

| State | Means |
| --- | --- |
| `unmeasured` | Its state could not be determined. Never counted as a pass or as a failure. |
| `absent` | The manifest asked for it and the environment does not have it. |
| `refused` | The policy deliberately does not reproduce it. A host in `block` mode is refused: the environment is doing what it was told, and it still does not reproduce that host. |
| `substituted` | Something stands in and behaves. A stateful mock pack, a captured message, a subset of the data. |
| `reproduced` | The real thing, present and answering. |

A dimension's verdict is the weakest measured state in it, because the one
component that was not reproduced is what a reader needs, not the average of
the ones that were.

## Not measured is a result

An `unmeasured` component is excluded from the score and named with the reason.
It is never quietly counted as either answer. This is the same discipline the
[insights](/docs/concepts/insights) report applies when it says what it could
not read, and for the same reason: a report that silently omits a check reads
exactly like a check that found nothing.

The cases that produce it today:

- The runtime could not be reached, or nothing is running for this environment.
  A stopped environment has not been shown to reproduce nothing.
- The database provider does not record which golden a branch came from.
- A host in `synth` mode. A model invents the response and the product already
  marks anything that touched it unverified rather than passed, so counting it
  as a reproduction would contradict the verdict.
- A `mock` rule that matches a pattern rather than one host, where which pack
  answers depends on the host the application reaches.
- A persona created through a provider's own API rather than in the branch,
  which nothing here can read without calling it.
- A datastore other than the primary database whose declared stance is not
  `golden`. Nothing here starts a second store, rebuilds one from the branch or
  creates a topic in one, so whether an `empty` store came up empty on purpose
  is genuinely unknown. A store declared `golden` is not in this list: it is
  `absent`, and the next section says why.
- A service that names no instance count, in the `topology` dimension. It runs
  one because one is what an omitted key means, not because anything compared
  that against production.

A dimension the manifest never asked for is excluded too, whole, with the
reason. An environment that sends no traffic at all has not reproduced traffic
perfectly.

## The score

```
17 of 21 measured components are production's own, which is 81 percent.
```

Reproduced over measured. A substitution, a refusal and an absence are all in
the denominator and none of them is in the numerator, which is what makes the
number mean "how much of this is production" rather than "how much of this went
to plan". Nothing unmeasured is in either half, and every exclusion is printed
under the table with the reason it was excluded.

When nothing could be measured there is no score. That is not nought percent
and is never rendered as one.

The per dimension verdict is the part to read. A change to billing cares about
the third party hosts and not about traffic; a migration cares about the data
and about neither. One averaged number hides whichever of those is yours,
which is why the score comes after the table and carries its own definition
every time it is printed.

## Third party reproduction is mostly low today, and says so

Only one mock pack ships, for Stripe. A host in `mock` mode with no pack
answering it is `absent`, and the report says exactly that: every request to it
is refused with a 404. A host in `mock` mode with a pack that keeps what was
created is a better reproduction than one whose pack returns canned answers,
and both are better than a host the policy blocks. The report distinguishes
all three rather than averaging them into one word.

## A second datastore, and what the report says about it

There was one golden, one masking pass, one verification scan and one branch,
and all four were Postgres, so a ClickHouse, a Redis, a Kafka or an
Elasticsearch declared as a service started as an empty container. For a stack
shaped like an analytics product that was the whole product: the twin held
masked Postgres metadata and zero events, because the events are in ClickHouse.
Every query path that mattered was untested and every chart was blank.

The inventory used to score that environment on its services, its branch, its
hosts, its personas and its traffic and call it faithful, because none of its
dimensions was looking at the second store. The `datastores` dimension is that
absence, written down.

A store declared `golden` is now refreshed, masked, verified and branched like
the primary, so the twin can hold the events as well as the metadata. The
dimension is kept and it is what tells you WHICH of the two an environment in
front of you is.

Which state a store gets turns on what the manifest declared for it and on what
the environment then did about it.

A store declared `golden` that this environment BRANCHED is reported from the
branch, in two components exactly like the primary database's: `data` says how
many tables and rows it holds and which golden it came from, and `provenance`
says whether that golden's signed attestation still matches its own signature.
That is the report reading the environment. It was worth writing down here
because the dimension used to read the declaration alone, so it called a store
holding a masked, verified copy of production `absent`, which understates a
twin rather than overstating one and is still wrong.

That `data` component is `unmeasured` rather than `reproduced`, and the report
says why: nothing here records what production's second store holds, so
whether the branch reproduces it is unknown. It is the rule the primary
database already follows, arriving one dimension lower. A golden copied from
whatever `source_url_env` names carries exactly the uncertainty that made a
branch of two hundred rows report as reproducing a production of four billion,
and the answer to it for the primary database, the committed volume profile
under `database.volume`, has no equivalent for a second store yet. The report
names that rather than counting the store as a copy of production nobody
checked.

A store declared `golden` that nothing branched is `absent`, and it is counted.
The manifest asked for a masked, verified copy of production in it, this
environment has none, and nothing has to read a ClickHouse to know that nothing
branched a golden for it. That is a fact about the environment rather than a
gap in what can be seen, so it belongs in the denominator, and the report names
the four things that are missing: no golden, no attestation, no tables and no
rows.

A store declared `empty`, `derived` or `topics_only` is `substituted` when this
environment did what the stance asks and the store is running. Not
`reproduced`, because none of the three is production's data and the whole
argument for the stances is that it should not be: an empty cache holds nothing
production holds, a rebuilt index holds documents built from the branch, and a
broker created with topics and consumer groups holds no message at all.
`substituted` is what this report means by something that stands in and behaves
without being the real thing.

That counts, in the denominator, and the score goes down for declaring a cache
empty. It should. The alternative is what these three used to be, `unmeasured`,
which held them out of the number in both directions, so a twin of a product
whose events live in Kafka scored the same whether its broker held the declared
topics or was an empty container nobody had touched. The declared `because` is
carried through as written beside the state, so the reader sees a position
rather than a gap, and a store with no reason declared says so.

Two things are observed rather than read off the manifest. A store whose
service is not running is `absent`, whatever the manifest says about it: the
manifest asked the environment to hold a store and it does not hold one. And a
store that is running for which this environment's own run recorded no such
job stays `unmeasured`, with the report saying to run `af up` again. That
second one is the question a manifest cannot answer at all: an environment
brought up by a build with no stance jobs runs the same services from the same
file with a broker that has nothing in it, and the run journal is the only
thing that records what a particular run actually did.

That is the number going down on purpose. A stack shaped like an analytics
product scored 100 percent before, and scores 89 after, on the same
observation, because the one store the product is about is now in the
denominator. The same stack with that store actually branched scores 100 again,
with the two components that would need production's own row counts excluded
and named. `just benchmark` runs the harness that produced all three.

A store is recognised two ways. A [declared datastore](/docs/reference/manifest)
is the better one, because it carries the stance somebody chose for it and the
report says which: a store declared `empty` reads as a decision, with the
reason written beside it, rather than as a container nobody looked at. The
entry named `primary` is left out here, since the `database` dimension above
measures it properly.

A store nothing declares is still recognised from the image a service runs or
from what the service is called, so an old manifest is not silently reported as
having no second store at all. One whose image this build does not recognise
and whose service carries an unrelated name is invisible to both signals, and
the dimension says which two it used when it finds none.

## One instance of every service, and the report that could not see it

`replicas` was a manifest field nothing read until both runtimes honoured it.
A manifest asking for three instances silently ran one, and the `services`
dimension called that service `reproduced`, because it asks whether a service
is up and stops there. So every bug that only appears above one instance was
invisible in the one report whose job is to say what a twin does not reproduce:
leader election, a queue processed twice, a cache coherent with one instance
and not two, a sticky session assumption, a migration safe against one writer.

The `topology` dimension counts instances against the count each service asked
for.

```
topology       absent (2 absent, 2 unmeasured)
  web          absent       1 of 3 instances, so anything that only breaks above one instance can still pass here
  worker       absent       1 of 2 instances, so anything that only breaks above one instance can still pass here
  events       unmeasured   this service names no count, so it runs one and nothing says whether production runs one
  cache        unmeasured   this service names no count, so it runs one and nothing says whether production runs one
```

A service that names no count is `unmeasured` rather than `reproduced`. The
manifest's count is the only statement anybody has made about how many
instances a service runs; a service that declares none has made no statement,
and the environment runs one of it because one is what an omitted key means.
Calling that reproduced would put a number in the numerator that nothing
measured, which is the same refusal the `runtime` dimension makes one level up.

When no service in the manifest names a count at all, the whole dimension is
excluded with one line saying so, rather than a row per service repeating it.
That is every manifest written before `replicas` was honoured, and the score
those manifests get is unchanged.

## Requiring a dimension

```yaml
fidelity:
  enabled: true
  require: [database, services]
```

`af fidelity` exits 6 with `AF-FID-001` when a required dimension was measured
and some component of it was not reproduced.

It exits 1 with `AF-FID-002` when a required dimension could not be measured,
which is neither met nor broken. The two are separate on purpose. A dimension
measured and found wanting is a fact about the environment; a dimension nothing
could measure is a fact about what we could see, and reporting the second as
the first is how a check stops being believed.

`runtime` is reported and is not comparable today, because nothing in the
manifest says what production runs on, so there is no other side to the
comparison. Requiring it fails with `AF-FID-002` saying so.

`datastores` is measurable for a store declared `golden`, and only as far as
the stances go for the rest. A store nothing branched is `absent`, so requiring
the dimension before an `af up` that branches it fails with `AF-FID-001`, which
is a fact about the environment. A store the environment did branch has its
provenance measured and its data reported as an unknown, so requiring the
dimension takes it to `AF-FID-002` naming the `data` component, which is the
honest answer rather than a pass. Any store on another stance is unmeasured and
takes the whole dimension to `AF-FID-002` naming it, for the same reason.

`topology` is measurable for every service that names an instance count, and a
count that is short fails with `AF-FID-001`. A manifest where some service
names no count fails with `AF-FID-002` naming it, and one where no service does
fails the same way with the dimension excluded whole.

Turning the inventory off with `enabled: false` means it is not taken, which is
not the same as everything having passed, and the command says so rather than
printing an empty report. A manifest that disables the inventory and still
names dimensions under `require` is refused: a requirement nothing evaluates
reads in review as a gate that is enforced.
