---
title: Sandbox credentials
description: How a live key stays outside the environment while sandbox calls still work.
sidebar:
  order: 6
---

In `sandbox` mode the application never holds the credential it appears to use.

```yaml
egress:
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
```

The container gets a placeholder in `STRIPE_SECRET_KEY`. The proxy substitutes
the sandbox key on the way out. The live key is never inside the environment,
so nothing that reads the container's environment, filesystem, or process list
can find it: not a crash dump, not a debug endpoint that prints `process.env`,
not a dependency doing something it should not.

There is a conformance test that starts a container and asserts exactly that.

## Where the sandbox key comes from

The same chain as every other secret: an exported variable, then `.env`, then
the encrypted local store. The manifest names the variable and never the value.

```sh
export STRIPE_SECRET_KEY_SANDBOX=sk_test_...
```

## A live key is refused

```
AF-SEC-003 The value supplied for STRIPE_SECRET_KEY carries a live credential
prefix, and STRIPE_SECRET_KEY is configured for sandbox use.
  Next: Point STRIPE_SECRET_KEY at a sandbox credential; the environment must
  never hold a live key.
```

Checked before anything starts, by prefix, so a live key cannot be handed to a
sandbox rule by accident. This is the same detector CI runs over the repository
and the proxy runs over outbound requests: one definition of what a live
credential looks like, in all three places.

## The sandbox rejects it

```
AF-NET-005 The sandbox credential for api.stripe.com was rejected: invalid api
key provided.
```

The substitution worked and the key is wrong. Usually a sandbox key from a
different account than the webhook signing secret, or one that was rotated.

## Providers with no sandbox

Use [`mock`](/guides/mocking/) with a fixture pack, or [`synth`](/guides/synth/)
where a fixture would have to be invented anyway. `block` is also an answer:
an environment that cannot reach a service is an environment that tells you
what your application does when that service is down.

Related: [egress](/concepts/egress/), [secrets](/guides/secrets/).
