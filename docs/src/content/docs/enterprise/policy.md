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

## Writing the policy down

A policy is a YAML document, and the engine reads it from the path in
`AF_ORG_POLICY_FILE`:

```sh
export AF_ORG_POLICY_FILE=/etc/antifailure/policy.yaml
```

```yaml
# Every key is a restriction. There is no key that grants anything.
required_masked_columns:
  - "*.email"
  - "customers.card_number"
denied_hosts:
  - api.stripe.com
allowed_modes:
  - block
  - capture
  - mock
synth_requires_approval: true
allowed_providers:
  - neon
allowed_regions:
  - westeurope
```

`required_masked_columns` is `table.column` with `*` allowed in either part. A
pattern that matches no column in the database counts as unsatisfied, because a
policy that quietly passes when the thing it protects is absent stops protecting
the moment somebody renames a table.

`denied_hosts` refuses a host named in any mode other than `block`. A repository
may still write a `block` rule for one, so that it can document what it
deliberately refuses.

The engine prints which rules are in force at startup, on standard error:

```
af: organization policy: egress deny list (1 hosts)
af: organization policy: required masking (*.email, customers.card_number)
```

A file you named that cannot be read, cannot be parsed, or carries a key this
build does not know stops the engine with the reason. That is deliberate:
starting anyway means every environment is created without being checked and
nothing in the output says so, which is exactly the behaviour the policy exists
to change.

Setting nothing registers nothing and prints nothing, which is the ordinary
case for an installation with no organization policy.

Approvals live in the control plane and this file does not carry them, so
`synth_requires_approval` refuses every synth rule when the engine reads its
policy from a file. A lookup that answered "approved" because it had nowhere to
ask would turn the rule into decoration.

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
