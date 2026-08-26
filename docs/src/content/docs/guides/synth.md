---
title: Synthesis
description: Answering an API with a model, when a fixture would have to be invented anyway.
sidebar:
  order: 13
---

`synth` answers a request with a model, given the API's shape and the request
that was made. It is for the case where there is no sandbox, no fixture, and
writing one by hand means inventing content anyway.

```yaml
egress:
  rules:
    - host: api.enrichment.example
      mode: synth
      note: "no sandbox; responses are shaped like the real ones, not real"
```

## When it is the right answer

An API whose responses are descriptive rather than transactional: enrichment,
classification, summarisation, recommendation. Your application reads the shape
and does something with the content, and the content does not need to be
correct for the flow to be exercised.

## When it is not

Anything transactional. Payments, authentication, anything with an identifier
your application will use later. A synthesised charge id is a charge id that
does not exist, and the failure arrives one step further along where it is
harder to read.

Use [`mock`](/guides/mocking/) for those, or a real sandbox.

## Determinism

The same request in the same environment gets the same answer, so a re-run does
not produce a different result and a flaky test is a flaky test rather than a
different fixture. Across environments answers differ, because they are
generated rather than recorded.

## When it produces nothing usable

```
AF-NET-030 The synthesis model returned no usable response for GET
/v1/enrich?domain=example.com.
  Next: Add a fixture for this request, or set the host to block.
```

Usually a response shape the model could not infer from the request alone.
A fixture for that one path fixes it, and the rest of the host can stay in
`synth`: rules can be narrowed by `paths`.

## The model key

Passed to the sidecar as an environment variable rather than written to a file,
so it never lands on disk inside an environment. It is resolved from the same
chain as every other secret.

Synthesis is off unless a rule asks for it. An environment that quietly called
a model for every unmatched request would be a surprising bill.

Related: [egress](/concepts/egress/), [mocking](/guides/mocking/).
