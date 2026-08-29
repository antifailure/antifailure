---
title: Signing in from a terminal
description: af login uses the device grant, so the token is never shown, copied, or typed.
sidebar:
  order: 14
---

`af login` signs this machine in to a control plane. It prints a short code,
opens a browser, and waits while you approve it somewhere that already has a
session.

```
$ af login

  Your code is  BCDF-GHJK

  Approve it at https://app.dev.antifailure.dev/device

  Waiting for approval...

  Signed in as somebody in antifailure (admin)
  Token stored in the operating system keyring, under "antifailure"
```

Then:

```
$ af whoami
  somebody in antifailure
  role           admin
  control plane  https://app.dev.antifailure.dev
  scopes         environments.view, runs.view, events.write
  expires        2026-11-26T04:12:09Z
  credential     the operating system keyring, under "antifailure"
```

## Why not paste a token

A token you paste has to exist before you paste it. It is created in a browser,
selected with a mouse, put on a clipboard every other application can read, and
pasted into a shell that writes it to a history file. The credential is exposed
four times before it is ever used, and the history file outlives the session.

The device grant never shows the token to a person. The terminal receives it
over TLS and writes it straight to the credential store.

## What the token can do

By default: reading environments and runs, and writing events. It cannot manage
members, change policy, or touch a provider key.

That default is the point. A laptop signed in months ago and since lost holds a
token that can read what happened and record what it did, and nothing that costs
money or changes a secret.

### Asking for more

`--scope` asks for a capability beyond the default:

```
af login --scope providers.write
```

The scopes that exist are `environments.view`, `runs.view`, `events.write`,
`providers.view` and `providers.write`. A name that is not one of those is
refused in the terminal, before a code is printed, rather than producing a token
that cannot do the thing you asked for.

What you asked for is shown on the screen where the login is approved, so nobody
grants provider-key management without reading the words.

`providers.write` lets a terminal store, rotate, remove and cap a key. There is
no scope that reads one back, and there is no route that would serve it. See
[Your own provider keys](/docs/guides/provider-keys/).

Scope is decided by the control plane from a closed list and is recorded when
the login starts, so approving cannot widen it and asking for something that
does not exist grants nothing. The organization comes from the session of
whoever approves: a terminal cannot ask to be let into a tenant.

Scope is also not the only check. It says what the token may do; your role says
what you may do, and both have to allow an action for it to happen.

Tokens expire after ninety days. `af whoami` says when.

## Where the credential is kept

On macOS, the operating system keyring, under the service name `antifailure`.

On Linux and Windows there is no keyring the engine can use yet, so the token
goes in `~/.antifailure/credentials/`, in a file with mode `0600` inside a
directory with mode `0700`. `af login` says which of the two happened rather
than leaving you to find out, because a credential protected only by file
permissions is a different thing from one the operating system is protecting.

Neither is inside your repository. Nothing reads or writes a token in the
working tree, so there is nothing for a commit or a support bundle to pick up.

One entry per control plane, so signing in to staging does not sign you out of
production.

## Where the token is read from

In this order:

1. `AF_CONTROL_PLANE_TOKEN` in the environment.
2. The credential `af login` stored.

The environment wins because exporting a token is somebody deliberately
overriding what is on the machine, usually to debug or because they are in CI.
CI should use an engine token in the environment and not `af login`: the device
grant needs a person, by design.

## Signing out

```
$ af logout
  Removed the credential for https://app.dev.antifailure.dev.
  The token is revoked, so a copy of it is no longer valid anywhere.
```

Both halves happen. Removing it locally stops this machine using it; revoking
it stops anybody who copied it. If the control plane cannot be reached, the
local credential is still removed and the command says the revocation did not
happen, so nobody is left believing a token is dead when it is not.

`af logout` clears both the keyring and the file, because a machine can hold
both if a login happened before the keyring worked.

## When it stops working

A token stops identifying you the moment your membership is removed, even
though the token itself is neither expired nor revoked. `af whoami` reports
that and tells you to sign in again, rather than showing a role you no longer
have.

## On a machine with no browser

The short code is the point. Run `af login --no-browser`, read the code off
this terminal, and approve it in a browser anywhere else, including a phone.

```
af login --no-browser --control-plane https://app.dev.antifailure.dev
```

The code contains no character that is easy to misread: no `O` or `0`, no `I`,
`L` or `1`. It is good for fifteen minutes.
