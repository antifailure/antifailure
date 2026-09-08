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

### Copy on write, which is measured rather than believed

`CopyOnWrite` says a branch shares storage with its golden, so branch time does
not grow with the database. It is the claim a customer is really buying, so the
suite does not take your word for it.

`CopyOnWrite_BranchTimeMatchesTheDeclaration` builds two goldens, one of them
half a gibibyte larger than the other, branches each of them several times
alternately, and takes the fastest of each. Then it asks one question in two
directions:

- Declared `true`, and the larger golden branched measurably slower: **fail**.
  You are copying, and whoever waits for an environment is paying for it.
- Declared `false`, and the larger golden branched no slower: **fail**. You have
  a flat branch time and are not saying so, which puts the wrong row in the
  comparison table a buyer chooses from.

Those two are complementary, so one of the two possible declarations is refused
on every run. There is no reading of the stopwatch that lets both pass, which is
the property a check needs before a green one means anything.

### The third answer, and why the default is unproven

The stopwatch is only as good as the storage under the run, and a fake cloud
control plane over one local Postgres can only hand back a branch carrying the
golden's data with `CREATE DATABASE ... TEMPLATE`, which copies files. On that
harness a truthful `CopyOnWrite: true` fails, and a `CopyOnWrite: false` passes
comfortably. **Both answers are about the harness and neither is about your
product**, so the behaviour has a third one.

```
NOT PROVED BY THIS RUN. This is not a pass.
  UNPROVEN  aurora  CopyOnWrite_BranchTimeMatchesTheDeclaration
```

**`Options.RealService` decides it, and leaving it empty is what produces the
unproven verdict.** It is an assertion of reality, not an admission of
simulation:

```go
conformance.RunDatabase(t, factory, conformance.Options{
    RealService: "the real Neon API, against a real project",
})
```

A field you set to excuse a fake is a field a fake can simply never set, and the
next author writes a control plane, never learns the field exists, and collects a
measured verdict from a run that measured nothing. Forgetting this one produces
the safe answer instead. Set it only when the run really drives the service whose
capability is being decided: a fake control plane over a real local Postgres does
**not** qualify, however real the Postgres is, because what the stopwatch timed
was Postgres.

**It is symmetric.** A run that asserts nothing is unproven whether you declare
`true` or `false`. The false side is the one worth spelling out, because it is the one that
would otherwise ship: a snapshot restore provider declaring `false` against a
copying fake passes comfortably, publishes a certified claim that its service is
not copy on write, and nobody rereads a green check.

**The measurement is not taken.** The verdict is decided before the behaviour
runs, so two goldens and six branches could not change it, and publishing what a
simulator timed invites somebody to quote it as though it were about the product.

**It is not a skip, and it never uses the word.** A skip says this provider makes
no such claim. An unproven says the provider does make the claim and this run
could not reach it. The verdict is reprinted at the end of the run, the ledger in
`engine/conformance/ledger.go` records it per provider, and
`conformance.CopyOnWriteClaim` renders it, so the cell in the published
comparison table reads `unproven` rather than blank. A blank cell is taken for a
pass by every reader in a hurry.

The suite makes a golden large by writing ballast into it from inside the `Mask`
callback, so a provider needs no extra method: hand `Mask` a connection string
that works, which every other behaviour needs anyway, and the sizing takes care
of itself. It also weighs the last branch it makes, because a branch that is
fast because it is EMPTY would otherwise read as one that is fast because it
shares storage.

### What the check can and cannot see, printed every time

The allowance the growth is measured against is not a constant. It is twice the
spread the small golden's own branch times showed during this run, floored at a
quarter of a second: the machine saying how far its readings travel while the
data is held still. On a quiet machine that collapses and the check sharpens;
under load it widens rather than accusing an honest provider of copying.

So the power of the check varies, and every run prints it:

```
  this run could refuse    a copy slower than 0.49 seconds per GiB, and nothing faster
```

Read that line before believing a pass. It is the bound on what the run was
able to see, and a provider whose branch times are erratic gets a weaker bound
than one whose are steady. Raise `Options.CopyOnWriteLargeBytes` if you want a
stronger statement than the one your run printed.

`CopyOnWriteSmallBytes`, `CopyOnWriteLargeBytes` and `CopyOnWriteSamples` tune
the cost for a provider that bills by the gibibyte, and
`AF_CONFORMANCE_COW_LARGE_BYTES` and its two siblings do the same from the
environment for a machine that cannot afford the default. Both have floors, and
both make the run say it was tuned. Against a real service there is deliberately
no way to skip the behaviour: a run that shrank says how far it shrank and what
it could still refuse, and that can be read, where a run that skipped cannot.
The one case that is neither a pass nor a shrunken run is the third verdict
above, and it is not a skip either: it is a verdict, it prints, and it makes the
claim unpublishable.

`ExpectedBranchLatency` is checked the same way.
`Branch_IsWithinTheDeclaredLatency` times the fastest of three branches of the
conformance dataset against the number you declared. Declare what your service
does, not what you hope it does: the number is what the engine plans an
environment around, and a provider that has got slower has to fail here rather
than degrade quietly.

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

