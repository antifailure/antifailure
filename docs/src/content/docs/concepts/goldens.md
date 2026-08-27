---
title: Goldens
description: The masked, verified copy every environment branches from, and why it is immutable.
sidebar:
  order: 1
---

A golden is one masked, verified copy of your production database. Every
environment gets a branch of one. Nothing branches from production, and nothing
branches from a golden that has not been verified.

```
production ──copy──> candidate ──mask──> ──verify──> golden
                                                       │
                              ┌────────────────────────┼────────────────┐
                              ▼                        ▼                ▼
                          env for PR 41           env for PR 42    env for PR 43
```

## Versions are immutable

A refresh produces a new version. It never rewrites an existing one.

That is not tidiness. An environment that branched an hour ago has to keep
seeing the data it branched from, or a test that passed becomes a test that
fails for a reason nobody can reproduce. A version is identified by
`gv_<timestamp>_<hash>`, so sorting by name sorts by age, and the hash makes
two refreshes in the same second distinct.

```sh
af golden list          # what exists, newest first
af golden refresh       # build a new version from the source
af golden verify <ver>  # rescan an existing one
af golden gc            # remove versions nothing came from
af golden pull [ver]    # bring a published one onto this machine
```

## Refreshing

```yaml
database:
  source_url_env: PRODUCTION_DATABASE_URL   # read once, never stored
  golden:
    schedule: "0 6 * * *"
    max_age: 24h
    retain: 5
```

`source_url_env` names the variable, not the value. It is read on the machine
running the refresh, used for one `pg_dump`, and never written anywhere an
environment can reach.

A refresh with no source configured still produces a golden. It is empty, your
migrations create the schema, and everything else works. That is the honest
starting point for a repository that has not connected production yet, and the
manifest says so where it would otherwise be silent.

### The schedule, and what a cron expression means without a daemon

`schedule` is a five field cron expression, optionally prefixed with a zone:

```yaml
    schedule: "CRON_TZ=Europe/London 0 3 * * *"
```

The zone is worth setting. Three in the morning means three in the morning where
the team is, and a schedule kept in UTC drifts an hour twice a year against the
one thing it was chosen to avoid, which is being awake for it.

There is no daemon. Nothing on your laptop is waiting to fire it. Instead, the
next command that would use a golden asks whether one came due since the last
refresh, and does it first, saying why:

```
refreshing the golden first: the schedule 0 3 * * * came due
```

Two details that only matter twice a year, and both are tested against the real
transition timestamps:

- When the clocks go **forward** and the time you named does not exist, the
  refresh happens at the first instant the clock reaches. `30 2 * * *` in New
  York runs at 03:00 on the day it jumps, rather than being skipped for the year
  or, worse, running an hour early.
- When the clocks go **back** and the hour repeats, it runs **once**.

### max_age

```yaml
    max_age: 24h
```

If the newest golden is older than this when an environment comes up, it is
refreshed first. A golden that has drifted far enough from production is one
that is testing last quarter's data, and `max_age` is where you say how far is
too far. Leave it unset and nothing is ever refreshed on your behalf.

### retain

```yaml
    retain: 5
```

How many versions `af golden gc` keeps. It is in the manifest so that every
machine and every runner collects the same way; `--keep` overrides it for one
run.

Two versions are never removed whatever the number says. One is any version an
environment is still branched from. The other is the newest verified golden,
because a project with nothing left to branch cannot bring an environment up at
all, which is worse than the disk it saved.

A version that is **not** verified is always collected and never counts against
the number. Nothing can branch it, so keeping it holds disk for something no
environment can use, and counting it would let it push out one that can.

## Publishing, so a fleet reads production once

```yaml
    storage: azure_blob            # or s3, or local
    storage_url: $AF_GOLDEN_STORE  # the variable holding the URL
```

One machine holds the production credential and refreshes. Every other machine
pulls what it published and never reads production at all.

`storage_url` names an environment variable rather than carrying a URL, because
a container URL carries a shared access signature and a bucket URL can carry a
user, and a manifest is committed. For `s3` the credential is not in the URL at
all: it is read from `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, the same
names the AWS tools already use.

Each version becomes two objects, and the order they are written in is the
contract:

```
gv_20260826120000_a1b2c3d4/dump.pgcustom      written first
gv_20260826120000_a1b2c3d4/attestation.json   written second
```

A version with only a dump is a publish that died partway, and it is invisible
to everything that lists the store rather than being offered. A dump with
nothing to check it against is not a golden.

```sh
af golden pull        # the newest complete version
af golden pull <ver>  # a particular one
```

A pulled golden is **not** trusted because it came from the store. The
verification scan runs again, on the machine that pulled it, against the
database that actually arrived. Skipping that would make the store a way to get
an unverified database branched, which is the one thing the product refuses.

The local copy gets a new version identifier, because an identifier carries when
the version was made and this copy was made now. `af golden pull` prints both.

A publish that fails does not fail the refresh. The golden exists and this
machine can branch it; the expensive part, reading production, already
succeeded, and throwing that away because an upload timed out would be the wrong
trade. The failure is printed.

Publishing goes through memory, so it is bounded, and it refuses a dump larger
than the bound rather than swallowing the machine. If you are publishing you
almost certainly want [subsetting](/docs/concepts/subsetting/) as well: a slice is
what makes a golden small enough to move.

## Collection

`af golden gc` removes versions nothing branched from. A version an environment
came from is refused:

```
AF-DB-005 The golden version gv_20260826120000_a1b2c3d4 is still referenced by
2 environments and cannot be collected.
  Next: Run 'af down' on those environments first, or leave the version in place.
```

That refusal is the point. Collecting a referenced version would pull the floor
out from under a running environment, and the failure would arrive later, in
somebody else's test, as a database that stopped existing.

## When a version is gone

```
AF-DB-004 The golden version gv_20260101000000_deadbeef no longer exists.
```

Usually a `--golden` pinned to a version that has since been collected. `af
golden list` shows what is there. Pinning is worth doing when you are chasing a
bug that only reproduces against particular data, and worth removing afterwards,
because a pin is a version that can never be collected.

## When the pool is full

```
AF-DB-010 The storage pool has 1.2 GiB free and the operation needs 4.0 GiB.
  Next: Run 'af golden gc' to reclaim unreferenced versions, or grow the pool.
```

With the Docker provider each golden is an image and they accumulate. `retain`
in the manifest bounds how many `af golden gc` keeps. With a copy on write
provider such as Neon this is rarer, because a branch shares its parent's
storage rather than copying it.

The other answer is to make each golden smaller. See
[subsetting](/docs/concepts/subsetting/).

## What a golden is not

It is not a backup. It is masked, which means it is deliberately not the data
production has. Do not restore one into production, and do not treat a
successful branch as evidence that your backups work.

Related: [masking](/docs/concepts/masking/), [verification](/docs/concepts/verification/),
[subsetting](/docs/concepts/subsetting/), [providers](/docs/providers/overview/).
