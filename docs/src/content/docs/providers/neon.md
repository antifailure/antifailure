---
title: Neon
description: Using Neon as the database provider, what it does well, and what it costs.
sidebar:
  order: 2
---

Neon branches share storage with their parent, so creating one takes about as
long for a hundred gigabytes as for a hundred rows. That is the reason to use
it: with the Docker provider, branch time grows with the database, and with
Neon it does not.

## Configuration

```yaml
database:
  provider: neon
  version: 17
  project: dawn-river-12345678
  api_key_env: NEON_API_KEY   # the default; name a different variable if you use one
  max_branches: 10            # your plan's limit
```

`project` is the Neon project branches are created in. It is not a secret, so
it lives in the manifest. The API key is, so the manifest names the variable
that holds it and never the value. The key is looked up through the same chain
as everything else: an exported variable, then `.env`, then the local store.

This provider does not create projects. A project is a billing boundary, and
creating one on your behalf is not a decision a tool should make.

Point it at a project that holds nothing else. Everything it creates is named
`af-`, and it ignores branches that are not, but a project shared with
production work is a project where somebody eventually reads the wrong branch
name.

## What it creates

| Name | What it is |
| --- | --- |
| `af-cand-<version>` | A golden being built. It exists for the minutes between creating the branch and publishing it. |
| `af-gv-<version>` | A published golden: masked, scanned, and branchable. |
| `af-env-<environment>` | One environment's database. |

Publishing is the rename from `af-cand-` to `af-gv-`, and it happens only after
verification returns without an error. Nothing else marks a golden as
publishable, so a refresh that dies at any point leaves a candidate that
nothing will branch.

The reason it is a rename and not a flag: Neon accepts an annotation when a
branch is created and ignores one sent afterwards, and the attestation does not
exist until the candidate has been masked and scanned. A rename is the one
atomic thing available at the right moment.

## Where the attestation lives

Inside the golden, in a table:

```sql
SELECT version, rules_hash, created_at, attestation
FROM _antifailure.golden;
```

In the database rather than beside it, because a verification statement is
about that data and should travel with it. A branch of a golden inherits the
row, so anyone holding an environment can read what was scanned and what was
found without asking the engine.

## Direct and pooled connections

Both are used. Services receive the pooled string; a service's `migrate`
command receives the direct one, and so do golden refreshes and restores,
because a transaction pooler does not support the session level features
migrations and `pg_restore` use. Nothing has to be configured for that: the
engine asks for a pooled string whenever the provider declares it has one, and
uses the direct string for both when it does not.

Worth knowing if you call Neon's API yourself: omitting the `pooled` parameter
does not mean direct. Neon defaults to the pooled host, so leaving it out hands
a pooled connection to something that needed a direct one, and the failure
looks like a restore that half worked. This provider sends it explicitly in
both directions.

## Limits

Neon's branch ceiling is a property of your plan and the API does not report it
on a path this provider can rely on, so `max_branches` states it. Reaching
either that number or Neon's own refusal fails with `AF-DB-006`, naming the
limit, rather than hanging or returning an unexplained 422.

Free tier projects also cap a branch at 512 MB and keep six hours of history.
Both are fine for previews of a small application and neither is enough for a
copy of a real production database.

## Failure and retries

Everything Neon does is asynchronous: creating a branch returns immediately
with operations that are still scheduling, and the branch is not usable until
they finish. This provider waits for its own operations before returning, so a
connection string it hands back is one you can connect to.

Reads and deletes are retried on a transport failure, a 429, or a 5xx. Creates
are never retried: one that timed out may have reached Neon, and sending it
again would make a second branch. Instead, `Branch` looks for an existing one
by annotation before creating, so a retried environment gets the branch it
already has.

## Cleaning up after a killed run

Environments and goldens are removed by `af down` and `af golden gc`, and
`af env prune --older-than 24h` does the first in bulk.

Candidates are the one thing removed without being asked. A candidate is a
branch that exists for the minutes between starting a refresh and publishing
it, and nothing ever branches from one, so a candidate older than two hours can
only be the remains of a process that died. The next refresh removes it.

If a run was killed in a way that left an environment branch behind, it is
still named `af-env-<environment>`, so `af env list` and `af down` reach it.

## Conformance

This provider passes the shared database conformance suite against the real
Neon API, not a fake. To run it yourself against your own project:

```sh
export AF_NEON_API_KEY=napi_...
export AF_NEON_PROJECT_ID=dawn-river-12345678
go test ./engine/internal/db/neon -run TestConformance -v -timeout 40m
```

It creates and deletes branches in that project and asserts at the end that it
left nothing behind. If a run is killed, `AF_NEON_SWEEP=1 go test
./engine/internal/db/neon -run TestSweepLeftovers` removes what it made.