## Writing an emulator

An emulator is a declaration rather than an implementation. Antifailure writes
none: LocalStack, Azurite and the vendors' own carry years of fidelity work that
a replacement written here would not have. Register one with
`extension.Registry.AddEmulator` and it supplies a name, the hostnames it
answers for, and a container pinned by digest. A tag is refused, because an
emulator answers for a production API and a tag that moves changes what an
environment was tested against with nothing in the repository changing.

What the engine adds is routing, and it is the whole reason the socket exists.
An egress rule set to `emulate` names your emulator, the engine starts your
container on the environment's inner network, and the sidecar answers for the
provider's own hostname with a certificate the environment already trusts. The
application needs no endpoint override, which is the one thing every other way
of using an emulator costs you.

Three fields on the container exist because one reference implementation is not
a contract, and LocalStack is the reason none of them showed up first: it is a
single image whose entrypoint is the emulator, so it needs none of them.

`Command` decides which emulator you get. Google ships Pub/Sub, Firestore,
Datastore and Bigtable inside ONE Cloud CLI image whose entrypoint is the CLI,
so `Image`, `Port` and `Env` alone describe four identical containers that run
nothing. Azurite needs it too, for a smaller reason with the same shape: it
binds to loopback unless told otherwise, and an emulator listening on 127.0.0.1
answers nothing from the sidecar while looking perfectly healthy in its own logs.

`Companions` are containers your emulator does not work without. Azure's Service
Bus emulator refuses to start without an MSSQL instance beside it. Companions
join the environment's inner network on exactly the terms the emulator does, so
they have no route out either and `Reach` covers them without knowing they
exist. Each carries its own digest and its own `Maintainer`, because a companion
runs beside a copy of production data on the emulator's terms and "it came with
the emulator" is not a provenance. A companion's own companions are refused: one
level is what the known cases need, and a graph here would be a dependency
resolver nobody asked for.

`Maintainer` is declared and never inferred from the registry the image sits in.
A registry path is a fact about hosting and this is a fact about support, and the
two disagree exactly where it matters: `fsouza/fake-gcs-server` is the de facto
GCS emulator and Google does not publish it, because Google ships no GCS emulator
at all. Somebody deciding whether to trust an environment's answers about object
storage should read that rather than infer it from a hostname.

```go
func TestMyEmulator(t *testing.T) {
    conformance.RunEmulator(t, factory, conformance.EmulatorOptions{})
}
```

`conformance.EmulatorBehaviors()` lists what it checks, and none of it is about
whether your emulator implements S3 correctly. That is your emulator's business
and its own project's tests. What the suite checks is the nine promises the
ENGINE makes: that a request inside your declared surface is answered and is not
refused, that an operation outside it comes back in the provider's own error
shape, that the state is enumerable and goes away, that a live credential is
refused before you see it, and that your container cannot reach the internet.

**The subject is the emulator as routed.** Every probe is sent to your own
declared hostname through `RoundTrip`, and nothing in the suite knows your
container's address or may learn it. An implementation that pointed `RoundTrip`
at the container directly would pass all nine behaviours and prove none of them,
because the claim being checked is the routing and not the emulator.

**Declare a covered probe that your emulator really implements.** Two behaviours
read it, and they are separate on purpose: one requires that it is answered at
all, and the other requires that the answer is not a refusal. An emulator that
refuses every request satisfies the uncovered behaviour, satisfies the live
credential behaviour, holds no state to leak and reaches nothing, so it would
pass everything else here and be useless. The second behaviour reads the
response body as well as the status, because AWS returns `200` carrying an error
document for several operations.

`Covered.Creates` false is a legitimate answer. A read only operation is a
perfectly good thing to be covered by, and the two state behaviours skip by name
rather than failing. Declaring `Creates` on a probe that creates nothing turns
`State_IsEnumerable` into a failure nobody can act on.

The suite ships with its own broken emulator and its own self test, in
`engine/conformance/emulator_selftest_test.go`. The rule there is one break per
ASSERTION rather than one per behaviour, because `Fatalf` stops at the first
failure: a behaviour with three assertions and one control has shown its first
can go red and has shown nothing about the other two.

**The containment behaviour is proved twice and it has to be.** `Reach` is a
behaviour in the suite, and the suite's own subject is a fake whose `Reach`
returns whatever the fake decides, so passing it says nothing about Docker.
`engine/internal/runtime/local/emulator_test.go` asks the daemon instead: it
reads back the network the container actually attached to, requires it to be
`Internal`, and attempts an outbound connection from inside the running
container. It carries a control in the same run, reaching the sidecar by name,
because a container that can reach nothing at all fails an escape attempt for
reasons that have nothing to do with containment.

## Before you open a pull request

Run `just gate`. It runs everything CI runs, in CI's order, so a green gate
means a green CI.

If your provider talks to a hosted service, say in the pull request which
behaviours you ran against the real thing and which you did not. `written` and
`proven` are different words here and the distinction is kept on purpose.
