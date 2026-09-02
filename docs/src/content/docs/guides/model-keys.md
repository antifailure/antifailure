---
title: Your own model key
description: Bring an Anthropic or OpenAI key, keep it on your machine, point it at a local model, and prove it works, all from a terminal.
sidebar:
  order: 20
---

The agents drive a real browser. To read a page and decide what a person would
do next, they need a model, and the key is yours. It stays on your machine, the
call goes straight to the provider, and nothing hosted is involved.

Everything on this page works with no account, no control plane and no network
except the one call to your provider.

```sh
af model set anthropic   # asks for the key, without echoing it
af model test            # one cheap call: does it work?
af model show            # what is configured, and where it came from
```

## Two ways to bring a key

They are different arrangements and the right one depends on whether you have a
control plane.

| | `af model` | [`af provider`](/docs/guides/provider-keys) |
| --- | --- | --- |
| Where the key lives | this machine | the control plane, sealed |
| Who calls the provider | this machine | the control plane |
| Monthly spending cap | none | checked before the key is decrypted |
| Needs an account | no | yes |

If you have a control plane, prefer `af provider`. A cap is only a cap when
something you control checks it before the money is spent; a key handed to a
build machine is spent by that machine and you find out afterwards, if at all.

If you do not, this page is the whole story and nothing here is a lesser
version of it.

### Having both is the one combination to watch

Nothing routes a run through your control plane on its own. Reaching the sealed
key means pointing the base URL at the gateway yourself:

```sh
export ANTHROPIC_BASE_URL=https://your-control-plane/byok/anthropic
export ANTHROPIC_API_KEY=<your Antifailure token>
```

So a local key and a capped key on a control plane can both exist, and **the
local one wins**, because it is the one the runner reaches without being told
anything. That is the right precedence: the base URL is an explicit instruction
and a stored key is a default. It is also the more expensive way round to be
wrong, since somebody who ran `af provider budget anthropic 50` has a ceiling
they believe in and are not getting.

`af model show` and `af doctor` say so rather than leaving you to notice:

```
warn  Model key    anthropic/claude-sonnet-5 from the system keyring, not capped
```

They check only whether this machine is signed in to a control plane, which is
a local read and not a request, so the warning appears whenever a cap could
have been in force and never on a machine that has no control plane at all.

## With no key at all

Runs work. This is worth saying plainly because it is the thing people assume
is not true: without a key, workflows still run, still drive a real browser,
still sign in, still capture evidence and still produce a verdict. The
deterministic planner takes over, which follows a workflow by matching its
expectations against the page.

What a model adds is tolerance for a workflow written as a sentence. "Sign up
and confirm you land on a signed in page" is something the deterministic
planner half understands and a model follows without being told every field.

`af doctor` reports this as a pass rather than a warning, because it is a
supported mode:

```
ok    Model key    none set, so agents use the deterministic planner
```

## Storing a key

The key is never an argument, and there is no `--key` flag. A secret on a
command line is written to your shell's history file. It is visible in `ps` to
every other user on the machine. It is captured by any recording of the
terminal. That is three exposures before it is used once.

So there are three ways to give it, and none of them put it in the argument
vector.

```sh
af model set anthropic                       # asks, without echoing
af model set anthropic --stdin < key.txt     # reads one line
af model set anthropic --from-env MY_KEY     # reads that variable
```

`anthropic` and `openai` are the providers the agents can use.

### Where it goes

Into the **system keyring** where the platform has one, which is the macOS
keychain, the freedesktop Secret Service on Linux, and the Credential Manager
on Windows. That is the only place on a workstation where a secret is protected
by something other than file permissions.

Some machines have no keyring: a Linux server without `libsecret`, most
containers, and the platforms with no credential store at all. There the key
goes into the **encrypted local store**, a file under `.antifailure`. It is
sealed with AES-256-GCM under a key derived from a passphrase with Argon2id.

That store needs `AF_SECRET_PASSPHRASE`, and there is deliberately no default.
With no keyring and no passphrase there is nowhere to write a key. The command
says so rather than writing a file that only looks encrypted:

```
AF-SEC-004 The encrypted local store has no passphrase: no system keyring
answered and AF_SECRET_PASSPHRASE is not set.
```

The command always says which of the two it used, because they do not have the
same properties and "stored" for either would hide the difference that matters.

