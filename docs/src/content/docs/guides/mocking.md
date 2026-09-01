---
title: Mocking
description: Answering an API from fixtures when it has no sandbox worth using.
sidebar:
  order: 13
---

Some third party APIs have no sandbox, or one that does not resemble the real
thing. `mock` answers them from a fixture pack, with no network involved at all.

```yaml
egress:
  rules:
    - host: api.clearbit.com
      mode: mock
      fixtures: ./fixtures/clearbit
      note: "no sandbox; these are recordings of real responses"
```

## Fixture packs

A pack is a directory of recorded request and response pairs. Packs for common
APIs ship with the engine and need no `fixtures` path; a pack in the repository
is for your own third parties and for cases the shipped ones do not cover.

Matching is by method, path, and where it matters the body. The most specific
match wins, so a general fixture for `GET /v1/people/*` and a specific one for
one identifier can coexist.

## Nothing matched

```
AF-NET-010 No mock matched GET /v1/companies/find on api.clearbit.com.
  Next: Add a fixture for it, or change the rule to another mode.
```

The request is refused rather than answered with something invented, because a
plausible wrong answer is worse than a refusal: it produces a green run that
proves nothing.

Three ways forward: record the fixture, use [`synth`](/docs/guides/synth/) if the
shape matters more than the content, or set the host to `block` and check what
your application does when the service is unavailable.

## Recording

Point a rule at a real sandbox in `sandbox` mode, run the flow, and read
`af net log`: every request and response is there, which is the material a
fixture is made from.

## Mock and capture

`capture` records what your application sent and answers with the provider's
success shape. `mock` answers with content from a fixture. Use `capture` when
you only need the call to succeed, such as sending mail, and `mock` when your
application reads the response and does something with it.

Related: [egress](/docs/concepts/egress/), [synth](/docs/guides/synth/).
