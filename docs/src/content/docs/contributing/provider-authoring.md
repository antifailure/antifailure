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

## What you import

Four packages, and no others. They are the four the
[stability page](/docs/reference/stability) names as stable, and they are
stable together because an interface is only as usable as the types its
signatures name.

| Package | Why you need it |
| --- | --- |
| `engine/pkg/provider` | The interface you implement. |
| `engine/pkg/secret` | `Database.ConnString` returns a `secret.Value`, so you have to name the type. Build one with `secret.New`; it renders as `[redacted]` through every path that turns a value into text. |
| `engine/pkg/schema` | The manifest types the interfaces carry. |
| `engine/conformance` | The suite. |

Anything under `engine/internal` is not importable from your module, and that
is the toolchain refusing it rather than a convention. If you find yourself
needing something in there, that is a gap in these four packages worth raising
rather than a barrier to work around.

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

## A worked example, in one sitting

`engine/internal/testutil/fakes/inmemory.go` is a complete
`provider.Database` in 180 lines, with no database behind it. It is the
shortest thing in the tree that passes the suite, and it is worth reading
before you write your own, because it makes the shape of the interface
obvious without any of a real service's noise.

Four things in it are worth copying rather than inventing.

**It declares only what it can do.** `Capabilities()` returns
`provider.Caps{Branching: true}` and nothing else. It has no rows, so it does
not claim `Reset`, and it has no pooler, so it does not claim pooled
endpoints. The suite skips those behaviours and says which capability was
missing as it skips them.

**It refuses rather than pretends.** Branching from a version that does not
exist, or from one that failed verification, returns an error. A provider that
invents a branch for a golden it does not have will pass a shallow test and
lose somebody's data on the real one.

**Destroy is idempotent, and so is everything teardown touches.** Removing a
branch twice succeeds, because teardown retries and a crash leaves a partial
state. It keeps a `destroyed` set for exactly that.

**Health answers rather than errors.** A destroyed branch is unreachable, not
a failure. `af down` asks for health, and a provider that errors on a branch it
has just removed makes a successful teardown look like a failure.

It is also the provider the suite's own negative controls run against, which
is the other reason it exists: a control that needs infrastructure gets
skipped, and a skipped control is a false green rather than a proof. That is
the subject of the next two sections.

## Making the engine use it

A provider nobody can select is a provider nobody has. Until a build knows the
name `mine` means your code, `database.provider: mine` is refused, and being
refused is the correct behaviour: falling back to `docker` would hand somebody
an empty preview with no reason for it.

Registration is how a build says so, and it needs no change to this repository.
`engine/pkg/extension` holds the sockets and `engine/pkg/afcli` runs the same
command tree the `af` binary runs, so your `main` is a few lines around both:

```go
package main

import (
	"context"
	"os"

	"github.com/antifailure/antifailure/engine/pkg/afcli"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

type registration struct{}

func (registration) Name() string { return "mine" }

func (registration) Open(
	ctx context.Context, cfg extension.DatabaseConfig,
) (provider.Database, error) {
	// cfg carries the manifest's database block, the resolved Postgres major
	// version, a state directory, the engine's clock, and Lookup, which
	// resolves a declared credential through the engine's whole chain. Read
	// credentials through Lookup rather than from the process environment, so
	// that every one your provider uses is declared and auditable.
	key, found, err := cfg.Lookup(ctx, cfg.Database.APIKeyEnv)
	if err != nil || !found {
		return nil, err
	}
	return myprovider.New(key, cfg.Database.Project)
}

func main() {
	extension.Default.AddDatabaseProvider(registration{})

	ctx, forced, stop := afcli.WithSignals(context.Background())
	defer stop()
	os.Exit(afcli.Run(ctx, forced, os.Args[1:], afcli.Options{}))
}
```

Five things can be registered: `AddDatabaseProvider`, `AddDatastoreProvider`,
`AddRuntimeProvider`, `AddGoldenStore` and `AddEmulator`. Three of them are
selected by the engine today. `AddDatastoreProvider` and `AddEmulator` have no
lifecycle behind them yet, because the manifest declares one datastore and no
egress rule can name an emulator, so registering either of those does nothing
beyond appearing in `af license status`. Each socket says so in its own
documentation rather than leaving you to discover it.

Four rules are worth knowing before you rely on this.

**A registration adds a choice and never replaces one.** The engine asks its
own providers first and the registry only afterwards, so registering under a
name this build already has would never be used. That is refused at the first
command rather than ignored, because a build somebody believes replaces the
Docker provider and silently does not is worse than one that will not start.

**A registered provider is checked exactly as a built in one is.** Masking,
verification, provenance and the egress policy all live above the provider.
Nothing here is a way around them.

**A refusal names what this build does have,** registered providers included,
so a misspelling in the manifest is answered by a message that mentions your
provider rather than one that lists only the four that ship.

**Run the conformance suite anyway.** Registration decides which provider is
selected. It says nothing about whether that provider keeps its promises, and
the suite is the only thing that does.

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

## Writing a datastore

`provider.Database` is Postgres and there is one of it. Everything else an
environment holds is a `provider.Datastore`: a ClickHouse, a Redis, a Kafka, a
search index. The interface is deliberately smaller, because a second store has
no pooled endpoint, no reset and no golden pool of its own to enumerate.

```go
func TestMyStore(t *testing.T) {
    conformance.RunDatastore(t, factory, conformance.DatastoreOptions{})
}
```

`conformance.DatastoreBehaviors()` lists what it checks. The suite checks the
CONTRACT rather than the contents, because the interface covers stores whose
only shared query language is none: that a refresh masks before it verifies and
publishes nothing when verification fails, that branching twice for one
environment produces one branch, that destroying twice succeeds, that a
connection string is a secret. Your own store's contents are the subject of
your own package's tests, where there is a client that can read them.

**A store that holds no golden is not a broken one.** A cache is correct to
start empty, and the manifest says so with `stance: empty`. Declare
`Golden: false` and answer `provider.ErrNoGolden`, and the suite runs the
behaviours that shape can pass and skips the rest by name. A generic error
there is the thing to avoid: the engine cannot tell it from a broken
connection, so a declared stance becomes a failure.

The suite ships with its own fake and its own self test, in
`engine/conformance/datastore_selftest_test.go`. Every behaviour has a flaw
pointed at it and a test that fails if adding a behaviour does not add one, so
"this assertion has been shown to go red" is something a test says rather than
something a reviewer hopes.

One of those controls is contrived and says so in place. `ConnString_IsASecret`
is enforced by the type, exactly as described above, so the only way to reach
the observation the assertion looks for is a value whose plaintext IS the
redaction marker. It is kept because the suite checks the rendering rather than
trusting the signature, and a signature that stopped returning `secret.Value`
would make it violable for real.

## Before you open a pull request

Run `just gate`. It runs everything CI runs, in CI's order, so a green gate
means a green CI.

If your provider talks to a hosted service, say in the pull request which
behaviours you ran against the real thing and which you did not. `written` and
`proven` are different words here and the distinction is kept on purpose.