## Where a key is looked up

The same order every other secret in this product uses, most specific first:

1. **This shell's environment**, `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`.
2. **`.env`** in the repository.
3. **The encrypted local store**, under `.antifailure`.
4. **The system keyring**, which is what `af model set` writes to.

The first source that has a key wins. An export beats a stored key, which is
deliberate: somebody who typed one meant it and is usually trying a different
key for one run. There is one precedence rule in this product rather than a
separate one for models, which is the whole reason to reuse the chain.

With keys for both providers, Anthropic is used.

### When the key you stored is not the key in use

This is the most common first-run surprise: a key exported months ago in a
shell profile, a fresh one stored today, and every run quietly using the old
one. Nothing is broken and nothing normally says anything, so both commands say
it:

```
$ af model set anthropic

  Stored the anthropic key in the system keyring.

  It is not the key runs will use. ANTHROPIC_API_KEY is also set
  in this shell's environment, which is asked first.
  Unset it there, or storing this one has no effect.
```

It names where the other key is rather than only that there is one, because
"unset it" is not advice until you know which file to open.

When nothing shadows the key you just stored, that paragraph does not appear
and the command ends with `Check it works: af model test`.

`af model show` reports it from the other direction, naming the source that won
and the one being shadowed.

## Proving it works

```
$ af model test

Model
  Asking https://api.anthropic.com for one token as claude-sonnet-5...

  The key works. api.anthropic.com answered as claude-sonnet-5.
  412 ms, fingerprint 8f2c41ad.
```

One real completion of a single token, which costs a fraction of a cent. A real
call rather than a check of the key's shape, because a well formed key that was
revoked this morning passes every shape check there is.

Every failure it can tell apart, it tells apart, because they have different
fixes and being told only that the call failed sends you to the wrong one:

| What happened | What it says |
| --- | --- |
| The key is revoked or wrong | Store the right one. A key that worked yesterday was revoked or rotated. |
| The account has no credit | The key is valid and there is nothing to spend. Retrying will not help. |
| The model name does not exist | Set `AF_MODEL` to a model this key can use, or unset it. |
| Rate limited | The key works. Wait and run it again; nothing needs changing. |
| The provider is down | This says nothing about the key. |
| Nothing answered | Check this machine can reach the endpoint. |
| It answered, but not with a completion | The endpoint is not speaking the provider's API. |

The last one matters more than it looks. A reverse proxy in front of a model
that is not running answers `200` with an error page, and reporting that as a
working key would certify a setup that fails on the first real run.

A success is written down, and `af model show` and `af doctor` report it:

```
ok    Model key    anthropic/claude-sonnet-5 from the system keyring, verified 2026-08-30
```

The note is tied to the exact key that was verified. Rotate the key and it is
discarded rather than shown beside the new one, which would be a lie in exactly
the situation where you are checking whether a rotation worked.

A key that is set and has never been tested is a **warning** rather than a
pass. A revoked key and a working one are indistinguishable without making a
call, and the difference costs a whole run to discover.

## A local model, or a gateway

