---
title: Supabase
description: Using Supabase as the database provider, what a branch really is, and what it costs.
sidebar:
  order: 3
---

A Supabase branch is a whole separate project: its own Postgres, its own API
keys, its own storage. That makes environments genuinely isolated from each
other and from production, and it makes them empty. Supabase creates a branch
with no data on purpose, so this provider copies the golden's rows into it.
Branch time is therefore the time to copy your data, not a constant.

That is the trade against [Neon](/docs/providers/neon), where branches share storage
with their parent and branch time is flat. Choose Supabase when your application
already lives there, because an environment that is a real Supabase project has
the Auth, Storage and Realtime services your application is calling.

## Configuration

```yaml
database:
  provider: supabase
  version: 17
  project: abcdefghijklmnopqrst
  api_key_env: SUPABASE_ACCESS_TOKEN   # the default; name a different variable if you use one
  max_branches: 5
```

`project` is the project reference branches are created in, the twenty character
string in your dashboard URL. It is not a secret, so it lives in the manifest.
The token is, so the manifest names the variable that holds it and never the
value.

Branching requires a paid plan. `version` may be 15 or 17; anything else is
refused before a branch is created, with a message naming what would work.

### The token is account wide

Supabase has no per project Management API credential. A personal access token
reaches every project in every organisation you belong to, so treat it as one:
keep it in the secret store rather than a file, and revoke it when a machine is
finished with it.

