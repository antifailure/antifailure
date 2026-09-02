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
| `traffic` | Where the endpoint mix comes from, through the same code the load run uses. |

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

Turning the inventory off with `enabled: false` means it is not taken, which is
not the same as everything having passed, and the command says so rather than
printing an empty report. A manifest that disables the inventory and still
names dimensions under `require` is refused: a requirement nothing evaluates
reads in review as a gate that is enforced.