Point the base URL somewhere else. This is a first class path: it is tested,
and the failures it produces have their own advice.

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:11434
export OPENAI_BASE_URL=http://127.0.0.1:8080
af model test
```

The endpoint has to speak the provider's own API, because that is what the
runner and the sidecar send. Concretely, for `anthropic` it must accept
`POST {base}/v1/messages` with an `x-api-key` header and answer with the
provider's response shape; for `openai` it must accept
`POST {base}/v1/chat/completions` with a bearer token and answer with
`choices[].message.content`. Most local servers and gateways offer an
OpenAI compatible mode, and that is the one to point `OPENAI_BASE_URL` at.

Set the base URL to the **base**, without the path on the end. A `404` from a
custom endpoint says so, because "your model name is wrong" would be the wrong
half of the message when the real problem is that the gateway does not serve
that path.

A local model loading its weights for the first time can take longer than any
hosted one ever does. A timeout there is not a sign anything is wrong; the
answer is `af model test --timeout 5m`.

`af model show` and `af doctor` both name a custom endpoint explicitly, so a
run that is quietly going somewhere unexpected is visible rather than something
you have to remember. A control plane gateway is named as that rather than as
an anonymous custom endpoint, because it is the one destination that changes
what the key means:

```
Endpoint       https://your-control-plane/byok/anthropic  (your control plane, where the monthly cap applies)
```

## Egress policy does not switch the model off

This is worth being explicit about, because this product's whole job is
intercepting and controlling outbound HTTP, and a model call is outbound HTTP.

**A `default: block` manifest does not stop the agents planning with a model,
and you do not have to name your model provider in the manifest.**

The policy applies to traffic *through* the sidecar. Services sit on a network
with no route out and every name they resolve points at the sidecar, so their
packets have nowhere else to go. Neither model caller is on that network:

- The **runner** is a subprocess of `af` on your own machine, outside the
  environment entirely.
- A **synth** rule's model call originates *in* the sidecar, which is the one
  container with a route out. It is made with the sidecar's own client, not
  through the engine that decides about everybody else's traffic.

What the policy does govern is the **application under test** calling a model.
If your own code calls `api.anthropic.com`, that is traffic through the sidecar
like any other, and under `default: block` it is refused until a rule names it.
`af net log` shows the refusal. The same key in the same run can be reached
from two places for two reasons, so it is worth knowing which one you are
looking at.

If a model call does fail, `af model test` says whether this machine can reach
the endpoint, and it says in as many words that the manifest is not what is
stopping it.

## What leaves your machine

The request to the provider, and nothing else.

**The model never sees the page's HTML.** It sees the accessibility snapshot,
which is the page's URL and title, the form fields and controls by their
accessible names, and the rendered text of the body. That is what a person
navigating with a screen reader gets, it is enough to decide from, and it keeps
whatever is in the DOM out of somebody else's logs. There is no cookie, no
local storage, no request body and no markup in the prompt.

The model is also confined to what is on the page. It chooses from a fixed set
of actions against names that are actually there, so it cannot invent a button;
anything it names that is not on the page is refused rather than attempted.

Your key is not sent anywhere except the provider. It is never written to an
event, an artifact, a log line or a support bundle. It is registered with the
redactor before it is handed to any subprocess, so output that quotes it is
scrubbed on the way back.

Nothing prints a key back. There is no `af model get`, no `--show` flag and no
scope that would grant one. What any screen can read is the provider, the
endpoint, the source, and a short non-reversible fingerprint. That is enough to
answer the question this is usually asked: whether the key here is the one you
think it is.

If a key ever ends up in a place that will not answer, a custom endpoint is
still somebody else's code. A gateway that echoes your key back in an error
message cannot get it onto your terminal: provider text is redacted before it
is printed.

## Rotating

Store the new key. It replaces whatever was there.

```sh
af model set anthropic --stdin < new-key.txt
af model test
```

Rotating discards the previous verification, so `af model show` will say the
new key has never been tested until you test it.

**This does not reach the provider.** Storing a key here does not create one
and removing one does not revoke one. If a key leaked, revoke it at Anthropic
or OpenAI as well.

## Removing

```sh
af model rm anthropic
```

It clears the key from both places this can write, not from the first that
answers. A key left in the encrypted store after the keyring entry was removed
is a key the next run silently uses, which is the exact failure you are trying
to prevent.

It cannot reach a key you exported in a shell or wrote into a `.env`. It says
so rather than reporting a removal that changed nothing:

```
  Removed the anthropic key from the system keyring.

  A ANTHROPIC_API_KEY is still supplied by this shell's environment, so runs
  will keep using one. This command cannot reach there; unset it yourself.
```

Removing a key that is not there is not an error. This is a command people run
in a hurry, and a retry after a timeout must not report failure for reaching
the state you asked for.

## In CI

CI has no keyring and no terminal. Use the platform's own secret store and
export the variable, which is the first source in the chain:

```yaml
- run: af test
  env:
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

`af model set anthropic --from-env ANTHROPIC_API_KEY` is there for a runner that has the key in a variable
and wants it in the store as well. With neither a terminal nor `--stdin` the
command refuses rather than reading. A read from a stdin nobody is typing into
either blocks forever or returns nothing at once, and both look like a network
problem in a CI log.

## Choosing a model

`AF_MODEL` picks the model for whichever provider's key is in use.

```sh
export AF_MODEL=claude-opus-5
```

Unset, it is `claude-sonnet-5` for Anthropic and `gpt-4.1` for OpenAI.
