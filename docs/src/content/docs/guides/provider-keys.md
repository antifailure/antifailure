---
title: Your own provider keys
description: Store an Anthropic or OpenAI key, cap what it may spend, and rotate it, from the console or a terminal.
sidebar:
  order: 15
---

Runs use your Anthropic and OpenAI keys, not ours. You store one, you cap what
it may spend in a month, and you rotate it when you want to. This page is about
both places you can do that: the console, and `af provider`.

## What is stored, and what is not

The key is sealed with AES-256-GCM under a secret held outside the database,
bound to your organization and to the provider. A row copied to another tenant
does not open. A row edited by one bit does not open.

Beside the ciphertext there are three things a screen may read: the provider,
the last four characters, and a fingerprint. That is deliberately everything a
screen needs, which is what makes the rule keepable rather than aspirational:
nothing has a reason to ask for the key.

The plaintext exists in one function, the one putting it into a request to the
provider. It is not in an event, an artifact, a log line, or a support bundle.

**There is no way to read a key back.** Not in the console, not in the API, not
in the CLI. There is no scope that grants it and no route that returns it.
Storing a secret and retrieving one are different capabilities, and nothing here
needs the second. If you have lost a key, make a new one at the provider.

## A cap comes first

A provider with no budget cannot spend anything. A missing cap reads as zero
rather than as unlimited, because the alternative on somebody else's key is an
unbounded bill.

The cap is checked **before** the key is decrypted. A run with no allowance never
causes the key to exist in the control plane's memory at all, which is the
difference between a cap and a suggestion.

A cap of zero is allowed and means what it says: refuse everything for this
provider. It has to be asked for, though. A blank field or a missing value is
refused rather than read as zero, because a silent cap of zero looks exactly
like a working setup until every run is refused for having no allowance.

## In the console

Organization, then Provider keys. Paste a key, save, set a cap.

Storing, rotating, removing and capping are for owners and admins. Everybody
else sees the same page without the forms: the last four and the fingerprint,
which is enough to tell whether a run was refused for want of a key, and nothing
that would let them change one.

## From a terminal

`af provider` does the same things. It needs a token that asked for the
capability, which a plain `af login` does not have:

```
af login --scope providers.write
```

The scope appears on the screen where you approve the login, so nobody grants
this without seeing the words. See [Signing in from a terminal](/guides/signing-in/).

### What is set

```
$ af provider list

  Provider   Key           Monthly cap                    Spent
  anthropic  ••••••••7777  50.00 USD                      12.50 USD
  openai     not set       none, so nothing may be spent  —
```

### Storing a key

The key is never an argument. There is no `--key` flag and there will not be
one: a secret on a command line is written to your shell's history file, is
visible in `ps` to every other user on the machine, and is captured by any
recording of the terminal. It is exposed three times before it is sent anywhere.

So there are three ways to give it, and none of them put it in the argument
vector.

Asked for on the terminal, not echoed:

```
af provider set anthropic
```

Piped, for a password manager or a file:

```
af provider set anthropic --stdin
```

Out of a named environment variable:

```
af provider set anthropic --from-env ANTHROPIC_API_KEY
```

With no terminal and no `--stdin`, the command refuses and says so. It does not
try to read: a read from a stdin nobody is typing into either blocks forever or
returns nothing at once, and both look like a network problem in a CI log.

### Capping it

```
af provider budget anthropic 50
```

Dollars per month, for the current month. `af provider list` shows what has been
spent against it.

### Rotating

Store the new key. Rotating replaces the old one and revokes it in the same
transaction, so there is never a moment with two live keys and no way to say
which one a run charged.

```
af provider set anthropic --stdin < new-key.txt
```

If the key you give is the one already stored, the command says so rather than
reporting a rotation that did not happen. That is the mistake people make at the
exact moment they believe they have replaced a leaked key.

The old fingerprint stays in the audit log, so it is always possible to say
which key was in use when, without either key being readable.

### Removing

```
af provider rm anthropic
```

Runs that need the provider are refused afterwards, with a message saying why,
rather than falling back to a key of ours.

**This does not reach the provider.** Removing a key here stops us using it and
stops nobody else. If it leaked, revoke it at Anthropic or OpenAI as well.

Removing a key that is not there is not an error. This is a command people run
in a hurry, and a retry after a timeout must not report failure for reaching the
state you asked for.

## Who may do what

| | Console | `af provider` |
| --- | --- | --- |
| See which keys are set | any member | `providers.view` |
| Store, rotate, remove, cap | owner, admin | `providers.write` **and** owner or admin |
| Read a key back | nobody | nobody |

Scope and role are both checked, and they answer different questions. Scope is
what this **token** may do, so a laptop signed in months ago cannot touch a key.
Role is what this **person** may do, so somebody who cannot change a key in the
console cannot change one from a shell either.

## In the audit log

Every store, rotation, removal and cap is an entry, and each one records where
it came from: `web` for the console, `cli` for a terminal. "Somebody rotated the
key while signed in to the console" and "a token on a build machine rotated it"
are the same event without that and different incidents with it.

The entry carries the fingerprint and the last four. It never carries a key,
because it is a record an operator reads, sometimes over somebody's shoulder.

## Self-hosting

Storing a key needs `AF_PROVIDER_KEY_SECRET`, thirty-two bytes, base64. Without
it the control plane serves normally and says it cannot store keys, in the
console and in `af provider list`, rather than accepting one and failing later.

Generate one with:

```
openssl rand -base64 32
```

Keep it outside the database. It is the whole point: somebody with a copy of the
database and no copy of this secret has nothing.
