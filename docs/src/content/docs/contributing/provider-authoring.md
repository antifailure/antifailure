---
title: Writing a provider
description: How to add a database provider, what the conformance suite requires of it, and how to prove it works.
sidebar:
  order: 1
---

Providers are the main extension point, and they are meant to be written by
people outside this repository. A provider decides where an environment's
database comes from: a container on the developer's machine, a branch on a
hosted Postgres, a snapshot on infrastructure you already run.

You do not have to ask permission and you do not have to be a contributor here.
Implement one interface, run one suite, and if the suite is green your provider
does what Antifailure promises its users.

## The shape

A provider implements `provider.Database`. The interface is in
`engine/pkg/provider/db.go` and every method carries the rule it has to keep.
Two of those rules are worth reading before you write any code, because they
are the ones that are easy to miss and expensive to get wrong.

**Every method is idempotent by its identifying argument.** Branching twice for
one environment returns one branch. Destroying something already gone succeeds.
This is not tidiness. The engine retries after a timeout, and a retry that
creates a second resource is how an orphan is made: a database nothing owns,
that nothing will ever clean up, that costs money until somebody notices.

**A version that is not verified is never branched.** Masking is a claim and
verification is a check, and the whole product rests on the check. A provider
that publishes an unverified version, or branches one, has broken the promise
that a preview environment cannot contain real customer data.

## Getting started

```go
import "github.com/antifailure/antifailure/engine/conformance"

func TestMyProvider(t *testing.T) {
    conformance.RunDatabase(t, func(t *testing.T) provider.Database {
        return myprovider.New(...)
    }, conformance.Options{})
}
```

That is the whole harness. It runs twenty three behaviours against your
provider and each one is a property a user depends on.

## Capabilities, and why skipping has to be loud

Not every provider can do everything. A provider without copy-on-write cannot
make branch time independent of database size; a provider without a pooler has
no pooled connection string to hand out.

Say so in `Capabilities()`. The suite reads it and skips the behaviours that
need what you do not have, naming the missing capability as it goes.

Declaring a capability you do not have is the failure worth guarding against,
and it fails loudly: `Capabilities_AreSelfConsistent` checks the declarations
against each other, and the behaviours themselves check the declarations
against reality. A silent skip is how a provider ends up claiming conformance
it does not have, so the suite is built to make skipping visible rather than
convenient.

## Proving the suite can fail

A green conformance run is worth exactly as much as your confidence that the
suite could have gone red. That confidence is not free, and the usual way a
suite quietly stops checking is undramatic: a helper starts skipping, an
assertion starts comparing a value against itself, a behaviour asserts on state
an earlier behaviour already established. All of those still print ok.

So `engine/internal/testutil/fakes` gives you fault injection. `fakes.Break`
takes a provider that works and returns one that violates exactly one
guarantee: publishing an unverified golden, making `Branch` non-idempotent,
making a second `Destroy` an error, under-reporting the inventory.

```go
p := fakes.Break(myprovider.New(...), fakes.BranchIsNotIdempotent)
```

Point the suite at that and it must go red in
`Branch_IsIdempotentByEnvironment`. `fakes.Catches()` maps every fault to the
behaviour that is supposed to catch it. If a fault goes undetected, the suite
has a hole and you have found it.

This is worth doing once for your own provider before you trust a green run.
It takes ten minutes and it is the difference between a suite that passes and a
suite that checks.

## What cannot be broken, and why that is fine

`ConnString_IsASecret` has no fault, deliberately. Connection strings are
`secrets.Value`, whose `String`, `GoString` and `Format` all return the redacted
marker, so there is no value of that type that renders its plaintext. The
guarantee is enforced by the type rather than by the suite.

That distinction is worth carrying into your own code: a rule the compiler
enforces does not need a test, and a rule only a comment enforces needs two.

## Testing against the real thing

Run against a real database. A provider tested only against a fake proves that
your code does what you expected, which is the thing you were least uncertain
about.

The Docker provider is the reference implementation. Its conformance test is in
`engine/internal/db/docker/conformance_test.go` and it is short, because the
suite does the work.

Start the test Postgres with `just db`. It is started with
`pg_stat_statements` preloaded, which matters more than it sounds: without the
preload `CREATE EXTENSION` succeeds, the view exists, and it records nothing,
so tests skip and the suite reports ok.

Two failure modes to watch for, both of which produce a green run that proved
nothing:

- **Skip only for "there is no Docker here".** Any other reason to skip should
  be a failure with the container's log attached. A container that starts,
  publishes a port and then answers nothing is not an absent Docker.
- **An open port is not an accepting database.** The Postgres image runs
  `initdb` against a temporary server and shuts it down before starting the
  real one, so both `nc -z` and `pg_isready` answer yes during a window where
  the next query fails.

## Before you open a pull request

Run `just gate`. It runs everything CI runs, in CI's order, so a green gate
means a green CI.

If your provider talks to a hosted service, say in the pull request which
behaviours you ran against the real thing and which you did not. `written` and
`proven` are different words here and the distinction is kept on purpose.
