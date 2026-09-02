---
title: Secrets
description: Where a value is looked up, and why the manifest holds names and never values.
sidebar:
  order: 12
---

The manifest names variables. It never holds values.

```yaml
database:
  source_url_env: PRODUCTION_DATABASE_URL

egress:
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
```

A manifest is committed. A secret is not. Naming the variable keeps the file
readable, reviewable, and safe to check in, and means somebody reading the
repository can see what credentials an environment needs without holding any of
them.

## Where a value is looked up

In order, most specific first:

1. **This shell's environment.** Somebody who typed an export meant it, and is
   usually debugging.
2. **`.env`.** A repository's file, checked out with the branch.
3. **The encrypted local store.** A file under `.antifailure`, for this
   repository.
4. **The system keyring.** The long lived default on a workstation, shared
   across repositories: the macOS keychain, the freedesktop Secret Service on
   Linux, and the Credential Manager on Windows.

The first source that has the value wins. The order is the point: a temporary
override beats a file, and a file beats a stored default, which is what makes
"try it with a different key" a one line thing.

## Nothing found

```
AF-SEC-001 The variables STRIPE_SECRET_KEY are declared in the manifest but
were not found in any configured source.
  Next: Add them to one of the searched sources: this shell's environment,
  .env (not present), the encrypted local store (no passphrase is set).
```

The message lists every source and why each did not answer, including the ones
that are not available. A message that only said "not found" leaves you
guessing which of three places to put it, and a source that is absent for a
reason is more useful to know about than one that was silently skipped.

## The local store

```sh
af secret set STRIPE_SECRET_KEY   # reads the value without echoing it
af secret list                    # names only, never values
af secret rm STRIPE_SECRET_KEY
```

The value is never taken as an argument. An argument is in the shell history,
in the process list, and in the CI log of whatever ran it.

The file is encrypted with a key derived from a passphrase using Argon2id and
sealed with AES-256-GCM. There is no command that prints a stored value: a store
that can print its contents is one screenshot away from not being a store.

```
AF-SEC-004 The encrypted local store has no passphrase: no system keyring
answered and AF_SECRET_PASSPHRASE is not set.
```

Set `AF_SECRET_PASSPHRASE`, which is what CI does. On a workstation the
passphrase can live in the system keyring instead, so it does not need to be
exported in every shell: the macOS keychain, the freedesktop Secret Service on
Linux, and the Credential Manager on Windows. A machine with no keyring, which
is a Linux server without `libsecret` and most containers, has no other way to
open the store, and the message says which sources it considered rather than
pretending one was tried.

There is deliberately no default passphrase. A store encrypted with a
passphrase everybody knows only looks encrypted.

## A credential that stopped working

```
AF-SEC-002 The credential for Azure Key Vault at https://af.vault.azure.net
was rejected after one refresh: Key Vault answered 403 Forbidden.
  Next: Rotate the credential and store the new value where it reads it. A
  rejection that survives a refresh is a credential that was revoked or was
  never right, so retrying will not help.
```

One renewal, once per process, then reported. Every store that holds a token
which expires gets that one renewal, which covers a long-running process holding
a stale token. A second rejection is not an expiry, and retrying a revoked
credential once per declared variable turns a configuration mistake into a rate
limit on a store everybody else is also using.

This comes from the enterprise secret stores, which are the sources that
authenticate. See [enterprise secret stores](/docs/enterprise/secrets).

## A live key where a sandbox key belongs

```
AF-SEC-003 The value supplied for STRIPE_SECRET_KEY carries a live credential
prefix, and STRIPE_SECRET_KEY is configured for sandbox use.
```

See [sandbox credentials](/docs/guides/sandbox). Checked before anything starts.

## Values never reach a log

Every connection string, token, and key is registered with the redactor when it
is resolved, and everything on its way to a log, an artifact, or a screenshot
goes through it. Redaction happens at the writer rather than at each call site,
because a call site somebody forgot is exactly how a secret ends up in a CI
log.

The engine has five writers that can put an event somewhere a person later
reads it: the local NDJSON log, the spool on disk, a span attribute, the bytes
an OTLP collector receives, and the body of the request the control plane
receives. Each redacts at its own writer, and each has a test that a connection
string cannot reach it. The last of those is the only one that leaves the
machine, so a self-hosted control plane stores events that have already been
through the redactor of the engine that sent them.

You will see this in error messages: `postgres://user:[redacted]@host/db`. That
is working.

## More places to look

An organisation that keeps its credentials in HashiCorp Vault, AWS Secrets
Manager, Azure Key Vault or Google Secret Manager can add them to the end of
this chain with the enterprise edition. They are asked after every local source,
for the same reason the keyring is asked after `.env`. See
[enterprise secret stores](/docs/enterprise/secrets).

Related: [sandbox credentials](/docs/guides/sandbox), [egress](/docs/concepts/egress).
