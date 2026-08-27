---
title: Policy enforcement
description: Organisation rules that decide whether an environment may exist.
sidebar:
  order: 2
---

*Requires an enterprise license with the `policy_enforcement` feature.*

A policy is an organisation rule checked before an environment is created. It
can refuse.

```
AF-EE-010 Organization policy no-unmasked-goldens refuses this environment:
the golden gv_20260826120000_a1b2c3d4 was published without a verification
attestation.
  Next: Ask an organization administrator to review no-unmasked-goldens, or
  bring the repository into compliance.
```

## Where it runs

The check happens before anything is created, not after. A policy that refused
an environment halfway through would leave resources behind and a decision
nobody can act on.

The extension point it uses is in the community edition, in
`engine/pkg/extension`. That is deliberate: the socket is MIT so that anybody
can write a hook, and the enterprise edition supplies one implementation of it.

## Hooks can only refuse

A hook returns a refusal or nothing. It cannot permit something the engine would
otherwise refuse.

That asymmetry is the whole safety property. A hook that could grant permission
would be a way to switch off masking verification, egress policy, or tenant
isolation from outside the engine, and none of those should have an off switch
that lives in somebody's plugin.

## Writing one

```go
type PolicyHook interface {
    // Returns an error to refuse. Nil permits nothing; it declines to object.
    Check(ctx context.Context, req Request) error
}
```

Register it with the engine's extension registry. The community build registers
nothing, so the check iterates an empty slice and returns nil.

Related: [licensing](/docs/enterprise/licensing/), [egress](/docs/concepts/egress/).
