---
title: The journal
description: Why every resource is recorded before it is created, and what that buys.
sidebar:
  order: 8
---

Antifailure writes down what it is about to create before it creates it, and
what it has removed after it removes it. That record is the journal, and it is
what makes "nothing outlives an environment" a property rather than a hope.

```
intend  container web        ──> create it  ──> confirm
intend  network  inner       ──> create it  ──> confirm
intend  branch   env-pr-41   ──> create it  ──> confirm
```

The order matters. A process killed between intending and creating leaves a
record of something that may or may not exist, and teardown can check. A
process killed after creating and before recording would leave a resource
nobody knows about, which is the leak this ordering prevents.

## Teardown reconciles

`af down` walks the journal, removes each resource, and confirms each removal
against the provider. It does not trust the record: a resource the journal
knows about and the provider does not is fine, and a resource the provider has
and the journal does not is reported.

```
AF-RUN-030 The environment could not be torn down completely; 2 resources are
still recorded.
  Next: Run 'af down' again once the provider is reachable; the journal
  remembers what is left.
```

Running it again is safe and is the answer. Teardown is idempotent by
construction: removing something already gone succeeds, in every provider,
because the conformance suite has a behaviour that requires it.

## Leak detection

```sh
af env list                        # what exists, read from the daemon
af env prune --older-than 0        # remove all of it
af env prune --older-than 24h --dry-run   # or see what would go first
```

The check that matters compares what the provider holds against what the
journal recorded. Anything the provider has and the journal does not is
something that escaped, and that is the failure the whole design exists to
catch. The conformance suite runs it after every provider's suite, and it has
caught a provider leaking a golden per refresh.

## The lock

```
AF-RUN-003 Another Antifailure process holds the lock for this branch (process
4821, since 12:04).
  Next: Wait for it to finish, or stop it and run 'af down' to clean up.
```

One environment per branch per machine. Two runs would race on the same names
and both fail in ways neither explains. The lock names the process and when it
took it, so a stale one is recognisable.

## When the state database is damaged

```
AF-RUN-011 The local state database at ~/.antifailure/state.db is corrupt.
  Next: A backup was written to ~/.antifailure/state.db.bak. The database was
  rebuilt, so it now tracks nothing: run 'af env list' to see what is still
  running and 'af env prune --older-than 0' to remove all of it.
```

The old file is kept rather than deleted, and the reconcile is the important
half: a rebuilt journal knows about nothing, so anything still running is now
untracked. `af env list` reads the daemon rather than the journal, which is what
makes it the right tool here, and `af env prune --older-than 0` removes what it
finds. That is the one situation where reading the provider matters more than
reading the record.

## Where it lives

`.antifailure/` in the repository, next to the manifest. Per repository rather
than per user, because the lock that stops two `af up` runs racing on one branch
lives here, and a directory shared between checkouts would put two repositories'
environments in one lock namespace. `af doctor` prints the path it is using.

It is local state and belongs in `.gitignore`, which `af init` adds. It holds no
secrets: connection strings are resolved when needed and never written down.

Related: [the local runtime](/docs/guides/local-runtime/), [providers](/docs/providers/overview/).