The containment is in this provider rather than in the credential. Every call
names the configured project, and the only branches it will read, write or
destroy are those whose names carry its own prefixes and that are not the
project's default branch. That last exclusion is load bearing and is explained
under [What it creates](#what-it-creates).

Point it at a project that holds nothing else.

### When the token is refused

Supabase answers 401 whether the token is revoked, expired, mistyped, or absent,
and the message it returns for a string that is not a token at all is "JWT could
not be decoded" rather than anything about authorization. So the provider says
which credential was refused and where to issue another one instead of passing
the status through, and it does not ask again: the same token cannot be accepted
on a second attempt, and retrying turns an instant failure into a slow one.

A token that is valid but cannot see the project is a different answer, 404, and
it reads as a project that is not there rather than a credential that was
refused. If every call reports a missing project, check `project` before you
reach for a new token.


## What it costs

A branch is a running project and is billed by the hour, at Micro compute
roughly $0.0134 an hour, about $10 a month if you leave one up. Compute credits
do not apply to branch compute. Branches are also outside the spend cap.

The practical consequence is that `af down` is not tidiness, it is the bill. So
is the leak detector, and so is the sweep described below.

## What it creates

| Name | What it is |
| --- | --- |
| `af-cand-<version>` | A golden being built. It exists for the minute between creating the branch and publishing it. |
| `af-gv-<version>` | A published golden: masked, scanned, and branchable. |
| `af-env-<environment>` | One environment's database. |

Publishing is the rename from `af-cand-` to `af-gv-`, and it happens only after
verification returns without an error. Nothing else marks a golden as
publishable, so a refresh that dies at any point leaves a candidate that nothing
will branch, and a candidate more than two hours old is swept on the next
refresh.

Everything else in the project is left alone, including branches somebody made
by hand. One of those deserves naming: **the first branch ever created on a
project also registers a row for production itself**, called `main`, with
`is_default` set. It appears in every branch listing from then on. This provider
never treats a default branch as its own, whatever it is called.

Branches are created persistent. An ephemeral Supabase branch is paused after
inactivity and deleted when its pull request closes, and an environment has
neither a pull request nor a tolerance for its database quietly stopping.

The consequence is that deleting one takes two calls: Supabase refuses to delete
a persistent branch, so the provider clears persistence and then deletes. If you
are cleaning up by hand, that is the order.

## What a branch is filled with

A copy between two Supabase databases is not a plain `pg_dump` into
`pg_restore`, and the reasons are worth knowing before you debug one.

The platform owns `auth`, `storage`, `realtime`, `graphql`, `extensions`,
`vault` and others in the source **and** in the target, so a whole database copy
fails immediately on `schema "auth" already exists`. Those schemas are excluded.
So are the publication and the six event triggers Supabase creates, which exist
in both databases and are owned by a role you are not.

What travels is your own schemas, plus the rows of two tables the platform owns:

- `auth.users`, because the commonest shape in a Supabase application is a table
  with a foreign key to it. Without those rows the restore reaches the foreign
  key, fails to validate it, and carries on: the data lands and the constraint
  does not. A golden published from that has referential integrity that silently
  is not there.
- `auth.identities`, because a user without one cannot sign in, which makes a
  persona a row rather than an account.

Nothing else from `auth` travels. `auth.sessions` and `auth.refresh_tokens` in
particular do not, and that is deliberate: a session token is not personal data
by any rule the verification scanner applies, so masking would not touch it, and
a golden carrying live sessions would hand anybody who can reach a branch a
working login as a real customer.

Because `auth.users` rows do travel into the golden, **your masking rules have
to cover them**. If they do not, verification finds the addresses and the
refresh fails with `AF-MSK-002` naming the column. That is the intended
outcome; a golden is not published either way.

The list of platform schemas is not hardcoded alone. It is a known set combined
with whatever the source database says is owned by one of Supabase's own roles,
so a schema Supabase adds after this was written is excluded without waiting for
a release. Your own schemas belong to `postgres` and are never caught by it.

### Why the branch is emptied first

Before a golden is restored, the provider drops the application's objects in the
target and leaves the schemas themselves in place. It does that on every
restore, not only on a reset, because a branch is not reliably empty when it is
created: a project with migration history gives its branches the migrated schema,
and restoring a golden's version of the same tables on top of that fails.

It does not run `DROP SCHEMA public CASCADE`, and neither should you. Supabase's
grants to `anon`, `authenticated` and `service_role` are partly default
privileges keyed to that schema, so dropping it takes them with it and every
table you create afterwards is invisible to the REST API, with nothing in any
log to say why.

## Reset

`Reset` is this provider's own rather than Supabase's. The platform's branch
reset returns a branch to its migration history, which is not the golden's state
and would discard the data the environment was given. Reset here empties the
branch and restores the golden, which is the same path a first branch takes,
sequences included.

## Direct and pooled connections

Both are real and they differ. Migrations and restores get the direct string on
port 5432; services get the transaction pooler on 6543, whose user is
`postgres.<branch-ref>`.

Supabase's pooler endpoint returns a connection string with the literal text
`[YOUR-PASSWORD]` where the password belongs. This provider assembles the string
from the pooler's fields and the branch's own password rather than handing that
one through, and it refuses to hand out a read replica as the pool.

## Personas

Supabase owns `auth.users` through GoTrue, and a user written directly as a row
is not an account that can sign in. A golden carries the rows so that foreign
keys resolve; creating a persona that can actually log in is the job of the
Supabase auth adapter, described in [Personas](/docs/guides/personas).

## Where the attestation lives

Inside the golden, in a table, so it travels with the data it describes:

```sql
SELECT version, rules_hash, created_at, attestation
FROM _antifailure.golden;
```

A branch restored from a golden carries the row, so anybody holding an
environment can read what was scanned and what was found without asking the
engine or the Supabase API.

## The API acknowledges writes before it can read them back

Two windows, both found by running the conformance suite repeatedly rather than
once, and both worth knowing if you automate against this API yourself.

Creating a branch answers 201 with an identifier, and asking for that identifier
can answer 404 for the next few seconds. Reading that as "the branch does not
exist" fails a refresh four seconds in.

Renaming a branch answers 200 with the new name while the branch LISTING still
carries the old one. Publishing a golden is a rename, so a caller that branched
in that window was told its golden had no valid verification attestation. The
golden was verified. The listing had not caught up, and the operator would have
been sent to look at their masking rules.

This provider waits out both, for a minute each, and treats exceeding that as a
real failure rather than waiting longer.

## When a run is killed

A killed run can leave branches behind, and branches cost money, so there is a
sweep:

```
AF_SUPABASE_SWEEP=1 \
AF_SUPABASE_TOKEN=... \
AF_SUPABASE_PROJECT_REF=... \
go test ./internal/db/supabase -run TestSweepLeftovers -v
```

It removes only branches carrying this provider's prefixes, never the default
branch, and it proves the project is clean afterwards rather than reporting
success and leaving you to check the invoice.

## What was proven, and how

Every behaviour in the shared conformance suite passes against the real
Supabase Management API, on a project created for the purpose. Not against a
fake: a fake would have agreed that a persistent branch can be deleted, that a
database copies cleanly into another one, and that the pooled connection string
you are given can be connected to. None of those is true.
