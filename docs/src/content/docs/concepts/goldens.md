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
in the manifest bounds how many a scheduled refresh keeps. With a copy on write
provider such as Neon this is rarer, because a branch shares its parent's
storage rather than copying it.

## What a golden is not

It is not a backup. It is masked, which means it is deliberately not the data
production has. Do not restore one into production, and do not treat a
successful branch as evidence that your backups work.

Related: [masking](/concepts/masking/), [verification](/concepts/verification/),
[providers](/providers/overview/).
