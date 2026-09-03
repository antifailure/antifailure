---
title: Signing in from a terminal
description: af login uses the device grant, so the token is never shown, copied, or typed.
sidebar:
  order: 19
---

`af login` signs this machine in to a control plane. It prints a short code,
opens a browser, and waits while you approve it somewhere that already has a
session.

The console has the whole of this on one screen, under **Command line**: the
install command, the sign-in command already carrying the address of the control
plane you are looking at, and every terminal currently signed in to your
organization.

```
$ af login

  Your code is  BCDF-GHJK

  Approve it at https://app.antifailure.dev/device

  Waiting for approval...

  Signed in as somebody in antifailure (admin)
  Token stored in the operating system keyring, under "antifailure"
```

Then:

```
$ af whoami
  somebody in antifailure
  role           admin
  control plane  https://app.antifailure.dev
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
`providers.view`, `providers.write` and `tokens.manage`. A name that is not one
of those is refused in the terminal, before a code is printed, rather than
producing a token that cannot do the thing you asked for.

What you asked for is shown on the screen where the login is approved, so nobody
grants provider-key management or the ability to mint a credential without
reading the words.

`providers.write` lets a terminal store, rotate, remove and cap a key. There is
no scope that reads one back, and there is no route that would serve it. See
[Your own provider keys](/docs/guides/provider-keys).

`tokens.manage` lets a terminal mint, list and revoke the engine tokens a CI job
presents. It is what running your own control plane needs, and it is the reason the two lists
differ: a token from a plain `af login` cannot make another credential, and
neither can an engine token, so only a person who is an owner or an admin right
now can mint one. See [Connecting an
engine](/docs/self-hosting/control-plane#connecting-an-engine).

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

## What the credential is for

Everything on this machine that talks to a control plane. `af up`, `af test`,
`af ci` and `af workload` report their runs with it, which is what makes an
environment appear under Environments in the console and its events under Runs.
`af env pull`, `af token`, `af provider` and `af whoami` read and write your
account with it.

Signing in changes nothing about where the work happens. The engine still builds
the environment on this machine, from the manifest in this repository, and the
control plane still only receives a record of what happened.

## Where the token is read from

In this order:

1. `AF_CONTROL_PLANE_TOKEN` in the environment.
2. The credential `af login` stored.
3. A GitHub Actions workflow identity, exchanged for a short lived credential.

The environment wins because exporting a token is somebody deliberately
overriding what is on the machine, usually to debug or because they are in CI.
CI should use an engine token in the environment, or the workflow identity, and
not `af login`: the device grant needs a person, by design.

## When the credential cannot be stored

The token is minted the moment somebody approves, before this machine has
written it anywhere. If the write then fails, which on macOS means a keychain
that will not take one, `af login` tells the control plane to revoke the token
it has just been given and says so:

```
Error: store the credential: write to the keyring: the authorization was canceled

       The token that had just been issued was revoked, so nothing was left
       behind. Nothing is signed in
```

That matters because the alternative is a live ninety day credential nobody
holds, nobody can see, and nobody can revoke, with another one beside it on
every retry. If the revocation fails too, the message says the token is still
live and where to go and revoke it: **Command line** in the console lists every
terminal signed in to your organization and takes one away.

## Signing out

```
$ af logout
  Removed the credential for https://app.antifailure.dev.
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
af login --no-browser
```

The code contains no character that is easy to misread: no `O` or `0`, no `I`,
`L` or `1`. It is good for fifteen minutes.
